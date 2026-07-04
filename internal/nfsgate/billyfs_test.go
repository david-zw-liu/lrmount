package nfsgate

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

func newFS(t *testing.T) (*afcfs.MemFS, *BillyFS) {
	t.Helper()
	m := afcfs.NewMemFS()
	m.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 4)
	return m, NewBillyFS(m, "Documents")
}

func TestResolveRootsAndGuards(t *testing.T) {
	m, b := newFS(t)
	// paths are relative to the Documents root
	fi, err := b.Stat("/cat/settings-acr/userStyles/a.xmp")
	if err != nil || fi.Size() != 4 || fi.IsDir() {
		t.Fatalf("fi = %+v err = %v", fi, err)
	}
	// ".." cannot escape the root
	if _, err := b.Stat("../secret"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist for escaped path, got %v", err)
	}
	_ = m
}

func TestStatRoot(t *testing.T) {
	_, b := newFS(t)
	fi, err := b.Stat("/")
	if err != nil || !fi.IsDir() {
		t.Fatalf("root stat = %+v %v", fi, err)
	}
}

func TestCreateWriteReadAt(t *testing.T) {
	m, b := newFS(t)
	f, err := b.Create("/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if n, err := f.ReadAt(buf, 6); err != nil && err != io.EOF {
		t.Fatal(err)
	} else if n != 5 || string(buf) != "world" {
		t.Fatalf("ReadAt = %d %q", n, buf)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if data, ok := m.Contents("Documents/new.txt"); !ok || string(data) != "hello world" {
		t.Fatalf("device contents = %q %v", data, ok)
	}
}

func TestOpenFileWriteOnlyPreservesContent(t *testing.T) {
	m, b := newFS(t)
	// go-nfs WRITE opens O_RDWR; SETATTR-size opens O_WRONLY|O_EXCL. Neither
	// may truncate.
	f, err := b.OpenFile("/cat/settings-acr/userStyles/a.xmp", os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if data, _ := m.Contents("Documents/cat/settings-acr/userStyles/a.xmp"); len(data) != 4 {
		t.Fatalf("content truncated to %d bytes", len(data))
	}
}

func TestReadDirSortedWithInfo(t *testing.T) {
	m, b := newFS(t)
	m.AddFile("Documents/b.txt", 1)
	m.AddFile("Documents/a.txt", 2)
	infos, err := b.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 || infos[0].Name() != "a.txt" || infos[1].Name() != "b.txt" || !infos[2].IsDir() {
		names := []string{}
		for _, i := range infos {
			names = append(names, i.Name())
		}
		t.Fatalf("infos = %v", names)
	}
}

func TestMkdirAllRenameRemove(t *testing.T) {
	m, b := newFS(t)
	if err := b.MkdirAll("/x/y/z", 0o755); err != nil {
		t.Fatal(err)
	}
	if !m.Has("Documents/x/y/z") {
		t.Fatal("mkdirall did not create tree")
	}
	if err := b.Rename("/cat", "/cat2"); err != nil {
		t.Fatal(err)
	}
	if !m.Has("Documents/cat2/settings-acr/userStyles/a.xmp") {
		t.Fatal("rename lost subtree")
	}
	if err := b.Remove("/cat2"); err == nil {
		t.Fatal("want error removing non-empty dir")
	}
	if err := b.Remove("/x/y/z"); err != nil {
		t.Fatal(err)
	}
}

func TestChangeChtimes(t *testing.T) {
	m, b := newFS(t)
	want := time.Unix(0, 999)
	if err := b.Chtimes("/cat/settings-acr/userStyles/a.xmp", time.Now(), want); err != nil {
		t.Fatal(err)
	}
	fi, _ := m.Stat("Documents/cat/settings-acr/userStyles/a.xmp")
	if !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v", fi.ModTime)
	}
	// Chmod/Chown are silent no-ops: AFC has no permission model, and
	// failing them would break Finder copies.
	if err := b.Chmod("/anything", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Chown("/anything", 1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedSurface(t *testing.T) {
	_, b := newFS(t)
	if err := b.Symlink("a", "b"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("Symlink err = %v", err)
	}
	if _, err := b.Readlink("a"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("Readlink err = %v", err)
	}
	if _, err := b.TempFile("", "x"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("TempFile err = %v", err)
	}
}

func TestCapabilitiesExcludeLock(t *testing.T) {
	_, b := newFS(t)
	caps := b.Capabilities()
	if caps&billy.LockCapability != 0 {
		t.Fatal("must not advertise lock capability")
	}
	for _, c := range []billy.Capability{billy.WriteCapability, billy.ReadCapability, billy.SeekCapability, billy.TruncateCapability} {
		if caps&c == 0 {
			t.Fatalf("missing capability %b", c)
		}
	}
}
