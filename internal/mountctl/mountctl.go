//go:build darwin

// Package mountctl drives macOS's built-in NFS client: mount, eject
// detection, unmount. Nothing here talks to the device.
package mountctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// swappable in tests
var (
	statfsFn     = unix.Statfs
	pollInterval = time.Second
)

// MountNFS mounts the localhost NFS server listening on port at mountpoint.
// nolocks: go-nfs implements no NLM lock protocol. Default hard-mount
// semantics are kept deliberately: transient stalls retry instead of
// dropping writes.
func MountNFS(mountpoint string, port int) error {
	opts := fmt.Sprintf("port=%d,mountport=%d,tcp,vers=3,nolocks", port, port)
	out, err := exec.Command("/sbin/mount", "-t", "nfs", "-o", opts, "localhost:/", mountpoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount %s: %w: %s", mountpoint, err, out)
	}
	return nil
}

// IsMounted reports whether mountpoint currently carries an NFS filesystem.
// After an eject, statfs sees the parent (apfs) filesystem instead.
func IsMounted(mountpoint string) bool {
	var st unix.Statfs_t
	if err := statfsFn(mountpoint, &st); err != nil {
		return false
	}
	return unix.ByteSliceToString(st.Fstypename[:]) == "nfs"
}

// WaitUnmount blocks until mountpoint stops being an NFS mount (Finder
// eject) or ctx is cancelled. The macOS NFS client flushes all dirty pages
// before an unmount can succeed, so returning nil here means every write
// has been acknowledged by the AFC layer.
func WaitUnmount(ctx context.Context, mountpoint string) error {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !IsMounted(mountpoint) {
				return nil
			}
		}
	}
}

// Unmount unmounts via diskutil, which performs the same flush as a Finder
// eject. force escalates a volume that stays busy.
func Unmount(mountpoint string, force bool) error {
	args := []string{"unmount"}
	if force {
		args = append(args, "force")
	}
	args = append(args, mountpoint)
	out, err := exec.Command("/usr/sbin/diskutil", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("diskutil unmount %s: %w: %s", mountpoint, err, out)
	}
	return nil
}

// Cleanup removes an empty leftover mountpoint directory; errors are
// ignored (a non-empty or already-removed dir is fine to leave).
func Cleanup(mountpoint string) { _ = os.Remove(mountpoint) }
