// Package nfsgate exposes an afcfs.FS as an NFSv3 server via go-nfs.
package nfsgate

import (
	"context"
	"net"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

// handleCacheSize bounds the NFS file-handle LRU. Evicting a live handle
// surfaces as NFS3ERR_STALE in Finder, so follow rclone's default (1M —
// entries are a UUID plus a path, allocated lazily) rather than go-nfs's
// 1024 example value.
const handleCacheSize = 1_000_000

// handler is NullAuth plus an FSStat sourced from the device, because the
// default all-zero FSStat makes Finder treat the volume as full.
type handler struct {
	nfs.Handler
	fs afcfs.FS
}

func (h *handler) FSStat(ctx context.Context, f billy.Filesystem, s *nfs.FSStat) error {
	total, free, err := h.fs.DeviceInfo()
	if err != nil {
		return err
	}
	s.TotalSize = total
	s.FreeSize = free
	s.AvailableSize = free
	return nil
}

// Serve answers MOUNT and NFSv3 for fs (rooted at root) on l until l closes.
func Serve(l net.Listener, fs afcfs.FS, root string) error {
	// The macOS client probes features the server doesn't implement
	// (e.g. exclusive CREATE) and falls back cleanly, but go-nfs logs each
	// probe as a scary ERROR line. Nothing go-nfs logs is actionable for
	// users — real failures surface as NFS errors in Finder and as our own
	// messages — so silence everything short of a panic.
	nfs.Log.SetLevel(nfs.PanicLevel)
	bfs := NewBillyFS(fs, root)
	h := &handler{Handler: nfshelper.NewNullAuthHandler(bfs), fs: fs}
	return nfs.Serve(l, nfshelper.NewCachingHandler(h, handleCacheSize))
}
