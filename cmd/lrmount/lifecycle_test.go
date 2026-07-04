package main

import "testing"

// deviceMount and activeMounts drive the daemon's exit decision: quit only
// when a Finder eject leaves no device still mounted.
func TestActiveMountsCountsUnejected(t *testing.T) {
	mounts := map[string]*deviceMount{
		"a": {ejected: false},
		"b": {ejected: false},
		"c": {ejected: true},
	}
	if got := activeMounts(mounts); got != 2 {
		t.Fatalf("activeMounts = %d, want 2", got)
	}
}

func TestExitOnlyWhenLastEjected(t *testing.T) {
	// Two devices mounted; eject one → one still active → do not exit.
	mounts := map[string]*deviceMount{"a": {ejected: false}, "b": {ejected: false}}
	mounts["a"].ejected = true // simulate Finder eject of a
	if activeMounts(mounts) == 0 {
		t.Fatal("should stay running while b is still mounted")
	}
	// Eject the second → none active → exit.
	mounts["b"].ejected = true
	if activeMounts(mounts) != 0 {
		t.Fatal("should exit once the last device is ejected")
	}
}
