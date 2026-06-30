// Package pushsync plans and executes pushing local presets into userStyles.
package pushsync

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FileOp is one local file to push to one exact device path.
type FileOp struct {
	Local  string
	Device string
}

// Plan is the full set of operations for a push.
type Plan struct {
	SourceIsDir bool
	ReplaceDirs []string // device dirs to RemoveAll before push (one per top-level source subdir; nil for single file)
	Ops         []FileOp
}

// deviceJoin joins device path parts with "/" (device paths never use "\").
func deviceJoin(parts ...string) string {
	var nz []string
	for _, p := range parts {
		if p != "" {
			nz = append(nz, strings.Trim(p, "/"))
		}
	}
	return strings.Join(nz, "/")
}

// PlanPush builds the push plan. See package interface notes for semantics.
func PlanPush(source, userStylesDir string) (Plan, error) {
	info, err := os.Stat(source)
	if err != nil {
		return Plan{}, fmt.Errorf("source %q: %w", source, err)
	}
	if !info.IsDir() {
		dev := deviceJoin(userStylesDir, filepath.Base(source))
		return Plan{Ops: []FileOp{{Local: source, Device: dev}}}, nil
	}

	plan := Plan{SourceIsDir: true}

	// Determine which top-level entries are subdirs — these become replace groups.
	topEntries, err := os.ReadDir(source)
	if err != nil {
		return Plan{}, fmt.Errorf("reading source dir %q: %w", source, err)
	}
	for _, entry := range topEntries {
		if entry.IsDir() {
			plan.ReplaceDirs = append(plan.ReplaceDirs, deviceJoin(userStylesDir, entry.Name()))
		}
	}

	// Walk all files and mirror them into userStyles (no basename wrapper).
	err = filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		plan.Ops = append(plan.Ops, FileOp{Local: p, Device: deviceJoin(userStylesDir, rel)})
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}
