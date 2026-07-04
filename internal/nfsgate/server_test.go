package nfsgate

import (
	"bytes"
	"net"
	"testing"

	nfs "github.com/willscott/go-nfs"
	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"
	xdr "github.com/willscott/go-nfs-client/nfs/xdr"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

func startServer(t *testing.T) (*afcfs.MemFS, *nfsc.Target) {
	t.Helper()
	m := afcfs.NewMemFS()
	m.AddFile("Documents/seed.txt", 4)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() { _ = Serve(l, m, "Documents") }()

	c, err := rpc.DialTCP(l.Addr().Network(), l.Addr().(*net.TCPAddr).String(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	var mounter nfsc.Mount
	mounter.Client = c
	target, err := mounter.Mount("/", rpc.AuthNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounter.Unmount() })
	return m, target
}

func TestWriteIsOnDeviceWhenAcknowledged(t *testing.T) {
	m, target := startServer(t)

	f, err := target.OpenFile("/hello.txt", 0o666)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("write-through!")
	if _, err := f.Write(payload); err != nil {
		t.Fatal(err)
	}
	// The client got its WRITE reply; the bytes must already be in the
	// backing FS — no flush, no close.
	if data, ok := m.Contents("Documents/hello.txt"); !ok || !bytes.Equal(data, payload) {
		t.Fatalf("backing store = %q %v, want %q", data, ok, payload)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadRenameRemoveRoundTrip(t *testing.T) {
	m, target := startServer(t)

	// read the seeded file through NFS
	mf, err := target.Open("/seed.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := mf.Read(buf); err != nil && err.Error() != "EOF" {
		t.Fatal(err)
	}
	_ = mf.Close()

	// mkdir + move the file into it
	if _, err := target.Mkdir("/styles", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := target.Rename("/seed.txt", "/styles/seed.txt"); err != nil {
		t.Fatal(err)
	}
	if !m.Has("Documents/styles/seed.txt") || m.Has("Documents/seed.txt") {
		t.Fatal("rename not reflected on device")
	}

	// remove
	if err := target.Remove("/styles/seed.txt"); err != nil {
		t.Fatal(err)
	}
	if m.Has("Documents/styles/seed.txt") {
		t.Fatal("remove not reflected on device")
	}
}

func TestFSStatReportsDeviceSpace(t *testing.T) {
	_, target := startServer(t)
	fsstat, err := fsStat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fsstat.TotalSize == 0 || fsstat.FreeSize == 0 {
		t.Fatalf("fsstat = %+v; Finder refuses copies onto 0-byte volumes", fsstat)
	}
}

// fsStat issues a raw NFSv3 FSSTAT RPC (disk-space info). The vendored
// go-nfs-client only wraps FSINFO (rtmax/wtmax et al.), not FSSTAT, so this
// hand-rolls the call the same way go-nfs's own nfs_test.go hand-rolls
// READDIR: Target.Call for the RPC round trip, then decode the RFC 1813
// FSSTAT3resok layout (matching the server's nfs.FSStat field order).
func fsStat(target *nfsc.Target) (*nfs.FSStat, error) {
	_, fh, err := target.Lookup(".")
	if err != nil {
		return nil, err
	}

	type fsStatArgs struct {
		rpc.Header
		FH []byte
	}
	type fsStatOk struct {
		Attr           nfsc.PostOpAttr
		TotalSize      uint64
		FreeSize       uint64
		AvailableSize  uint64
		TotalFiles     uint64
		FreeFiles      uint64
		AvailableFiles uint64
	}

	res, err := target.Call(&fsStatArgs{
		Header: rpc.Header{
			Rpcvers: 2,
			Prog:    nfsc.Nfs3Prog,
			Vers:    nfsc.Nfs3Vers,
			Proc:    uint32(nfs.NFSProcedureFSStat),
			Cred:    rpc.AuthNull,
			Verf:    rpc.AuthNull,
		},
		FH: fh,
	})
	if err != nil {
		return nil, err
	}

	status, err := xdr.ReadUint32(res)
	if err != nil {
		return nil, err
	}
	if err := nfsc.NFS3Error(status); err != nil {
		return nil, err
	}

	ok := new(fsStatOk)
	if err := xdr.Read(res, ok); err != nil {
		return nil, err
	}
	return &nfs.FSStat{
		TotalSize:     ok.TotalSize,
		FreeSize:      ok.FreeSize,
		AvailableSize: ok.AvailableSize,
	}, nil
}
