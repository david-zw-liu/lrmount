package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
	"github.com/david-zw-liu/lrmount/internal/device"
	"github.com/david-zw-liu/lrmount/internal/locate"
	"github.com/david-zw-liu/lrmount/internal/mountctl"
	"github.com/david-zw-liu/lrmount/internal/nfsgate"
)

// lightroomBundleIDs are probed in order; the iPhone app comes first.
var lightroomBundleIDs = []string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}

var rootCmd = &cobra.Command{
	Use:           "lrmount",
	Short:         "Mount each iPhone/iPad Lightroom app's Documents as an ejectable Finder volume",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          func(cmd *cobra.Command, args []string) error { return run() },
}

func Execute() error { return rootCmd.Execute() }

type volume struct {
	name         string // device name → Finder volume label
	hints        []string
	mountpoint   string
	ln           net.Listener
	ejected      bool // set once this volume is torn down (single supervise goroutine)
	shutdownOnce sync.Once
}

// shutdown closes the volume's NFS listener and removes its mountpoint dir.
// The supervise loop is single-goroutine so this is never actually reentered;
// shutdownOnce is cheap defense-in-depth.
func (v *volume) shutdown() {
	v.shutdownOnce.Do(func() {
		v.ln.Close()
		mountctl.Cleanup(v.mountpoint)
	})
}

// genOutcome is how one mount "generation" (the volumes for one continuous
// span of device connectivity) ended.
// deviceMount is one device's live volume and the AFC sessions behind it.
type deviceMount struct {
	vol      *volume
	sessions []*device.Session
	ejected  bool // user ejected it in Finder; leave alone until it is replugged
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Print(warningBanner())
	fmt.Println("Mounting every USB-connected device (Wi-Fi devices are ignored).")
	fmt.Println("Eject a device in Finder to unmount just that one; lrmount quits when the last")
	fmt.Println("is ejected. Unplugging a device ejects it but keeps running. Ctrl-C also quits.")

	// Resident daemon keyed by udid. Each tick reconciles the mounted set
	// against the USB devices usbmuxd reports.
	mounts := map[string]*deviceMount{}
	warned := map[string]bool{} // suppress repeated mount-failure logs per device

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownAll(mounts)
			fmt.Println("\nDone. Reopen Lightroom so it rebuilds its preset index.")
			return nil
		case <-tick.C:
			usb, err := device.List()
			if err != nil {
				continue // transient usbmux error; retry next tick
			}
			present := make(map[string]device.Info, len(usb))
			for _, d := range usb {
				present[d.UDID] = d
			}
			reconcileGone(mounts, warned, present)
			// A Finder eject unmounts just that device. When it was the last
			// one still mounted, the user is done, so quit; otherwise keep
			// serving the rest. (An unplug leaves the mount in the table and
			// is handled by reconcileGone, so it stays resident instead.)
			if reconcileEjected(mounts) && activeMounts(mounts) == 0 {
				shutdownAll(mounts)
				fmt.Println("\nDone. Reopen Lightroom so it rebuilds its preset index.")
				return nil
			}
			reconcileNew(mounts, warned, present)
		}
	}
}

// reconcileGone force-ejects and forgets any mounted device that is no longer
// on USB, so reconnecting it remounts fresh.
func reconcileGone(mounts map[string]*deviceMount, warned map[string]bool, present map[string]device.Info) {
	for udid, dm := range mounts {
		if _, ok := present[udid]; ok {
			continue
		}
		if !dm.ejected {
			fmt.Fprintf(os.Stderr, "%s disconnected — ejecting\n", dm.vol.name)
			forceEject(dm.vol)
		}
		closeSessions(dm.sessions)
		delete(mounts, udid)
		delete(warned, udid)
	}
}

// activeMounts counts devices still serving a mounted volume (not yet ejected).
func activeMounts(mounts map[string]*deviceMount) int {
	n := 0
	for _, dm := range mounts {
		if !dm.ejected {
			n++
		}
	}
	return n
}

// reconcileEjected tears down the server of any still-connected device whose
// volume the user ejected in Finder, and reports whether that happened — the
// caller treats a Finder eject as "done" and exits.
func reconcileEjected(mounts map[string]*deviceMount) bool {
	ejected := false
	for _, dm := range mounts {
		if dm.ejected || mountctl.IsMounted(dm.vol.mountpoint) {
			continue
		}
		fmt.Printf("ejected  %s\n", dm.vol.mountpoint)
		dm.vol.shutdown()
		closeSessions(dm.sessions)
		dm.ejected = true
		ejected = true
	}
	return ejected
}

