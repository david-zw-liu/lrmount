//go:build darwin

package mountctl

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fakeStatfs(fstype string, fail bool) func(string, *unix.Statfs_t) error {
	return func(_ string, st *unix.Statfs_t) error {
		if fail {
			return errors.New("no such file")
		}
		copy(st.Fstypename[:], fstype)
		return nil
	}
}

func TestIsMounted(t *testing.T) {
	orig := statfsFn
	defer func() { statfsFn = orig }()

	statfsFn = fakeStatfs("nfs", false)
	if !IsMounted("/Volumes/x") {
		t.Fatal("want mounted for nfs fstype")
	}
	statfsFn = fakeStatfs("apfs", false)
	if IsMounted("/Volumes/x") {
		t.Fatal("want unmounted for apfs fstype")
	}
	statfsFn = fakeStatfs("", true)
	if IsMounted("/Volumes/x") {
		t.Fatal("want unmounted on statfs error")
	}
}

func TestWaitUnmountReturnsOnEject(t *testing.T) {
	orig, origPoll := statfsFn, pollInterval
	defer func() { statfsFn, pollInterval = orig, origPoll }()
	pollInterval = 5 * time.Millisecond

	var calls atomic.Int32
	statfsFn = func(_ string, st *unix.Statfs_t) error {
		if calls.Add(1) < 3 {
			copy(st.Fstypename[:], "nfs")
		} else {
			copy(st.Fstypename[:], "apfs")
		}
		return nil
	}
	if err := WaitUnmount(context.Background(), "/Volumes/x"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitUnmountHonorsContext(t *testing.T) {
	orig, origPoll := statfsFn, pollInterval
	defer func() { statfsFn, pollInterval = orig, origPoll }()
	pollInterval = 5 * time.Millisecond
	statfsFn = fakeStatfs("nfs", false)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := WaitUnmount(ctx, "/Volumes/x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
