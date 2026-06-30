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
	ReplaceDir  string // device dir to RemoveAll before push ("" for single file)
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
	base := filepath.Base(strings.TrimRight(source, string(os.PathSeparator)))
	replaceDir := deviceJoin(userStylesDir, base)
	plan := Plan{SourceIsDir: true, ReplaceDir: replaceDir}
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
		plan.Ops = append(plan.Ops, FileOp{Local: p, Device: deviceJoin(replaceDir, rel)})
		return nil
	})
	if err != nil {
		return Plan{}, err
	}
	return plan, nil
}
