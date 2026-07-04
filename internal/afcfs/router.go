package afcfs

import (
	"io/fs"
	"sort"
	"strings"
	"time"
)

// NamedFS pairs a display name with a filesystem, for NewRouter.
type NamedFS struct {
	Name string
	FS   FS
}

// Router presents several filesystems as subdirectories of one virtual root,
// each under its Name — so a single NFS mount can expose every Lightroom app
// on a device as "<app>/…". The virtual top level is read-only: creating,
// deleting, or renaming an entry directory is rejected; every operation
// inside an entry delegates to that entry's filesystem, and renames may not
// cross entries.
type Router struct {
	order []string
	subs  map[string]FS
}

// NewRouter builds a Router over entries; duplicate names keep the first.
func NewRouter(entries []NamedFS) *Router {
	r := &Router{subs: make(map[string]FS, len(entries))}
	for _, e := range entries {
		if _, dup := r.subs[e.Name]; dup {
			continue
		}
		r.order = append(r.order, e.Name)
		r.subs[e.Name] = e.FS
	}
	return r
}

// splitSeg splits a cleaned path into its first segment and the remainder.
func splitSeg(p string) (head, rest string) {
	p = strings.Trim(p, "/")
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return p, ""
}

func routeErr(op, p string, err error) error {
	return &fs.PathError{Op: op, Path: p, Err: err}
}

func (r *Router) List(p string) ([]string, error) {
	if strings.Trim(p, "/") == "" {
		out := make([]string, len(r.order))
		copy(out, r.order)
		sort.Strings(out)
		return out, nil
	}
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok {
		return nil, routeErr("list", p, fs.ErrNotExist)
	}
	return s.List(rest)
}

func (r *Router) Stat(p string) (FileInfo, error) {
	if strings.Trim(p, "/") == "" {
		return FileInfo{Name: "", IsDir: true}, nil
	}
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok {
		return FileInfo{}, routeErr("stat", p, fs.ErrNotExist)
	}
	if rest == "" {
		return FileInfo{Name: head, IsDir: true}, nil
	}
	return s.Stat(rest)
}

func (r *Router) MkDir(p string) error {
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok || rest == "" {
		return routeErr("mkdir", p, fs.ErrPermission) // no new top-level entries
	}
	return s.MkDir(rest)
}

func (r *Router) Remove(p string) error {
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok {
		return routeErr("remove", p, fs.ErrNotExist)
	}
	if rest == "" {
		return routeErr("remove", p, fs.ErrPermission) // can't remove an app dir
	}
	return s.Remove(rest)
}

func (r *Router) Rename(from, to string) error {
	fh, fr := splitSeg(from)
	th, tr := splitSeg(to)
	if fr == "" || tr == "" {
		return routeErr("rename", from, fs.ErrPermission)
	}
	if fh != th {
		return routeErr("rename", from, fs.ErrInvalid) // no cross-app moves
	}
	s, ok := r.subs[fh]
	if !ok {
		return routeErr("rename", from, fs.ErrNotExist)
	}
	return s.Rename(fr, tr)
}

func (r *Router) SetMtime(p string, t time.Time) error {
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok {
		return routeErr("chtimes", p, fs.ErrNotExist)
	}
	if rest == "" {
		return nil // virtual dir has no stored mtime
	}
	return s.SetMtime(rest, t)
}

func (r *Router) OpenFile(p string, flag int) (File, error) {
	head, rest := splitSeg(p)
	s, ok := r.subs[head]
	if !ok {
		return nil, routeErr("open", p, fs.ErrNotExist)
	}
	if rest == "" {
		return nil, routeErr("open", p, fs.ErrInvalid) // it's a directory
	}
	return s.OpenFile(rest, flag)
}

func (r *Router) DeviceInfo() (uint64, uint64, error) {
	for _, name := range r.order {
		return r.subs[name].DeviceInfo()
	}
	return 0, 0, routeErr("deviceinfo", "/", fs.ErrNotExist)
}
