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
	shutdownOnce sync.Once
}

// shutdown closes the volume's NFS listener and removes its mountpoint dir.
// Both the eject watcher and the Ctrl-C unmount path may reach this; Once
// makes the two paths safe against each other.
func (v *volume) shutdown() {
	v.shutdownOnce.Do(func() {
		v.ln.Close()
		mountctl.Cleanup(v.mountpoint)
	})
}

func run() error {
	// 1. Pick device.
	infos, err := device.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return fmt.Errorf("no USB device found; connect and trust your device")
	}
	chosen := infos[0]
	if len(infos) > 1 {
		labels := make([]string, len(infos))
		for i, d := range infos {
			labels[i] = fmt.Sprintf("%s  (%s, iOS %s, %s)", d.Name, d.ProductType, d.Version, d.UDID)
		}
		idx, err := pickIndex("Select a device", labels)
		if err != nil {
			return err
		}
		chosen = infos[idx]
	}

	// 2. Open a session per installed Lightroom app.
	sessions, err := device.DetectSessions(chosen.UDID, lightroomBundleIDs)
	if err != nil {
		return err
	}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()
	multi := len(sessions) > 1

	// 3. Build volume descriptions.
	var vols []*volume
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] skipped: %v\n", s.BundleID, err)
			continue
		}
		v := &volume{sess: s, root: docs, name: volumeName(chosen.Name, s.BundleID, multi)}
		if cands, err := locate.FindCatalogs(s.FS, docs); err == nil {
			for _, c := range cands {
				v.hints = append(v.hints, c.UserStyles)
			}
		}
		vols = append(vols, v)
	}
	if len(vols) == 0 {
		return fmt.Errorf("no Lightroom app with a usable Documents folder found")
	}

	fmt.Print(warningBanner())

	// 4. Serve + mount every volume; failures skip that volume only.
	var mounted []*volume
	for _, v := range vols {
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
		if err := mountctl.MountNFS(mp, port); err != nil {
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
	if len(mounted) == 0 {
		return fmt.Errorf("all volumes failed to mount")
	}
	fmt.Println("\nEject the volume(s) in Finder when done, or press Ctrl-C.")

	// 5. Wait for ejects; Ctrl-C unmounts what is left.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	for _, v := range mounted {
		wg.Add(1)
		go func(v *volume) {
			defer wg.Done()
			if err := mountctl.WaitUnmount(ctx, v.mountpoint); err != nil {
				return // Ctrl-C: main unmounts below
			}
			fmt.Printf("ejected  %s\n", v.mountpoint)
			v.shutdown()
		}(v)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done: // every volume ejected in Finder
	case <-ctx.Done(): // Ctrl-C
		fmt.Println("\nunmounting…")
		force := make(chan os.Signal, 1)
		signal.Notify(force, os.Interrupt)
		defer signal.Stop(force)
		for _, v := range mounted {
			unmountWithRetry(v, force)
		}
		wg.Wait()
	}

	fmt.Println("\nAll volumes ejected. Reopen Lightroom so it rebuilds its preset index.")
	return nil
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
