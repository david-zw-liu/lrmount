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
	sess         *device.Session
	name         string
	root         string // Documents root on the device
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
type genOutcome int

const (
	genDisconnected genOutcome = iota // cable pulled / device gone → wait for reconnect
	genAllEjected                     // every volume ejected in Finder → exit
	genCtrlC                          // interrupt → exit
)

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Print(warningBanner())

	// Resident supervise loop. lrmount never exits merely because no device
	// is attached: it waits for one to appear, mounts its volumes, and keeps
	// running across unplug/replug. It exits only when the user ejects every
	// volume in Finder or presses Ctrl-C.
	var chosenUDID, chosenName string
	for {
		// Resolve a device to work with. Empty udid means we have not picked
		// one yet (startup, or after all volumes were ejected on a prior
		// device); otherwise wait for the chosen device to (re)appear.
		if chosenUDID == "" {
			info, ok := waitAndPickDevice(ctx)
			if !ok {
				break // Ctrl-C
			}
			chosenUDID, chosenName = info.UDID, info.Name
		} else if present, _ := device.Present(chosenUDID); !present {
			fmt.Fprintln(os.Stderr, "\nWaiting for the device to reconnect… (Ctrl-C to quit)")
			if !waitForReconnect(ctx, chosenUDID) {
				break // Ctrl-C
			}
			fmt.Fprintln(os.Stderr, "Device reconnected — remounting…")
		}

		sessions, err := device.DetectSessions(chosenUDID, lightroomBundleIDs)
		if err != nil {
			// Device present but not ready (locked, still settling after
			// replug, or Lightroom not installed). Retry; never fatal.
			fmt.Fprintf(os.Stderr, "detecting Lightroom: %v — retrying…\n", err)
			if !sleepOrDone(ctx, 2*time.Second) {
				break
			}
			continue
		}

		mounted := mountVolumes(sessions, chosenName)
		if len(mounted) == 0 {
			closeSessions(sessions)
			fmt.Fprintln(os.Stderr, "no Lightroom app with a usable Documents folder — retrying…")
			if !sleepOrDone(ctx, 3*time.Second) {
				break
			}
			continue
		}
		fmt.Println("\nEdit presets in Finder. Eject a volume when done, or press Ctrl-C.")
		fmt.Println("Unplugging the cable auto-ejects and re-mounts when you reconnect.")

		outcome := superviseGeneration(ctx, chosenUDID, mounted)
		closeSessions(sessions)
		switch outcome {
		case genDisconnected:
			// Keep the chosen udid and loop back to wait for reconnect.
			continue
		case genAllEjected:
			// User is done with this device; forget it and wait for the next
			// one instead of exiting.
			fmt.Fprintln(os.Stderr, "\nAll volumes ejected. Waiting for a device… (Ctrl-C to quit)")
			chosenUDID, chosenName = "", ""
			continue
		case genCtrlC:
			return nil
		}
	}

	return nil
}

// waitAndPickDevice blocks until at least one device is attached, then returns
// it (prompting a menu when several are present). It reports false only on
// Ctrl-C. The "waiting" notice is printed once, not every poll.
func waitAndPickDevice(ctx context.Context) (device.Info, bool) {
	announced := false
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		infos, err := device.List()
		if err == nil && len(infos) > 0 {
			if len(infos) == 1 {
				return infos[0], true
			}
			labels := make([]string, len(infos))
			for i, d := range infos {
				labels[i] = fmt.Sprintf("%s  (%s, iOS %s, %s)", d.Name, d.ProductType, d.Version, d.UDID)
			}
			idx, err := pickIndex("Select a device", labels)
			if err != nil {
				return device.Info{}, false
			}
			return infos[idx], true
		}
		if !announced {
			fmt.Println("Waiting for a USB device… connect and trust it. (Ctrl-C to quit)")
			announced = true
		}
		select {
		case <-ctx.Done():
			return device.Info{}, false
		case <-tick.C:
		}
	}
}

// mountVolumes opens an NFS server and Finder mount for each Lightroom app in
// sessions. A per-volume failure is reported and skipped; the returned slice
// holds only the volumes that mounted.
func mountVolumes(sessions []*device.Session, deviceName string) []*volume {
	multi := len(sessions) > 1
	var mounted []*volume
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] skipped: %v\n", s.BundleID, err)
			continue
		}
		v := &volume{sess: s, root: docs, name: volumeName(deviceName, s.BundleID, multi)}
		if cands, err := locate.FindCatalogs(s.FS, docs); err == nil {
			for _, c := range cands {
				v.hints = append(v.hints, c.UserStyles)
			}
		}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] listen: %v\n", v.name, err)
			continue
		}
		v.ln = ln
		go func(v *volume) {
			if err := nfsgate.Serve(v.ln, v.sess.FS, v.root); err != nil && !errors.Is(err, net.ErrClosed) {
				fmt.Fprintf(os.Stderr, "[%s] nfs server: %v\n", v.name, err)
			}
		}(v)
		mp, err := pickMountpoint(v.name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", v.name, err)
			ln.Close()
			continue
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := mountctl.MountNFS(mp, v.name, port); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", v.name, err)
			ln.Close()
			mountctl.Cleanup(mp)
			continue
		}
		v.mountpoint = mp
		mounted = append(mounted, v)
		fmt.Printf("mounted  %s\n", mp)
		for _, h := range v.hints {
			fmt.Printf("  presets: %s\n", hintPath(mp, v.root, h))
		}
	}
	return mounted
}

// superviseGeneration runs a single connectivity generation from one
// goroutine: it polls (a) device presence over usbmux — which never hangs on
// a dead AFC socket — and (b) each volume's mount state, so a Finder eject is
// noticed and cleaned up. It returns when the device vanishes, when every
// volume has been ejected, or on Ctrl-C.
func superviseGeneration(ctx context.Context, udid string, mounted []*volume) genOutcome {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	// A single missed presence poll can be a usbmux hiccup rather than an
	// unplug; require two consecutive confirmed absences before force-ejecting
	// (which discards buffered writes), so a transient blip doesn't tear down
	// a healthy mount.
	absences := 0
	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nunmounting…")
			force := make(chan os.Signal, 1)
			signal.Notify(force, os.Interrupt)
			defer signal.Stop(force)
			for _, v := range mounted {
				if !v.ejected {
					unmountWithRetry(v, force)
					v.ejected = true
				}
			}
			return genCtrlC
		case <-tick.C:
			// Device gone → force-eject every volume promptly (which also
			// clears macOS's "server not responding" state) and report the
			// disconnect so the caller waits for a reconnect. A usbmux query
			// error is treated as "unknown", not a disconnect.
			if present, err := device.Present(udid); err == nil && !present {
				if absences++; absences < 2 {
					continue
				}
				for _, v := range mounted {
					forceEject(v)
				}
				return genDisconnected
			}
			absences = 0
			allEjected := true
			for _, v := range mounted {
				if v.ejected {
					continue
				}
				if mountctl.IsMounted(v.mountpoint) {
					allEjected = false
					continue
				}
				v.ejected = true
				fmt.Printf("ejected  %s\n", v.mountpoint)
				v.shutdown()
			}
			if allEjected {
				return genAllEjected
			}
		}
	}
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

// waitForReconnect blocks until the device reappears over usbmux or ctx is
// cancelled; it reports false only on cancellation (Ctrl-C).
func waitForReconnect(ctx context.Context, udid string) bool {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
			if present, _ := device.Present(udid); present {
				return true
			}
		}
	}
}

// sleepOrDone waits for d or ctx cancellation; false means cancelled.
func sleepOrDone(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
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
