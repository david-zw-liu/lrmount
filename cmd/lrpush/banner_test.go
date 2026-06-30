package main

import (
	"strings"
	"testing"
)

func TestWarningBannerMentionsCloseAndCloud(t *testing.T) {
	b := warningBanner()
	for _, want := range []string{"close", "Lightroom", "Creative Cloud"} {
		if !strings.Contains(b, want) {
			t.Fatalf("banner missing %q:\n%s", want, b)
		}
	}
}
