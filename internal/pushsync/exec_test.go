package pushsync

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func TestExecuteDryRunDoesNotMutate(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddDir("U/my-presets") // existing target dir
	dir := t.TempDir()
	local := filepath.Join(dir, "my-presets", "a.xmp")
	os.MkdirAll(filepath.Dir(local), 0o755)
	os.WriteFile(local, []byte("x"), 0o644)

	plan, _ := PlanPush(filepath.Join(dir, "my-presets"), "U")
	var buf bytes.Buffer
	err := Execute(m, plan, ExecOptions{UserStylesDir: "U", BackupDir: "/tmp/bk", Commit: false, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Pushed) != 0 {
		t.Fatalf("dry-run pushed files: %v", m.Pushed)
	}
	if len(m.Pulled) != 0 {
		t.Fatalf("dry-run backed up: %v", m.Pulled)
	}
	if !m.Has("U/my-presets") {
		t.Fatal("dry-run must not RemoveAll existing dir")
	}
}

func TestExecuteCommitBacksUpReplacesAndPushes(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddFile("U/my-presets/old.xmp", 1) // stale file that must NOT survive
	m.AddFile("U/keep.xmp", 1)           // unrelated existing file, must survive

	dir := t.TempDir()
	srcDir := filepath.Join(dir, "my-presets")
	os.MkdirAll(srcDir, 0o755)
	os.WriteFile(filepath.Join(srcDir, "a.xmp"), []byte("x"), 0o644)

	plan, _ := PlanPush(srcDir, "U")
	var buf bytes.Buffer
	err := Execute(m, plan, ExecOptions{UserStylesDir: "U", BackupDir: "/tmp/bk", Commit: true, Out: &buf})
	if err != nil {
		t.Fatal(err)
	}
	if m.Pulled["U"] != "/tmp/bk" {
		t.Fatalf("expected backup Pull of U, got %v", m.Pulled)
	}
	if m.Has("U/my-presets/old.xmp") {
		t.Fatal("stale old.xmp should be gone after replace")
	}
	if !m.Has("U/my-presets/a.xmp") {
		t.Fatal("a.xmp should have been pushed")
	}
	if !m.Has("U/keep.xmp") {
		t.Fatal("unrelated keep.xmp must survive")
	}
}
