package main

import (
	"strings"
	"testing"
)

func TestVolumeName(t *testing.T) {
	if got := volumeName("David's iPhone", "com.adobe.lrmobilephone", false); got != "David's iPhone Lightroom" {
		t.Fatalf("got %q", got)
	}
	if got := volumeName("iPad", "com.adobe.lrmobile", true); got != "iPad Lightroom lrmobile" {
		t.Fatalf("got %q", got)
	}
	// path-hostile characters are replaced
	if got := volumeName("we/ird:name", "b", false); strings.ContainsAny(got, "/:") {
		t.Fatalf("got %q", got)
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
