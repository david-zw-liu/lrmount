//go:build darwin

package mountctl

import (
	"errors"
	"testing"
)

func TestIsMounted(t *testing.T) {
	orig := listMountsFn
	defer func() { listMountsFn = orig }()

	table := []mountEntry{
		{dir: "/Volumes/x", fstype: "nfs"},
		{dir: "/", fstype: "apfs"},
	}
	listMountsFn = func() ([]mountEntry, error) { return table, nil }

	if !IsMounted("/Volumes/x") {
		t.Fatal("want mounted for nfs entry in the table")
	}
	if IsMounted("/") {
		t.Fatal("want not-mounted for a non-nfs entry")
	}
	if IsMounted("/Volumes/absent") {
		t.Fatal("want not-mounted for an entry missing from the table")
	}

	// A not-responding server still leaves the entry in the table, so it must
	// read as mounted (this is the regression the mount-table check fixes).
	listMountsFn = func() ([]mountEntry, error) {
		return []mountEntry{{dir: "/Volumes/x", fstype: "nfs"}}, nil
	}
	if !IsMounted("/Volumes/x") {
		t.Fatal("want mounted while server is not responding but still registered")
	}

	// A failure enumerating the table is treated as not-mounted.
	listMountsFn = func() ([]mountEntry, error) { return nil, errors.New("boom") }
	if IsMounted("/Volumes/x") {
		t.Fatal("want not-mounted when the mount table cannot be read")
	}
}
