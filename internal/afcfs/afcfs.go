// Package afcfs is the single boundary between lrpush logic and go-ios AFC.
// Logic depends only on the FS interface so it can be tested with MemFS.
package afcfs

import (
	"os"

	"github.com/danielpaulus/go-ios/ios/afc"
)

// FileInfo is the subset of stat data lrpush needs. go-ios v1.2.0 exposes no
// modification time, so there is deliberately no ModTime field here.
type FileInfo struct {
	Name  string
	IsDir bool
	Size  int64
}

// FS is the device filesystem surface lrpush depends on.
type FS interface {
	List(devicePath string) ([]string, error)
	Stat(devicePath string) (FileInfo, error)
	MkDir(devicePath string) error
	RemoveAll(devicePath string) error
	// Pull recursively copies a device path (file or dir) to localDst.
	Pull(deviceSrc, localDst string) error
	// PushFile pushes a single local file to an exact device path.
	PushFile(localSrc, deviceDst string) error
}

type clientFS struct{ c *afc.Client }

// Wrap adapts a go-ios afc.Client to the FS interface.
func Wrap(c *afc.Client) FS { return &clientFS{c: c} }

func (f *clientFS) List(p string) ([]string, error) { return f.c.List(p) }

func (f *clientFS) Stat(p string) (FileInfo, error) {
	fi, err := f.c.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Name: fi.Name, IsDir: fi.IsDir(), Size: fi.Size}, nil
}

func (f *clientFS) MkDir(p string) error       { return f.c.MkDir(p) }
func (f *clientFS) RemoveAll(p string) error   { return f.c.RemoveAll(p) }
func (f *clientFS) Pull(src, dst string) error { return f.c.Pull(src, dst) }

// PushFile uses WriteToFile (not Push) so the device path is written exactly,
// avoiding go-ios Push's "append basename if dst is a dir" behavior.
func (f *clientFS) PushFile(localSrc, deviceDst string) error {
	in, err := os.Open(localSrc)
	if err != nil {
		return err
	}
	defer in.Close()
	return f.c.WriteToFile(in, deviceDst)
}
