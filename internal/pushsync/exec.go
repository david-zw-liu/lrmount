package pushsync

import (
	"fmt"
	"io"
	"strings"

	"github.com/davidliu/lrpush/internal/afcfs"
)

// ExecOptions configures a push execution.
type ExecOptions struct {
	UserStylesDir string
	BackupDir     string
	Commit        bool
	Out           io.Writer
}

// mkDirAll creates deviceDir and all its ancestors (idempotent).
func mkDirAll(fs afcfs.FS, deviceDir string) error {
	deviceDir = strings.Trim(deviceDir, "/")
	if deviceDir == "" {
		return nil
	}
	parts := strings.Split(deviceDir, "/")
	cur := ""
	for _, part := range parts {
		if cur == "" {
			cur = part
		} else {
			cur = cur + "/" + part
		}
		if err := fs.MkDir(cur); err != nil {
			return fmt.Errorf("mkdir %q: %w", cur, err)
		}
	}
	return nil
}

func deviceParent(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// Execute runs (or, in dry-run, describes) the push plan.
func Execute(fs afcfs.FS, plan Plan, opts ExecOptions) error {
	w := opts.Out
	if !opts.Commit {
		fmt.Fprintln(w, "DRY RUN (no changes will be made). Pass --commit to apply.")
		fmt.Fprintf(w, "backup of %s -> %s\n", opts.UserStylesDir, opts.BackupDir)
		for _, dir := range plan.ReplaceDirs {
			if fi, err := fs.Stat(dir); err == nil && fi.IsDir {
				fmt.Fprintf(w, "would REPLACE existing group: %s\n", dir)
			}
		}
		for _, op := range plan.Ops {
			fmt.Fprintf(w, "would push: %s -> %s\n", op.Local, op.Device)
		}
		fmt.Fprintf(w, "total files: %d\n", len(plan.Ops))
		return nil
	}

	fmt.Fprintf(w, "backing up %s -> %s ...\n", opts.UserStylesDir, opts.BackupDir)
	if err := fs.Pull(opts.UserStylesDir, opts.BackupDir); err != nil {
		return fmt.Errorf("backup failed (aborting, nothing pushed): %w", err)
	}

	for _, dir := range plan.ReplaceDirs {
		if fi, err := fs.Stat(dir); err == nil && fi.IsDir {
			fmt.Fprintf(w, "replacing existing group %s\n", dir)
			if err := fs.RemoveAll(dir); err != nil {
				return fmt.Errorf("remove existing %q: %w", dir, err)
			}
		}
	}

	var failures int
	for _, op := range plan.Ops {
		if err := mkDirAll(fs, deviceParent(op.Device)); err != nil {
			fmt.Fprintf(w, "FAIL %s: %v\n", op.Device, err)
			failures++
			continue
		}
		if err := fs.PushFile(op.Local, op.Device); err != nil {
			fmt.Fprintf(w, "FAIL %s: %v\n", op.Device, err)
			failures++
			continue
		}
		fmt.Fprintf(w, "OK   %s\n", op.Device)
	}
	fmt.Fprintf(w, "done: %d pushed, %d failed\n", len(plan.Ops)-failures, failures)
	if failures > 0 {
		return fmt.Errorf("%d file(s) failed to push", failures)
	}
	return nil
}
