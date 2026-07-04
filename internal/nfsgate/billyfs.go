// Package nfsgate exposes an afcfs.FS as an NFSv3 server via go-nfs.
package nfsgate

import (
	"io"
	"os"
	gopath "path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

// BillyFS adapts afcfs.FS to billy.Filesystem for go-nfs. All billy paths
// resolve under root (the app's Documents dir on the device).
type BillyFS struct {
	fs   afcfs.FS
	root string
}

func NewBillyFS(fs afcfs.FS, root string) *BillyFS {
	return &BillyFS{fs: fs, root: strings.Trim(root, "/")}
}

// resolve maps a billy path onto a device path. Cleaning with a leading "/"
// first collapses any ".." so paths cannot escape the root.
func (b *BillyFS) resolve(name string) string {
	name = gopath.Clean("/" + name)
	return strings.Trim(gopath.Join(b.root, name), "/")
}

// fileInfo adapts afcfs.FileInfo to os.FileInfo. AFC has no permission
// model; 0644/0755 keep NFS clients content.
type fileInfo struct {
	name string
	fi   afcfs.FileInfo
}

func (f fileInfo) Name() string { return f.name }
func (f fileInfo) Size() int64  { return f.fi.Size }
func (f fileInfo) Mode() os.FileMode {
	if f.fi.IsDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (f fileInfo) ModTime() time.Time { return f.fi.ModTime }
func (f fileInfo) IsDir() bool        { return f.fi.IsDir }
func (f fileInfo) Sys() any           { return nil }

func (b *BillyFS) Create(filename string) (billy.File, error) {
	return b.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
}

func (b *BillyFS) Open(filename string) (billy.File, error) {
	return b.OpenFile(filename, os.O_RDONLY, 0)
}

func (b *BillyFS) OpenFile(filename string, flag int, _ os.FileMode) (billy.File, error) {
	h, err := b.fs.OpenFile(b.resolve(filename), flag)
	if err != nil {
		return nil, err
	}
	return &file{name: filename, f: h}, nil
}

func (b *BillyFS) Stat(filename string) (os.FileInfo, error) {
	fi, err := b.fs.Stat(b.resolve(filename))
	if err != nil {
		return nil, err
	}
	name := gopath.Base(gopath.Clean("/" + filename))
	return fileInfo{name: name, fi: fi}, nil
}

func (b *BillyFS) Lstat(filename string) (os.FileInfo, error) { return b.Stat(filename) }

func (b *BillyFS) Rename(oldpath, newpath string) error {
	return b.fs.Rename(b.resolve(oldpath), b.resolve(newpath))
}

func (b *BillyFS) Remove(filename string) error {
	return b.fs.Remove(b.resolve(filename))
}

func (b *BillyFS) Join(elem ...string) string { return gopath.Join(elem...) }

func (b *BillyFS) ReadDir(p string) ([]os.FileInfo, error) {
	dir := b.resolve(p)
	names, err := b.fs.List(dir)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	out := make([]os.FileInfo, 0, len(names))
	for _, n := range names {
		fi, err := b.fs.Stat(strings.Trim(gopath.Join(dir, n), "/"))
		if err != nil {
			continue // raced deletion on the device
		}
		out = append(out, fileInfo{name: n, fi: fi})
	}
	return out, nil
}

func (b *BillyFS) MkdirAll(p string, _ os.FileMode) error {
	full := b.resolve(p)
	if full == "" {
		return nil
	}
	cur := ""
	for _, seg := range strings.Split(full, "/") {
		cur = strings.Trim(gopath.Join(cur, seg), "/")
		if fi, err := b.fs.Stat(cur); err == nil && fi.IsDir {
			continue
		}
		if err := b.fs.MkDir(cur); err != nil {
			return err
		}
	}
	return nil
}

func (b *BillyFS) Symlink(string, string) (err error) { return billy.ErrNotSupported }
func (b *BillyFS) Readlink(string) (string, error)    { return "", billy.ErrNotSupported }
func (b *BillyFS) TempFile(string, string) (billy.File, error) {
	return nil, billy.ErrNotSupported
}
func (b *BillyFS) Chroot(string) (billy.Filesystem, error) { return nil, billy.ErrNotSupported }
func (b *BillyFS) Root() string                            { return "/" }

// billy.Change. AFC has no permission/ownership model: Chmod/Chown succeed
// silently (failing them would abort Finder copies), Chtimes maps to AFC
// set-mtime.
func (b *BillyFS) Chmod(string, os.FileMode) error { return nil }
func (b *BillyFS) Lchown(string, int, int) error   { return nil }
func (b *BillyFS) Chown(string, int, int) error    { return nil }
func (b *BillyFS) Chtimes(name string, _ time.Time, mtime time.Time) error {
	return b.fs.SetMtime(b.resolve(name), mtime)
}

// Capabilities omits LockCapability: go-nfs has no NLM support and the
// volume is mounted with nolocks.
func (b *BillyFS) Capabilities() billy.Capability {
	return billy.WriteCapability | billy.ReadCapability |
		billy.ReadAndWriteCapability | billy.SeekCapability | billy.TruncateCapability
}

// file wraps one device handle as a billy.File. The mutex serializes ops on
// this handle (go-nfs may touch a file from concurrent RPCs); distinct files
// have distinct device handles and never block each other.
type file struct {
	name string
	f    afcfs.File
	mu   sync.Mutex
}

func (f *file) Name() string { return f.name }

func (f *file) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Read(p)
}

func (f *file) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Write(p)
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Seek(offset, whence)
}

func (f *file) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.f.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(f.f, p)
	if err == io.ErrUnexpectedEOF {
		err = io.EOF
	}
	return n, err
}

func (f *file) Truncate(size int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Truncate(size)
}

func (f *file) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Close()
}

func (f *file) Lock() error   { return nil }
func (f *file) Unlock() error { return nil }
