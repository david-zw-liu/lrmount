package pushsync

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlanPushSingleFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.xmp")
	writeFile(t, src)

	plan, err := PlanPush(src, "Documents/123/userStyles")
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceIsDir || plan.ReplaceDir != "" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Ops) != 1 || plan.Ops[0].Device != "Documents/123/userStyles/foo.xmp" {
		t.Fatalf("ops = %+v", plan.Ops)
	}
}

func TestPlanPushDirPreservesStructureAndBasename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "my-presets")
	writeFile(t, filepath.Join(src, "a.xmp"))
	writeFile(t, filepath.Join(src, "sub", "b.xmp"))

	plan, err := PlanPush(src, "Documents/123/userStyles")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SourceIsDir {
		t.Fatal("expected SourceIsDir")
	}
	if plan.ReplaceDir != "Documents/123/userStyles/my-presets" {
		t.Fatalf("ReplaceDir = %q", plan.ReplaceDir)
	}
	got := map[string]bool{}
	for _, op := range plan.Ops {
		got[op.Device] = true
	}
	want := []string{
		"Documents/123/userStyles/my-presets/a.xmp",
		"Documents/123/userStyles/my-presets/sub/b.xmp",
	}
	if len(plan.Ops) != 2 {
		t.Fatalf("ops = %+v", plan.Ops)
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("missing op %q in %+v", w, plan.Ops)
		}
	}
}

func TestPlanPushPushesAllFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p")
	writeFile(t, filepath.Join(src, "a.xmp"))
	writeFile(t, filepath.Join(src, "b.txt"))
	plan, err := PlanPush(src, "U")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ops) != 2 {
		t.Fatalf("want all 2 files pushed, got %+v", plan.Ops)
	}
}

func TestPlanPushMissingSource(t *testing.T) {
	if _, err := PlanPush("/nope/nope", "U"); err == nil {
		t.Fatal("expected error for missing source")
	}
}
