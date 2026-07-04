//go:build darwin

package mountctl

import (
	"errors"
	"testing"

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
