package mirror

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestRelFromEventPath(t *testing.T) {
	local := "/tmp/sync/app/userStyles"
	if rel, ok := relFromEventPath(local, filepath.Join(local, "A", "foo.xmp")); !ok || rel != "A/foo.xmp" {
		t.Errorf("got %q,%v want A/foo.xmp,true", rel, ok)
	}
	if _, ok := relFromEventPath(local, local); ok {
		t.Error("root itself should be rejected")
	}
	if _, ok := relFromEventPath(local, "/etc/passwd"); ok {
		t.Error("outside path should be rejected")
	}
}

func TestSubdirs(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "A", "sub"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "B"), 0o755))

	got, err := subdirs(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{root, filepath.Join(root, "A"), filepath.Join(root, "A", "sub"), filepath.Join(root, "B")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
