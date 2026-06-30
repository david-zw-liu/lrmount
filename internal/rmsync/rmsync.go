// Package rmsync plans and executes deletes from the device userStyles.
package rmsync

import (
	"fmt"
	"io"
	"strings"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func deviceJoin(parts ...string) string {
	var nz []string
	for _, p := range parts {
		if p != "" {
			nz = append(nz, strings.Trim(p, "/"))
		}
	}
	return strings.Join(nz, "/")
}

// Target is one deletion target resolved against userStyles.
type Target struct {
	Rel    string
	Device string
	Exists bool
	IsDir  bool
}

// PlanRm resolves relative paths under userStylesDir and stats each.
func PlanRm(fs afcfs.FS, userStylesDir string, rels []string) []Target {
	var out []Target
	for _, rel := range rels {
		dev := deviceJoin(userStylesDir, rel)
		t := Target{Rel: rel, Device: dev}
		if fi, err := fs.Stat(dev); err == nil {
			t.Exists = true
			t.IsDir = fi.IsDir
		}
		out = append(out, t)
	}
	return out
}

// ExecOptions configures rm execution.
type ExecOptions struct {
	BackupDir string
	Commit    bool
	Out       io.Writer
}

// Execute runs (or describes) the deletes.
func Execute(fs afcfs.FS, targets []Target, opts ExecOptions) error {
	w := opts.Out
	if !opts.Commit {
		fmt.Fprintln(w, "DRY RUN (no changes will be made). Pass --commit to apply.")
		for _, t := range targets {
			if !t.Exists {
				fmt.Fprintf(w, "skip (not found): %s\n", t.Device)
				continue
			}
			kind := "file"
			if t.IsDir {
				kind = "dir"
			}
			fmt.Fprintf(w, "would back up + delete %s: %s\n", kind, t.Device)
		}
		return nil
	}

	var failures int
	for _, t := range targets {
		if !t.Exists {
			fmt.Fprintf(w, "skip (not found): %s\n", t.Device)
			continue
		}
		backupPath := deviceJoin(opts.BackupDir, t.Rel)
		if err := fs.Pull(t.Device, backupPath); err != nil {
			fmt.Fprintf(w, "FAIL backup %s: %v\n", t.Device, err)
			failures++
			continue
		}
		if err := fs.RemoveAll(t.Device); err != nil {
			fmt.Fprintf(w, "FAIL delete %s: %v\n", t.Device, err)
			failures++
			continue
		}
		fmt.Fprintf(w, "DELETED %s (backup: %s)\n", t.Device, backupPath)
	}
	if failures > 0 {
		return fmt.Errorf("%d target(s) failed", failures)
	}
	return nil
}
