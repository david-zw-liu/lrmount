//go:build darwin

// Package mountctl drives macOS's built-in NFS client: mount, eject
// detection, unmount. Nothing here talks to the device.
package mountctl

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// mountEntry is one row of the kernel mount table.
type mountEntry struct {
	dir    string
	fstype string
}

// listMountsFn is swappable in tests.
var listMountsFn = listMounts

// listMounts reads the kernel mount table with MNT_NOWAIT so it reports cached
// state without contacting each filesystem's server. That is essential here:
// statfs on a not-responding NFS mount blocks or errors, but the mount is
// still registered — enumerating the table sees it and won't hang.
func listMounts() ([]mountEntry, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	bufs := make([]unix.Statfs_t, n)
	n, err = unix.Getfsstat(bufs, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}
	out := make([]mountEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, mountEntry{
			dir:    unix.ByteSliceToString(bufs[i].Mntonname[:]),
			fstype: unix.ByteSliceToString(bufs[i].Fstypename[:]),
		})
	}
	return out, nil
}

// MountNFS mounts the localhost NFS server listening on port at mountpoint.
// nolocks: go-nfs implements no NLM lock protocol. Default hard-mount
// semantics are kept deliberately: transient stalls retry instead of
// dropping writes.
//
// share becomes the path component of the mount source
// ("localhost:/<share>"). Finder derives the volume's displayed name from
// that path, so passing the volume name here is what makes Finder show
// "David's iPhone Lightroom" instead of "localhost". The NFS server ignores
// the mount dirpath, so any string works.
//
// Some macOS configurations refuse to bind the NFS client to a
// non-reserved (>1024) source port, causing the initial mount to fail.
// Rather than requiring the user to diagnose and add "resvport" by hand,
// retry once with it added: this keeps the tool zero-config for the
// common case while still working on stricter setups.
func MountNFS(mountpoint, share string, port int) error {
	source := "localhost:/" + share
	opts := fmt.Sprintf("port=%d,mountport=%d,tcp,vers=3,nolocks", port, port)
	out, err := exec.Command("/sbin/mount", "-t", "nfs", "-o", opts, source, mountpoint).CombinedOutput()
	if err == nil {
		return nil
	}

	retryOpts := opts + ",resvport"
	retryOut, retryErr := exec.Command("/sbin/mount", "-t", "nfs", "-o", retryOpts, source, mountpoint).CombinedOutput()
	if retryErr == nil {
		return nil
	}
	return fmt.Errorf("mount %s: first attempt: %w: %s; retry with resvport: %v: %s", mountpoint, err, out, retryErr, retryOut)
}

// IsMounted reports whether an NFS filesystem is currently mounted at
// mountpoint. It checks the kernel mount table (not statfs on the path), so a
// briefly not-responding NFS server still reads as mounted — only a real
// unmount (Finder eject or diskutil) removes the entry. That distinction is
// what keeps a transient stall from being mistaken for an eject.
func IsMounted(mountpoint string) bool {
	mounts, err := listMountsFn()
	if err != nil {
		return false
	}
	for _, m := range mounts {
		if m.dir == mountpoint && m.fstype == "nfs" {
			return true
		}
	}
	return false
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
