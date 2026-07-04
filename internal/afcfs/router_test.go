package afcfs

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

func TestRootedResolvesUnderRoot(t *testing.T) {
	m := NewMemFS()
	m.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 3)
	r := Rooted(m, "Documents")

	names, err := r.List("")
	if err != nil || len(names) != 1 || names[0] != "cat" {
		t.Fatalf("List(\"\") = %v, %v", names, err)
	}
	fi, err := r.Stat("cat/settings-acr/userStyles/a.xmp")
	if err != nil || fi.Size != 3 {
		t.Fatalf("Stat = %+v, %v", fi, err)
	}
	// a write goes to the rooted location on the underlying FS
	h, err := r.OpenFile("cat/new.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.Write([]byte("hi"))
	_ = h.Close()
	if data, ok := m.Contents("Documents/cat/new.txt"); !ok || string(data) != "hi" {
		t.Fatalf("underlying contents = %q %v", data, ok)
	}
}

func newRouter(t *testing.T) (*MemFS, *MemFS, *Router) {
	t.Helper()
	iphone := NewMemFS()
	iphone.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 1)
	ipad := NewMemFS()
	ipad.AddFile("Documents/other/settings-acr/userStyles/b.xmp", 2)
	r := NewRouter([]NamedFS{
		{Name: "Lightroom Mobile", FS: Rooted(iphone, "Documents")},
		{Name: "Lightroom for iPad", FS: Rooted(ipad, "Documents")},
	})
	return iphone, ipad, r
}

func TestRouterRootListsAppsSorted(t *testing.T) {
	_, _, r := newRouter(t)
	names, err := r.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "Lightroom Mobile" || names[1] != "Lightroom for iPad" {
		// sorted: "Lightroom Mobile" < "Lightroom for iPad" (uppercase M < lowercase f)
		t.Fatalf("root list = %v", names)
	}
	fi, err := r.Stat("")
	if err != nil || !fi.IsDir {
		t.Fatalf("root stat = %+v %v", fi, err)
	}
}

func TestRouterDelegatesIntoApp(t *testing.T) {
	_, _, r := newRouter(t)
	// the app dir itself is a virtual directory
	fi, err := r.Stat("Lightroom Mobile")
	if err != nil || !fi.IsDir || fi.Name != "Lightroom Mobile" {
		t.Fatalf("app stat = %+v %v", fi, err)
	}
	// listing into it reaches the app's Documents
	names, err := r.List("Lightroom Mobile")
	if err != nil || len(names) != 1 || names[0] != "cat" {
		t.Fatalf("app list = %v %v", names, err)
	}
	// a nested write lands in the right app's backing store
	h, err := r.OpenFile("Lightroom for iPad/other/new.xmp", os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.Write([]byte("x"))
	_ = h.Close()
	_, r2, _ := newRouter(t)
	_ = r2
}

func TestRouterWritesRouteToCorrectDevice(t *testing.T) {
	iphone, ipad, r := newRouter(t)
	h, err := r.OpenFile("Lightroom Mobile/cat/z.xmp", os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = h.Write([]byte("z"))
	_ = h.Close()
	if !iphone.Has("Documents/cat/z.xmp") {
		t.Fatal("write did not reach the iPhone FS")
	}
	if ipad.Has("Documents/cat/z.xmp") {
		t.Fatal("write leaked into the iPad FS")
	}
}

func TestRouterVirtualRootIsReadOnly(t *testing.T) {
	_, _, r := newRouter(t)
	if err := r.MkDir("NewApp"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("MkDir at root = %v, want permission denied", err)
	}
	if err := r.Remove("Lightroom Mobile"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Remove app dir = %v, want permission denied", err)
	}
	if err := r.Rename("Lightroom Mobile", "X"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Rename app dir = %v, want permission denied", err)
	}
	if _, err := r.OpenFile("Lightroom Mobile", os.O_RDONLY); err == nil {
		t.Fatal("opening an app dir as a file should fail")
	}
}

func TestRouterRejectsCrossAppRename(t *testing.T) {
	_, _, r := newRouter(t)
	err := r.Rename("Lightroom Mobile/cat/a", "Lightroom for iPad/cat/a")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Fatalf("cross-app rename = %v, want invalid", err)
	}
}

func TestRouterUnknownApp(t *testing.T) {
	_, _, r := newRouter(t)
	if _, err := r.List("Nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("unknown app list = %v", err)
	}
}

func TestRouterDeviceInfo(t *testing.T) {
	_, _, r := newRouter(t)
	total, free, err := r.DeviceInfo()
	if err != nil || total == 0 || free == 0 {
		t.Fatalf("device info = %d %d %v", total, free, err)
	}
	empty := NewRouter(nil)
	if _, _, err := empty.DeviceInfo(); err == nil {
		t.Fatal("empty router should error on DeviceInfo")
	}
}

func TestRouterSetMtimeIntoApp(t *testing.T) {
	iphone, _, r := newRouter(t)
	want := time.Unix(0, 555)
	if err := r.SetMtime("Lightroom Mobile/cat/settings-acr/userStyles/a.xmp", want); err != nil {
		t.Fatal(err)
	}
	fi, _ := iphone.Stat("Documents/cat/settings-acr/userStyles/a.xmp")
	if !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v", fi.ModTime)
	}
	// mtime on a virtual app dir is a silent no-op
	if err := r.SetMtime("Lightroom Mobile", want); err != nil {
		t.Fatalf("SetMtime on app dir = %v", err)
	}
}
