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
	if plan.SourceIsDir || len(plan.ReplaceDirs) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if len(plan.Ops) != 1 || plan.Ops[0].Device != "Documents/123/userStyles/foo.xmp" {
		t.Fatalf("ops = %+v", plan.Ops)
	}
}

func TestPlanPushDirMirrorsContentsIntoUserStyles(t *testing.T) {
	const US = "Documents/123/userStyles"
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	writeFile(t, filepath.Join(src, "A", "a.xmp"))
	writeFile(t, filepath.Join(src, "B", "sub", "b.xmp"))
	writeFile(t, filepath.Join(src, "xxx.xmp"))

	plan, err := PlanPush(src, US)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SourceIsDir {
		t.Fatal("expected SourceIsDir")
	}

	// ReplaceDirs must contain exactly the device dirs for top-level subdirs A and B.
	// xxx.xmp is a loose top-level file — no replace dir for it.
	replaceDirSet := map[string]bool{}
	for _, rd := range plan.ReplaceDirs {
		replaceDirSet[rd] = true
	}
	if !replaceDirSet[US+"/A"] {
		t.Fatalf("ReplaceDirs missing %q; got %v", US+"/A", plan.ReplaceDirs)
	}
	if !replaceDirSet[US+"/B"] {
		t.Fatalf("ReplaceDirs missing %q; got %v", US+"/B", plan.ReplaceDirs)
	}
	if len(plan.ReplaceDirs) != 2 {
		t.Fatalf("expected exactly 2 ReplaceDirs, got %v", plan.ReplaceDirs)
	}

	// Ops must mirror source contents directly into userStyles — no basename wrapper.
	got := map[string]bool{}
	for _, op := range plan.Ops {
		got[op.Device] = true
	}
	want := []string{
		US + "/A/a.xmp",
		US + "/B/sub/b.xmp",
		US + "/xxx.xmp",
	}
	if len(plan.Ops) != 3 {
		t.Fatalf("expected 3 ops, got %+v", plan.Ops)
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
