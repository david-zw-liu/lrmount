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