// reconcileNew mounts any USB device not already tracked. Detection failures
// (device locked/settling, no Lightroom) are logged once and retried.
func reconcileNew(mounts map[string]*deviceMount, warned map[string]bool, present map[string]device.Info) {
	for udid, info := range present {
		if _, ok := mounts[udid]; ok {
			continue
		}
		name := info.Name
		if name == "" {
			name = udid
		}
		sessions, err := device.DetectSessions(info, lightroomBundleIDs)
		if err != nil {
			if !warned[udid] {
				fmt.Fprintf(os.Stderr, "[%s] %v — will retry\n", name, err)
				warned[udid] = true
			}
			continue
		}
		mounted := mountDevice(sessions, name)
		if len(mounted) == 0 {
			closeSessions(sessions)
			if !warned[udid] {
				fmt.Fprintf(os.Stderr, "[%s] no usable Lightroom Documents — will retry\n", name)
				warned[udid] = true
			}
			continue
		}
		mounts[udid] = &deviceMount{vol: mounted[0], sessions: sessions}
		delete(warned, udid)
	}
}

// shutdownAll gracefully unmounts every still-mounted device (Ctrl-C, or the
// remaining volumes after a Finder eject); a second Ctrl-C forces any that
// stay busy. Already-ejected devices only have their sessions closed.
func shutdownAll(mounts map[string]*deviceMount) {
	pending := false
	for _, dm := range mounts {
		if !dm.ejected {
			pending = true
			break
		}
	}
	force := make(chan os.Signal, 1)
	if pending {
		fmt.Println("\nunmounting…")
		signal.Notify(force, os.Interrupt)
		defer signal.Stop(force)
	}
	for _, dm := range mounts {
		if !dm.ejected {
			unmountWithRetry(dm.vol, force)
		}
		closeSessions(dm.sessions)
	}
}

// catRef locates one catalog's userStyles so its mounted path can be printed
// once the mountpoint is known.
type catRef struct {
	app        string
	root       string
	devicePath string
}

// mountDevice serves every Lightroom app on the device through one NFS server
// — each app a virtual subdirectory (Router) — and mounts it once as
// ~/lrmount/<device>, so Finder shows "<device>" containing "<app>/…". It
// returns a single-element slice (or nil if nothing mounted); the plural
// shape keeps the supervise loop uniform.
func mountDevice(sessions []*device.Session, deviceName string) []*volume {
	var entries []afcfs.NamedFS
	var refs []catRef
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] skipped: %v\n", s.BundleID, err)
			continue
		}
		label := appLabel(s.BundleID)
		entries = append(entries, afcfs.NamedFS{Name: label, FS: afcfs.Rooted(s.FS, docs)})
		if cands, err := locate.FindCatalogs(s.FS, docs); err == nil {
			for _, c := range cands {
				refs = append(refs, catRef{app: label, root: docs, devicePath: c.UserStyles})
			}
		}
	}
	if len(entries) == 0 {
		return nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		return nil
	}
	router := afcfs.NewRouter(entries)
	go func() {
		if err := nfsgate.Serve(ln, router, ""); err != nil && !errors.Is(err, net.ErrClosed) {
			fmt.Fprintf(os.Stderr, "nfs server: %v\n", err)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	mp, err := mountAt(deviceName, port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		ln.Close()
		return nil
	}

	v := &volume{name: deviceName, mountpoint: mp, ln: ln}
	for _, r := range refs {
		v.hints = append(v.hints, hintPath(mp, r.app, r.root, r.devicePath))
	}
	fmt.Printf("mounted  %s\n", mp)
	for _, h := range v.hints {
		fmt.Printf("  presets: %s\n", h)
	}
	return []*volume{v}
}

// forceEject tears a volume down without a graceful flush — used when the
// device is already gone, so there is nothing to flush to. Data the NFS
// client had buffered but not yet written is lost; that is inherent to
// pulling the cable.
func forceEject(v *volume) {
	if v.ejected {
		return
	}
	v.ejected = true
	if mountctl.IsMounted(v.mountpoint) {
		if err := mountctl.Unmount(v.mountpoint, true); err != nil {
			fmt.Fprintf(os.Stderr, "force unmount %s: %v\n", v.mountpoint, err)
		}
	}
	v.shutdown()
}

func closeSessions(sessions []*device.Session) {
	for _, s := range sessions {
		s.Close()
	}
}

// unmountWithRetry keeps trying a graceful unmount (which flushes like a
// Finder eject); a second Ctrl-C escalates to a forced unmount.
func unmountWithRetry(v *volume, force <-chan os.Signal) {
	defer func() {
		v.shutdown()
	}()
	for mountctl.IsMounted(v.mountpoint) {
		err := mountctl.Unmount(v.mountpoint, false)
		if err == nil {
			fmt.Printf("ejected  %s\n", v.mountpoint)
			return
		}
		fmt.Fprintf(os.Stderr, "%s is busy — close open files, retrying in 2s (Ctrl-C again to force)\n", v.mountpoint)
		select {
		case <-force:
			if err := mountctl.Unmount(v.mountpoint, true); err != nil {
				fmt.Fprintf(os.Stderr, "force unmount %s: %v\n", v.mountpoint, err)
			}
			return
		case <-time.After(2 * time.Second):
		}
	}
}
