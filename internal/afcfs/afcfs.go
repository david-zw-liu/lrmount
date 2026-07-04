// Package afcfs is the single boundary between lrmount logic and the AFC
// protocol client. Logic depends only on the FS interface so it can be
// tested with MemFS.
package afcfs

import (
	"io"
	"os"
	"time"

	"github.com/david-zw-liu/lrmount/internal/afc"
)

// FileInfo is the subset of stat data lrmount needs.
type FileInfo struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// File is one open handle on the device.
type File interface {
	io.Reader
	io.Writer
	io.Closer
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// FS is the device filesystem surface lrmount depends on. Paths use "/"
// separators relative to the AFC root, no leading slash.
type FS interface {
	List(p string) ([]string, error)
	Stat(p string) (FileInfo, error)
	MkDir(p string) error
	// Remove is non-recursive and fails on non-empty directories, matching
	// what NFS REMOVE/RMDIR require.
	Remove(p string) error
	Rename(from, to string) error
	SetMtime(p string, t time.Time) error
	// OpenFile takes an os.O_* flag combination. Mirroring AFC semantics,
	// any writable mode creates a missing file.
	OpenFile(p string, flag int) (File, error)
	DeviceInfo() (total, free uint64, err error)
}

type clientFS struct{ c *afc.Conn }

// Wrap adapts an afc.Conn to the FS interface.
func Wrap(c *afc.Conn) FS { return &clientFS{c: c} }

func (f *clientFS) List(p string) ([]string, error) { return f.c.List(p) }

func (f *clientFS) Stat(p string) (FileInfo, error) {
	fi, err := f.c.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Name: fi.Name, IsDir: fi.IsDir, Size: fi.Size, ModTime: fi.ModTime}, nil
}

func (f *clientFS) MkDir(p string) error                 { return f.c.MkDir(p) }
func (f *clientFS) Remove(p string) error                { return f.c.Remove(p) }
func (f *clientFS) Rename(from, to string) error         { return f.c.Rename(from, to) }
func (f *clientFS) SetMtime(p string, t time.Time) error { return f.c.SetMtime(p, t) }

func (f *clientFS) OpenFile(p string, flag int) (File, error) {
	return f.c.Open(p, afcOpenMode(flag))
}

func (f *clientFS) DeviceInfo() (uint64, uint64, error) {
	di, err := f.c.DeviceInfo()
	if err != nil {
		return 0, 0, err
	}
	return di.TotalBytes, di.FreeBytes, nil
}

// afcOpenMode maps os.O_* flags onto AFC open modes. AFC's write-only mode
// truncates, so plain O_WRONLY maps to read-write-create to preserve content.
func afcOpenMode(flag int) uint64 {
	switch {
	case flag&os.O_APPEND != 0:
		if flag&os.O_RDWR != 0 {
			return afc.ModeRDAppend
		}
		return afc.ModeAppend
	case flag&os.O_TRUNC != 0:
		if flag&os.O_RDWR != 0 {
			return afc.ModeWR
		}
		return afc.ModeWROnly
	case flag&(os.O_RDWR|os.O_WRONLY) != 0:
		return afc.ModeRW
	default:
		return afc.ModeRDOnly
	}
}
