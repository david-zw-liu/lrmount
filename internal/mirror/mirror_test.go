package mirror

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func TestSafeRelRejectsEscapes(t *testing.T) {
	bad := []string{"", ".", "..", "../x", "/abs", "a/../../b"}
	for _, r := range bad {
		if _, err := safeRel(r); err == nil {
			t.Errorf("safeRel(%q): expected error", r)
		}
	}
	good := map[string]string{"A/foo.xmp": "A/foo.xmp", "a/b/c": "a/b/c", "foo.xmp": "foo.xmp"}
	for in, want := range good {
		got, err := safeRel(in)
		if err != nil || got != want {
			t.Errorf("safeRel(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
}

func TestReconcilePushesNewFile(t *testing.T) {
	fs := afcfs.NewMemFS()
	local := t.TempDir()
	writeLocal(t, local, "A/foo.xmp", "hi")
	root := "Documents/cat/settings-acr/userStyles"

	if err := Reconcile(fs, local, root, []string{"A/foo.xmp"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !fs.Has(root + "/A/foo.xmp") {
		t.Error("expected pushed file on device")
	}
}

func TestReconcilePushesNewDirRecursively(t *testing.T) {
	fs := afcfs.NewMemFS()
	local := t.TempDir()
	writeLocal(t, local, "Grp/one.xmp", "a")
	writeLocal(t, local, "Grp/sub/two.xmp", "b")
	root := "Documents/cat/settings-acr/userStyles"

	if err := Reconcile(fs, local, root, []string{"Grp"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root + "/Grp/one.xmp", root + "/Grp/sub/two.xmp"} {
		if !fs.Has(p) {
			t.Errorf("expected %s pushed", p)
		}
	}
}

func TestReconcileDeletesMissingLocalPath(t *testing.T) {
	fs := afcfs.NewMemFS()
	root := "Documents/cat/settings-acr/userStyles"
	fs.AddFile(root+"/Old/gone.xmp", 5)
	local := t.TempDir() // Old/ does not exist locally

	if err := Reconcile(fs, local, root, []string{"Old"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if fs.Has(root+"/Old") || fs.Has(root+"/Old/gone.xmp") {
		t.Error("expected device path removed")
	}
}

func TestReconcileSkipsEscapePath(t *testing.T) {
	fs := afcfs.NewMemFS()
	root := "Documents/cat/settings-acr/userStyles"
	fs.AddFile(root+"/keep.xmp", 1)
	local := t.TempDir()

	var logged []string
	if err := Reconcile(fs, local, root, []string{"../evil"}, func(s string) { logged = append(logged, s) }); err != nil {
		t.Fatal(err)
	}
	if !fs.Has(root + "/keep.xmp") {
		t.Error("device content must be untouched by an escape path")
	}
	if len(logged) == 0 {
		t.Error("expected a log line for the refused path")
	}
}

// writeLocal creates local/rel with the given content, making parent dirs.
func writeLocal(t *testing.T, local, rel, content string) {
	t.Helper()
	p := filepath.Join(local, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPullReplaceMirrorsTreeAndCreatesLocalDirs(t *testing.T) {
	fs := afcfs.NewMemFS()
	// device userStyles tree
	fs.AddFile("Documents/cat/settings-acr/userStyles/A/foo.xmp", 10)
	fs.AddFile("Documents/cat/settings-acr/userStyles/B/bar.xmp", 20)
	fs.AddFile("Documents/cat/settings-acr/userStyles/Index.dat", 30)

	local := filepath.Join(t.TempDir(), "sync", "com.adobe.lrmobile", "userStyles")
	root := "Documents/cat/settings-acr/userStyles"

	var logged []string
	if err := PullReplace(fs, root, local, func(s string) { logged = append(logged, s) }); err != nil {
		t.Fatal(err)
	}

	// local subdirs created
	for _, d := range []string{"A", "B"} {
		if fi, err := os.Stat(filepath.Join(local, d)); err != nil || !fi.IsDir() {
			t.Errorf("expected local dir %s", d)
		}
	}
	// every device file was pulled to its mirrored local path
	wants := map[string]string{
		root + "/A/foo.xmp": filepath.Join(local, "A", "foo.xmp"),
		root + "/B/bar.xmp": filepath.Join(local, "B", "bar.xmp"),
		root + "/Index.dat": filepath.Join(local, "Index.dat"),
	}
	for src, dst := range wants {
		if got := fs.Pulled[src]; got != dst {
			t.Errorf("Pulled[%q] = %q, want %q", src, got, dst)
		}
	}
	if len(logged) != 3 {
		t.Errorf("expected 3 per-file log lines, got %d: %v", len(logged), logged)
	}
}
