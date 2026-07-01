// Package mirror mirrors a device Lightroom userStyles folder to a local
// directory and syncs local edits back to the device.
package mirror

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/davidliu/lrpush/internal/afcfs"
)

// deviceJoinRel joins a device root with a slash-separated relative path.
// An empty rel returns the root unchanged.
func deviceJoinRel(root, rel string) string {
	if rel == "" {
		return root
	}
	return root + "/" + rel
}

// safeRel validates that rel is a clean, relative, non-empty slash-separated
// path with no ".." components. It is used by Task 2 (push sync).
func safeRel(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty relative path")
	}
	clean := path.Clean(rel)
	if path.IsAbs(clean) {
		return "", fmt.Errorf("absolute path not allowed: %s", rel)
	}
	if clean == "." || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("unsafe relative path: %s", rel)
	}
	return clean, nil
}

// PullReplace recreates localDir and recursively pulls the device userStyles
// tree into it, logging one line per file. The device is never written. The
// caller is responsible for wiping ./sync beforehand.
func PullReplace(fs afcfs.FS, deviceUserStyles, localDir string, log func(string)) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	return pullTree(fs, deviceUserStyles, localDir, "", log)
}

// deviceMkdirAll makes a device directory; afcfs.MkDir is already mkdir-p.
func deviceMkdirAll(fs afcfs.FS, dir string) error { return fs.MkDir(dir) }

// Reconcile applies a set of changed local relative paths to the device:
// missing local path -> RemoveAll on device; file -> PushFile; dir -> push all
// files under it. Per-path errors and refused (escaping) paths are logged and
// skipped so the session survives; Reconcile returns nil.
func Reconcile(fs afcfs.FS, localDir, deviceUserStyles string, changed []string, log func(string)) error {
	for _, raw := range changed {
		rel, err := safeRel(raw)
		if err != nil {
			log("skip " + err.Error())
			continue
		}
		localPath := filepath.Join(localDir, filepath.FromSlash(rel))
		devPath := deviceJoinRel(deviceUserStyles, rel)

		fi, statErr := os.Stat(localPath)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := fs.RemoveAll(devPath); err != nil {
				log("delete failed " + rel + ": " + err.Error())
				continue
			}
			log("deleted " + rel)
			continue
		}
		if statErr != nil {
			log("stat failed " + rel + ": " + statErr.Error())
			continue
		}
		if fi.IsDir() {
			if err := pushDir(fs, localPath, devPath, log); err != nil {
				log("push dir failed " + rel + ": " + err.Error())
			}
			continue
		}
		if err := deviceMkdirAll(fs, path.Dir(devPath)); err != nil {
			log("mkdir failed " + rel + ": " + err.Error())
			continue
		}
		if err := fs.PushFile(localPath, devPath); err != nil {
			log("push failed " + rel + ": " + err.Error())
			continue
		}
		log("pushed " + rel)
	}
	return nil
}

// pushDir mkdir-p's deviceDir and pushes every file under localDir into it.
func pushDir(fs afcfs.FS, localDir, deviceDir string, log func(string)) error {
	if err := deviceMkdirAll(fs, deviceDir); err != nil {
		return err
	}
	return filepath.WalkDir(localDir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		dev := deviceDir
		if relSlash != "." {
			dev = deviceDir + "/" + relSlash
		}
		if d.IsDir() {
			return deviceMkdirAll(fs, dev)
		}
		if err := fs.PushFile(p, dev); err != nil {
			return err
		}
		log("pushed " + relSlash)
		return nil
	})
}

func pullTree(fs afcfs.FS, deviceRoot, localRoot, rel string, log func(string)) error {
	entries, err := fs.List(deviceJoinRel(deviceRoot, rel))
	if err != nil {
		return err
	}
	for _, name := range entries {
		childRel := path.Join(rel, name)
		devPath := deviceJoinRel(deviceRoot, childRel)
		fi, err := fs.Stat(devPath)
		if err != nil {
			return err
		}
		localPath := filepath.Join(localRoot, filepath.FromSlash(childRel))
		if fi.IsDir {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				return err
			}
			if err := pullTree(fs, deviceRoot, localRoot, childRel, log); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return err
		}
		if err := fs.Pull(devPath, localPath); err != nil {
			return err
		}
		log("pulled " + childRel)
	}
	return nil
}
