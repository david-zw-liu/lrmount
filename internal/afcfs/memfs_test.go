package afcfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"
)

func TestMemFSListStat(t *testing.T) {
	m := NewMemFS()
	m.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 3)
	m.AddDir("Documents/empty")
	names, err := m.List("Documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "cat" || names[1] != "empty" {
		t.Fatalf("names = %v", names)
	}
	fi, err := m.Stat("Documents/cat/settings-acr/userStyles/a.xmp")
	if err != nil || fi.IsDir || fi.Size != 3 || fi.Name != "a.xmp" {
		t.Fatalf("fi = %+v err = %v", fi, err)
	}
	if _, err := m.Stat("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestMemFSOpenWriteReadBack(t *testing.T) {
	m := NewMemFS()
	m.AddDir("d")
	h, err := m.OpenFile("d/f.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("WORLD")); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, ok := m.Contents("d/f.txt")
	if !ok || string(data) != "hello WORLD" {
		t.Fatalf("contents = %q %v", data, ok)
	}
	// read-only open of a missing file fails
	if _, err := m.OpenFile("d/none", os.O_RDONLY); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestMemFSTruncateAndSeekEnd(t *testing.T) {
	m := NewMemFS()
	m.AddFile("f", 0)
	h, _ := m.OpenFile("f", os.O_RDWR)
	_, _ = h.Write([]byte("0123456789"))
	if err := h.Truncate(4); err != nil {
		t.Fatal(err)
	}
	if pos, err := h.Seek(0, io.SeekEnd); err != nil || pos != 4 {
		t.Fatalf("seek end = %d %v", pos, err)
	}
	_ = h.Close()
}

func TestMemFSRemoveSemantics(t *testing.T) {
	m := NewMemFS()
	m.AddFile("d/sub/f", 1)
	if err := m.Remove("d/sub"); err == nil {
		t.Fatal("want error removing non-empty dir")
	}
	if err := m.Remove("d/sub/f"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("d/sub"); err != nil {
		t.Fatal(err)
	}
	if m.Has("d/sub") {
		t.Fatal("dir still present")
	}
}

func TestMemFSRenameFileAndDir(t *testing.T) {
	m := NewMemFS()
	m.AddFile("a/f", 2)
	if err := m.Rename("a/f", "a/g"); err != nil {
		t.Fatal(err)
	}
	if m.Has("a/f") || !m.Has("a/g") {
		t.Fatal("file rename failed")
	}
	m.AddFile("dir/x/y", 1)
	if err := m.Rename("dir", "dir2"); err != nil {
		t.Fatal(err)
	}
	if !m.Has("dir2/x/y") || m.Has("dir/x/y") {
		t.Fatal("dir rename did not move subtree")
	}
}

func TestMemFSSetMtime(t *testing.T) {
	m := NewMemFS()
	m.AddFile("f", 0)
	want := time.Unix(0, 12345)
	if err := m.SetMtime("f", want); err != nil {
		t.Fatal(err)
	}
	fi, _ := m.Stat("f")
	if !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v", fi.ModTime)
	}
}

func TestMemFSDeviceInfo(t *testing.T) {
	m := NewMemFS()
	total, free, err := m.DeviceInfo()
	if err != nil || total == 0 || free == 0 {
		t.Fatalf("device info = %d %d %v", total, free, err)
	}
}
