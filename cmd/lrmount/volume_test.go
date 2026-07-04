package main

import (
	"strings"
	"testing"
)

func TestAppLabel(t *testing.T) {
	cases := map[string]string{
		"com.adobe.lrmobilephone": "Lightroom Mobile",
		"com.adobe.lrmobile":      "Lightroom for iPad",
		"com.adobe.unknown":       "Lightroom",
	}
	for bundle, want := range cases {
		if got := appLabel(bundle); got != want {
			t.Fatalf("appLabel(%q) = %q, want %q", bundle, got, want)
		}
	}
}

func TestSanitizeSeg(t *testing.T) {
	if got := sanitizeSeg("we/ird:name"); strings.ContainsAny(got, "/:") {
		t.Fatalf("got %q, still contains a path-hostile character", got)
	}
	if got := sanitizeSeg("David's iPhone"); got != "David's iPhone" {
		t.Fatalf("got %q, apostrophes and spaces should be preserved", got)
	}
}

func TestHintPath(t *testing.T) {
	got := hintPath("/Volumes/iPad Lightroom", "Documents", "Documents/cat/settings-acr/userStyles")
	if got != "/Volumes/iPad Lightroom/cat/settings-acr/userStyles" {
		t.Fatalf("got %q", got)
	}
	// root "" means the AFC root already is Documents
	got = hintPath("/Volumes/x", "", "cat/settings-acr/userStyles")
	if got != "/Volumes/x/cat/settings-acr/userStyles" {
		t.Fatalf("got %q", got)
	}
}
