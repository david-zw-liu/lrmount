package inspect

import (
	"strings"
	"testing"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func TestTreeLines(t *testing.T) {
	m := afcfs.NewMemFS()
	m.AddFile("Documents/123/settings-acr/userStyles/a.xmp", 5)

	lines, err := TreeLines(m, "Documents", 10)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"123", "settings-acr", "userStyles", "a.xmp"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tree missing %q:\n%s", want, joined)
		}
	}
}

func TestSampleCandidatesSkipsIndexDatAndPrefersSubfolderFiles(t *testing.T) {
	m := afcfs.NewMemFS()
	us := "Documents/123/settings-acr/userStyles"
	m.AddFile(us+"/Index.dat", 500)            // top-level binary index — must be skipped
	m.AddFile(us+"/GroupA/p1.xmp", 10)         // real preset inside a subfolder
	m.AddFile(us+"/loose.xmp", 7)              // a top-level loose preset file

	got := sampleCandidates(m, us, 1)
	if len(got) != 1 {
		t.Fatalf("want 1 candidate, got %v", got)
	}
	if got[0] != us+"/GroupA/p1.xmp" {
		t.Fatalf("want subfolder preset first, got %q", got[0])
	}

	// Even when asked for more than exist, Index.dat is never a candidate.
	all := sampleCandidates(m, us, 10)
	for _, c := range all {
		if strings.HasSuffix(c, "/Index.dat") {
			t.Fatalf("Index.dat must never be sampled, got %v", all)
		}
	}
}
