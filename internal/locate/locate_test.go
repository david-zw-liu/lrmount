package locate

import (
	"fmt"
	"testing"

	"github.com/davidliu/lrpush/internal/afcfs"
)

// rootUnlistableFS wraps a MemFS but fails List("") the way a house_arrest
// VendDocuments transport does (afc error 10), while named children list fine.
type rootUnlistableFS struct{ *afcfs.MemFS }

func (f rootUnlistableFS) List(p string) ([]string, error) {
	if p == "" || p == "/" || p == "." {
		return nil, fmt.Errorf("afc error code: 10")
	}
	return f.MemFS.List(p)
}

func TestDocumentsRootWhenRootUnlistable(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddDir("Documents/123/settings-acr")
	fs := rootUnlistableFS{m}
	got, err := DocumentsRoot(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Documents" {
		t.Fatalf("DocumentsRoot = %q, want Documents (probe should not rely on List(\"\"))", got)
	}
}

func TestDocumentsRootContainer(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddDir("Documents/123/settings-acr")
	got, err := DocumentsRoot(m, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Documents" {
		t.Fatalf("DocumentsRoot = %q, want Documents", got)
	}
}

func TestDocumentsRootIsDocuments(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddDir("123/settings-acr") // root already is Documents
	got, err := DocumentsRoot(m, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("DocumentsRoot = %q, want empty", got)
	}
}

func TestFindCatalogs(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddDir("Documents/aaa/settings-acr")
	m.AddFile("Documents/aaa/settings-acr/userStyles/p1.xmp", 1)
	m.AddFile("Documents/aaa/settings-acr/userStyles/p2.xmp", 1)
	m.AddDir("Documents/bbb/settings-acr/userStyles")
	m.AddDir("Documents/ccc/other") // not a catalog

	cands, err := FindCatalogs(m, "Documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d catalogs, want 2: %+v", len(cands), cands)
	}
	byName := map[string]Catalog{}
	for _, c := range cands {
		byName[c.Name] = c
	}
	if byName["aaa"].PresetCount != 2 {
		t.Fatalf("aaa preset count = %d, want 2", byName["aaa"].PresetCount)
	}
	if byName["aaa"].UserStyles != "Documents/aaa/settings-acr/userStyles" {
		t.Fatalf("aaa userStyles = %q", byName["aaa"].UserStyles)
	}
}
