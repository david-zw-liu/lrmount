package mirror

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/davidliu/lrpush/internal/afcfs"
)

const debounce = 400 * time.Millisecond

// Watcher mirrors local edits under localDir up to the device userStyles.
type Watcher struct {
	fs               afcfs.FS
	localDir         string
	deviceUserStyles string
	log              func(string)
	w                *fsnotify.Watcher
}

func NewWatcher(fs afcfs.FS, localDir, deviceUserStyles string, log func(string)) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{fs: fs, localDir: localDir, deviceUserStyles: deviceUserStyles, log: log, w: w}, nil
}

// relFromEventPath converts an absolute event path to a slash relative path
// under localDir. It returns false for the root itself or any path outside it.
func relFromEventPath(localDir, evPath string) (string, bool) {
	rel, err := filepath.Rel(localDir, evPath)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// subdirs returns root and every directory beneath it.
func subdirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs, err
}

// statDir reports whether p is an existing directory.
func statDir(p string) (bool, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}

// Run watches localDir recursively and pushes debounced batches of changes to
// the device via Reconcile until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	dirs, err := subdirs(w.localDir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if err := w.w.Add(d); err != nil {
			return err
		}
	}

	pending := map[string]struct{}{}
	var timerC <-chan time.Time

	flush := func() {
		if len(pending) == 0 {
			return
		}
		changed := make([]string, 0, len(pending))
		for r := range pending {
			changed = append(changed, r)
		}
		pending = map[string]struct{}{}
		if err := Reconcile(w.fs, w.localDir, w.deviceUserStyles, changed, w.log); err != nil {
			w.log("reconcile error: " + err.Error())
		}
	}

	for {
		select {
		case <-ctx.Done():
			return w.w.Close()
		case ev, ok := <-w.w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := statDir(ev.Name); err == nil && fi {
					_ = w.w.Add(ev.Name) // watch newly-created subdir
				}
			}
			if rel, ok := relFromEventPath(w.localDir, ev.Name); ok {
				pending[rel] = struct{}{}
				timerC = time.After(debounce)
			}
		case <-timerC:
			timerC = nil
			flush()
		case err, ok := <-w.w.Errors:
			if !ok {
				return nil
			}
			w.log("watch error: " + err.Error())
		}
	}
}
