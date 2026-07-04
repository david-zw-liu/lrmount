package afcfs

import (
	"path"
	"strings"
	"time"
)

// Rooted returns a view of fs whose paths are all relative to root, so a
// filesystem addressed from an app-container root can be presented as if root
// were its top. root uses "/" separators, no leading slash.
func Rooted(fs FS, root string) FS {
	return &rootedFS{fs: fs, root: strings.Trim(root, "/")}
}

type rootedFS struct {
	fs   FS
	root string
}

func (r *rootedFS) full(p string) string {
	return strings.Trim(path.Join(r.root, strings.Trim(p, "/")), "/")
}

func (r *rootedFS) List(p string) ([]string, error)      { return r.fs.List(r.full(p)) }
func (r *rootedFS) Stat(p string) (FileInfo, error)      { return r.fs.Stat(r.full(p)) }
func (r *rootedFS) MkDir(p string) error                 { return r.fs.MkDir(r.full(p)) }
func (r *rootedFS) Remove(p string) error                { return r.fs.Remove(r.full(p)) }
func (r *rootedFS) Rename(from, to string) error         { return r.fs.Rename(r.full(from), r.full(to)) }
func (r *rootedFS) SetMtime(p string, t time.Time) error { return r.fs.SetMtime(r.full(p), t) }

func (r *rootedFS) OpenFile(p string, flag int) (File, error) {
	return r.fs.OpenFile(r.full(p), flag)
}

func (r *rootedFS) DeviceInfo() (uint64, uint64, error) { return r.fs.DeviceInfo() }
