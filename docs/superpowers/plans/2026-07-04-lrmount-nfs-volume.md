# lrmount NFS Volume Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the mirror+watch flow with per-(device, Lightroom-app) Finder volumes: an embedded localhost NFSv3 server bridges NFS ops to the device over AFC; Finder eject flushes and unmounts; the program exits when every volume is gone.

**Architecture:** A new `internal/afc` package speaks the AFC wire protocol directly (go-ios v1.2.0 lacks seek/rename/mtime). `internal/afcfs` exposes it as a testable random-access FS interface with an upgraded MemFS. `internal/nfsgate` adapts that to `billy.Filesystem` for `willscott/go-nfs`. `internal/mountctl` runs `/sbin/mount`, detects eject by statfs polling, and unmounts via `diskutil`. `cmd/lrmount` orchestrates one volume per (device, app).

**Tech Stack:** Go 1.26.4, github.com/danielpaulus/go-ios (usbmux/lockdown only), github.com/willscott/go-nfs, github.com/go-git/go-billy/v5, golang.org/x/sys, test-only github.com/willscott/go-nfs-client.

**Spec:** `docs/superpowers/specs/2026-07-04-lrmount-nfs-volume-design.md`

## Global Constraints

- Zero end-user install: mount with `/sbin/mount`, unmount with `/usr/sbin/diskutil`. No macFUSE/FUSE-T/appex.
- Write-through invariant: no layer of ours may buffer writes; every NFS WRITE reaches the device before it is acknowledged. (go-nfs itself is write-through: each WRITE does OpenFile→Seek→Write→Close synchronously and replies `FILE_SYNC` — verified in `nfs_onwrite.go`.)
- AFC wire facts (verified against libimobiledevice + go-ios v1.2.0): everything little-endian; header 40 bytes = magic `0x4141504c36414643` ("CFA6LPAA"), entire_len, this_len, packet_num, opcode (5×u64). Status reply op=0x01 carries u64 error code (0=success, 8=NotFound, 10=PermDenied, 16=Exists, 33=DirNotEmpty). File times are **nanoseconds** since epoch.
- AFC is strict request/response on one socket: every round trip holds a mutex.
- Module path stays `github.com/davidliu/lrpush` until Task 9 renames everything to `github.com/david-zw-liu/lrmount`.
- Work on branch `feat/nfs-volume`. Run `gofmt -w .` and `go vet ./...` before every commit.
- macOS-only runtime; `internal/mountctl` files carry `//go:build darwin`.

---

### Task 1: internal/afc — packet codec and errors

**Files:**
- Create: `internal/afc/packet.go`
- Create: `internal/afc/errors.go`
- Test: `internal/afc/packet_test.go`

**Interfaces:**
- Consumes: nothing (leaf package).
- Produces (package-internal, used by Task 2): `writePacket(w io.Writer, packetNum uint64, p packet) error`, `readPacket(r io.Reader) (packet, error)`, `type packet struct{ op uint64; headerPayload, payload []byte }`, opcode constants `opStatus…opSetFileModTime`, `type Error struct{ Code uint64 }` with `Is()` mapping 8/16/10 → `fs.ErrNotExist/ErrExist/ErrPermission`, plus `pathErr(op, path string, err error) error` producing `*fs.PathError` whose `Err` is the bare sentinel (so `os.IsNotExist` works — it does not call `errors.Is`).

- [ ] **Step 1: Write the failing test**

```go
// internal/afc/packet_test.go
package afc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"testing"
)

func le64(vs ...uint64) []byte {
	b := make([]byte, 8*len(vs))
	for i, v := range vs {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	return b
}

func TestWritePacketFrames(t *testing.T) {
	var buf bytes.Buffer
	hp := []byte("from\x00to\x00")
	if err := writePacket(&buf, 7, packet{op: opRenamePath, headerPayload: hp}); err != nil {
		t.Fatal(err)
	}
	want := append([]byte("CFA6LPAA"), le64(40+9, 40+9, 7, 0x18)...)
	want = append(want, hp...)
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("frame mismatch\n got %x\nwant %x", buf.Bytes(), want)
	}
}

func TestWritePacketPayloadInEntireLenOnly(t *testing.T) {
	var buf bytes.Buffer
	hp := le64(3)                  // e.g. a file handle
	body := []byte("hello")        // bulk payload (file write)
	if err := writePacket(&buf, 1, packet{op: opFileWrite, headerPayload: hp, payload: body}); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if got := binary.LittleEndian.Uint64(b[8:]); got != 40+8+5 {
		t.Fatalf("entire_len = %d, want %d", got, 40+8+5)
	}
	if got := binary.LittleEndian.Uint64(b[16:]); got != 40+8 {
		t.Fatalf("this_len = %d, want %d", got, 40+8)
	}
}

func TestReadPacketRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writePacket(&buf, 2, packet{op: opData, headerPayload: le64(9), payload: []byte("xy")}); err != nil {
		t.Fatal(err)
	}
	p, err := readPacket(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if p.op != opData || !bytes.Equal(p.headerPayload, le64(9)) || string(p.payload) != "xy" {
		t.Fatalf("unexpected packet %+v", p)
	}
}

func TestReadPacketRejectsBadMagic(t *testing.T) {
	raw := append([]byte("XXXXXXXX"), le64(40, 40, 1, 1)...)
	if _, err := readPacket(bytes.NewReader(raw)); err == nil {
		t.Fatal("want error for bad magic")
	}
}

func TestErrorSentinelMapping(t *testing.T) {
	err := pathErr("stat", "a/b", &Error{Code: 8})
	if !os.IsNotExist(err) {
		t.Fatalf("os.IsNotExist(%v) = false", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("errors.Is(%v, fs.ErrNotExist) = false", err)
	}
	err = pathErr("open", "a", &Error{Code: 16})
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("want ErrExist, got %v", err)
	}
	// unmapped codes keep the *Error so callers can inspect Code
	err = pathErr("remove", "d", &Error{Code: 33})
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != 33 {
		t.Fatalf("want afc code 33 preserved, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/afc/ -v`
Expected: FAIL — `undefined: writePacket` etc.

- [ ] **Step 3: Write the implementation**

```go
// internal/afc/packet.go
// Package afc speaks the AFC (Apple File Conduit) wire protocol. It exists
// because go-ios v1.2.0 exposes no seek/rename/set-mtime, which a filesystem
// backend needs. All wire values are little-endian.
package afc

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	magic      uint64 = 0x4141504c36414643 // "CFA6LPAA"
	headerSize uint64 = 40
)

// Opcodes, per libimobiledevice src/afc.h.
const (
	opStatus         uint64 = 0x01
	opData           uint64 = 0x02
	opReadDir        uint64 = 0x03
	opRemovePath     uint64 = 0x08
	opMakeDir        uint64 = 0x09
	opGetFileInfo    uint64 = 0x0A
	opGetDevInfo     uint64 = 0x0B
	opFileOpen       uint64 = 0x0D
	opFileOpenResult uint64 = 0x0E
	opFileRead       uint64 = 0x0F
	opFileWrite      uint64 = 0x10
	opFileSeek       uint64 = 0x11
	opFileTell       uint64 = 0x12
	opFileTellResult uint64 = 0x13
	opFileClose      uint64 = 0x14
	opFileSetSize    uint64 = 0x15
	opRenamePath     uint64 = 0x18
	opSetFileModTime uint64 = 0x1E
)

// packet is one AFC frame. headerPayload carries the op's fixed args and path
// strings (counted in this_len); payload carries bulk data — only file-write
// bodies on send, and listing/read results on receive.
type packet struct {
	op            uint64
	headerPayload []byte
	payload       []byte
}

func writePacket(w io.Writer, packetNum uint64, p packet) error {
	hdr := make([]byte, headerSize)
	binary.LittleEndian.PutUint64(hdr[0:], magic)
	binary.LittleEndian.PutUint64(hdr[8:], headerSize+uint64(len(p.headerPayload))+uint64(len(p.payload)))
	binary.LittleEndian.PutUint64(hdr[16:], headerSize+uint64(len(p.headerPayload)))
	binary.LittleEndian.PutUint64(hdr[24:], packetNum)
	binary.LittleEndian.PutUint64(hdr[32:], p.op)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if len(p.headerPayload) > 0 {
		if _, err := w.Write(p.headerPayload); err != nil {
			return err
		}
	}
	if len(p.payload) > 0 {
		if _, err := w.Write(p.payload); err != nil {
			return err
		}
	}
	return nil
}

func readPacket(r io.Reader) (packet, error) {
	hdr := make([]byte, headerSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return packet{}, err
	}
	if m := binary.LittleEndian.Uint64(hdr[0:]); m != magic {
		return packet{}, fmt.Errorf("afc: bad magic %#x", m)
	}
	entire := binary.LittleEndian.Uint64(hdr[8:])
	this := binary.LittleEndian.Uint64(hdr[16:])
	if this < headerSize || entire < this {
		return packet{}, fmt.Errorf("afc: bad lengths entire=%d this=%d", entire, this)
	}
	p := packet{op: binary.LittleEndian.Uint64(hdr[32:])}
	p.headerPayload = make([]byte, this-headerSize)
	if _, err := io.ReadFull(r, p.headerPayload); err != nil {
		return packet{}, err
	}
	p.payload = make([]byte, entire-this)
	if _, err := io.ReadFull(r, p.payload); err != nil {
		return packet{}, err
	}
	return p, nil
}
```

```go
// internal/afc/errors.go
package afc

import (
	"errors"
	"fmt"
	"io/fs"
)

// AFC status codes (subset; full list in libimobiledevice afc_error_t).
const (
	codeSuccess        uint64 = 0
	codeObjectNotFound uint64 = 8
	codeObjectIsDir    uint64 = 9
	codePermDenied     uint64 = 10
	codeObjectExists   uint64 = 16
	codeNoSpaceLeft    uint64 = 18
	codeDirNotEmpty    uint64 = 33
)

var codeNames = map[uint64]string{
	codeObjectNotFound: "object not found",
	codeObjectIsDir:    "object is a directory",
	codePermDenied:     "permission denied",
	codeObjectExists:   "object exists",
	codeNoSpaceLeft:    "no space left",
	codeDirNotEmpty:    "directory not empty",
}

// Error is a non-zero AFC status reply.
type Error struct{ Code uint64 }

func (e *Error) Error() string {
	if n, ok := codeNames[e.Code]; ok {
		return fmt.Sprintf("afc: %s (code %d)", n, e.Code)
	}
	return fmt.Sprintf("afc: error code %d", e.Code)
}

// Is supports errors.Is against the fs sentinel errors.
func (e *Error) Is(target error) bool {
	switch target {
	case fs.ErrNotExist:
		return e.Code == codeObjectNotFound
	case fs.ErrExist:
		return e.Code == codeObjectExists
	case fs.ErrPermission:
		return e.Code == codePermDenied
	}
	return false
}

// pathErr wraps an op error as *fs.PathError. Codes with an fs sentinel use
// the bare sentinel as Err because os.IsNotExist (used inside go-nfs) only
// unwraps one PathError level and compares == — it never calls errors.Is.
func pathErr(op, path string, err error) error {
	var ae *Error
	if errors.As(err, &ae) {
		switch ae.Code {
		case codeObjectNotFound:
			err = fs.ErrNotExist
		case codeObjectExists:
			err = fs.ErrExist
		case codePermDenied:
			err = fs.ErrPermission
		}
	}
	return &fs.PathError{Op: op, Path: path, Err: err}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/afc/ -v`
Expected: PASS (all 5 tests).

- [ ] **Step 5: Commit**

```bash
gofmt -w . && go vet ./...
git add internal/afc
git commit -m "feat(afc): AFC packet codec and status errors"
```

---

### Task 2: internal/afc — Conn operations and File handles

**Files:**
- Create: `internal/afc/conn.go`
- Create: `internal/afc/file.go`
- Test: `internal/afc/conn_test.go`

**Interfaces:**
- Consumes: Task 1's codec.
- Produces (used by Tasks 3–4):
  - `func NewConn(rw io.ReadWriteCloser) *Conn`, `(*Conn).Close() error`
  - `(*Conn).List(p string) ([]string, error)`
  - `(*Conn).Stat(p string) (FileInfo, error)` — `type FileInfo struct{ Name string; IsDir, IsLink bool; Size int64; ModTime time.Time }`
  - `(*Conn).DeviceInfo() (DeviceInfo, error)` — `type DeviceInfo struct{ TotalBytes, FreeBytes uint64 }`
  - `(*Conn).MkDir(p string) error`, `(*Conn).Remove(p string) error`, `(*Conn).Rename(from, to string) error`, `(*Conn).SetMtime(p string, t time.Time) error`
  - `(*Conn).Open(p string, mode uint64) (*File, error)` with mode constants `ModeRDOnly=1, ModeRW=2, ModeWROnly=3, ModeWR=4, ModeAppend=5, ModeRDAppend=6`
  - `(*File)`: `Read/Write([]byte) (int, error)`, `Seek(offset int64, whence int) (int64, error)`, `Truncate(size int64) error`, `Close() error`

- [ ] **Step 1: Write the failing test**

The fake device side of a `net.Pipe` reads one request, asserts its shape, and writes a scripted reply.

```go
// internal/afc/conn_test.go
package afc

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// script serves one request per entry on the device end of a pipe.
type step struct {
	wantOp uint64
	wantHP []byte // nil = don't check
	reply  packet
}

func fakeDevice(t *testing.T, steps []step) *Conn {
	t.Helper()
	cli, dev := net.Pipe()
	go func() {
		defer dev.Close()
		for _, s := range steps {
			req, err := readPacket(dev)
			if err != nil {
				t.Errorf("device read: %v", err)
				return
			}
			if req.op != s.wantOp {
				t.Errorf("op = %#x, want %#x", req.op, s.wantOp)
			}
			if s.wantHP != nil && !bytes.Equal(req.headerPayload, s.wantHP) {
				t.Errorf("headerPayload = %q, want %q", req.headerPayload, s.wantHP)
			}
			if err := writePacket(dev, 1, s.reply); err != nil {
				t.Errorf("device write: %v", err)
				return
			}
		}
	}()
	t.Cleanup(func() { cli.Close() })
	return NewConn(cli)
}

func okStatus() packet { return packet{op: opStatus, headerPayload: le64(codeSuccess)} }

func TestList(t *testing.T) {
	c := fakeDevice(t, []step{{
		wantOp: opReadDir, wantHP: []byte("Documents\x00"),
		reply: packet{op: opData, payload: []byte(".\x00..\x00a.xmp\x00sub\x00")},
	}})
	got, err := c.List("Documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a.xmp" || got[1] != "sub" {
		t.Fatalf("got %v", got)
	}
}

func TestStatParsesKVAndNanosecondMtime(t *testing.T) {
	kv := []byte("st_size\x0011\x00st_ifmt\x00S_IFREG\x00st_mtime\x001700000000123456789\x00")
	c := fakeDevice(t, []step{{wantOp: opGetFileInfo, wantHP: []byte("a/b.xmp\x00"),
		reply: packet{op: opData, payload: kv}}})
	fi, err := c.Stat("a/b.xmp")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Name != "b.xmp" || fi.IsDir || fi.Size != 11 {
		t.Fatalf("fi = %+v", fi)
	}
	if want := time.Unix(0, 1700000000123456789); !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v, want %v", fi.ModTime, want)
	}
}

func TestStatDir(t *testing.T) {
	kv := []byte("st_size\x0064\x00st_ifmt\x00S_IFDIR\x00st_mtime\x001\x00")
	c := fakeDevice(t, []step{{wantOp: opGetFileInfo, reply: packet{op: opData, payload: kv}}})
	fi, err := c.Stat("Documents")
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir {
		t.Fatal("want IsDir")
	}
}

func TestStatNotFound(t *testing.T) {
	c := fakeDevice(t, []step{{wantOp: opGetFileInfo,
		reply: packet{op: opStatus, headerPayload: le64(codeObjectNotFound)}}})
	_, err := c.Stat("gone")
	if err == nil || !errorsIsNotExist(err) {
		t.Fatalf("want not-exist, got %v", err)
	}
}

func TestDeviceInfo(t *testing.T) {
	kv := []byte("Model\x00iPhone\x00FSTotalBytes\x001000\x00FSFreeBytes\x00400\x00FSBlockSize\x004096\x00")
	c := fakeDevice(t, []step{{wantOp: opGetDevInfo, reply: packet{op: opData, payload: kv}}})
	di, err := c.DeviceInfo()
	if err != nil {
		t.Fatal(err)
	}
	if di.TotalBytes != 1000 || di.FreeBytes != 400 {
		t.Fatalf("di = %+v", di)
	}
}

func TestRenameSetMtimeMkDirRemove(t *testing.T) {
	mt := time.Unix(0, 42)
	wantMtimeHP := append(le64(42), []byte("f\x00")...)
	c := fakeDevice(t, []step{
		{wantOp: opRenamePath, wantHP: []byte("a\x00b\x00"), reply: okStatus()},
		{wantOp: opSetFileModTime, wantHP: wantMtimeHP, reply: okStatus()},
		{wantOp: opMakeDir, wantHP: []byte("d\x00"), reply: okStatus()},
		{wantOp: opRemovePath, wantHP: []byte("x\x00"), reply: okStatus()},
	})
	if err := c.Rename("a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetMtime("f", mt); err != nil {
		t.Fatal(err)
	}
	if err := c.MkDir("d"); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove("x"); err != nil {
		t.Fatal(err)
	}
}

func TestFileOpenReadWriteSeekTruncateClose(t *testing.T) {
	openHP := append(le64(ModeRW), []byte("f\x00")...)
	c := fakeDevice(t, []step{
		{wantOp: opFileOpen, wantHP: openHP,
			reply: packet{op: opFileOpenResult, headerPayload: le64(3)}},
		{wantOp: opFileRead, wantHP: le64(3, 4),
			reply: packet{op: opData, payload: []byte("data")}},
		{wantOp: opFileWrite, wantHP: le64(3), reply: okStatus()},
		{wantOp: opFileSeek, wantHP: le64(3, 0, 5), reply: okStatus()},
		{wantOp: opFileSeek, wantHP: append(le64(3, 2), leI64(-1)...), reply: okStatus()},
		{wantOp: opFileTell, wantHP: le64(3),
			reply: packet{op: opFileTellResult, headerPayload: le64(9)}},
		{wantOp: opFileSetSize, wantHP: le64(3, 5), reply: okStatus()},
		{wantOp: opFileClose, wantHP: le64(3), reply: okStatus()},
	})
	f, err := c.Open("f", ModeRW)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if n, err := f.Read(buf); err != nil || n != 4 || string(buf) != "data" {
		t.Fatalf("read = %d %v %q", n, err, buf)
	}
	if n, err := f.Write([]byte("hi")); err != nil || n != 2 {
		t.Fatalf("write = %d %v", n, err)
	}
	if pos, err := f.Seek(5, io.SeekStart); err != nil || pos != 5 {
		t.Fatalf("seek = %d %v", pos, err)
	}
	if pos, err := f.Seek(-1, io.SeekEnd); err != nil || pos != 9 {
		t.Fatalf("seek end = %d %v", pos, err)
	}
	if err := f.Truncate(5); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReadEOF(t *testing.T) {
	c := fakeDevice(t, []step{
		{wantOp: opFileOpen, reply: packet{op: opFileOpenResult, headerPayload: le64(3)}},
		{wantOp: opFileRead, reply: packet{op: opData}},
	})
	f, err := c.Open("f", ModeRDOnly)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Read(make([]byte, 4)); err != io.EOF {
		t.Fatalf("want io.EOF, got %v", err)
	}
}

func leI64(v int64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(v))
	return b
}

func errorsIsNotExist(err error) bool {
	return err != nil && (func() bool { defer func() { recover() }(); return osIsNotExist(err) })()
}
```

Replace the last helper with the direct form (simpler — use it instead of the recover contraption above):

```go
func osIsNotExist(err error) bool { return os.IsNotExist(err) }
```

and in `TestStatNotFound` call `os.IsNotExist(err)` directly (add `"os"` to imports, delete both helpers).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/afc/ -v`
Expected: FAIL — `undefined: NewConn` etc.

- [ ] **Step 3: Write the implementation**

```go
// internal/afc/conn.go
package afc

import (
	"encoding/binary"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Conn is an AFC client over one service connection. AFC is strict
// request/response, so every round trip holds mu for its full duration.
type Conn struct {
	mu        sync.Mutex
	rw        io.ReadWriteCloser
	packetNum uint64
}

func NewConn(rw io.ReadWriteCloser) *Conn { return &Conn{rw: rw} }

func (c *Conn) Close() error { return c.rw.Close() }

func (c *Conn) roundTrip(req packet) (packet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.packetNum++
	if err := writePacket(c.rw, c.packetNum, req); err != nil {
		return packet{}, err
	}
	resp, err := readPacket(c.rw)
	if err != nil {
		return packet{}, err
	}
	if resp.op == opStatus {
		if len(resp.headerPayload) < 8 {
			return packet{}, errors.New("afc: short status reply")
		}
		if code := binary.LittleEndian.Uint64(resp.headerPayload); code != codeSuccess {
			return packet{}, &Error{Code: code}
		}
	}
	return resp, nil
}

// cstr encodes strings as consecutive NUL-terminated byte runs.
func cstr(parts ...string) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
		b = append(b, 0)
	}
	return b
}

// parseKV decodes the alternating NUL-terminated key/value list AFC uses for
// GetFileInfo and GetDevInfo replies.
func parseKV(payload []byte) map[string]string {
	fields := strings.Split(string(payload), "\x00")
	kv := make(map[string]string, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		kv[fields[i]] = fields[i+1]
	}
	return kv
}

// FileInfo is stat data for one device path.
type FileInfo struct {
	Name    string
	IsDir   bool
	IsLink  bool
	Size    int64
	ModTime time.Time
}

// DeviceInfo describes the vended container's filesystem.
type DeviceInfo struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// List returns the entries of directory p without "." and "..".
func (c *Conn) List(p string) ([]string, error) {
	resp, err := c.roundTrip(packet{op: opReadDir, headerPayload: cstr(p)})
	if err != nil {
		return nil, pathErr("list", p, err)
	}
	var out []string
	for _, name := range strings.Split(string(resp.payload), "\x00") {
		if name == "" || name == "." || name == ".." {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func (c *Conn) Stat(p string) (FileInfo, error) {
	resp, err := c.roundTrip(packet{op: opGetFileInfo, headerPayload: cstr(p)})
	if err != nil {
		return FileInfo{}, pathErr("stat", p, err)
	}
	kv := parseKV(resp.payload)
	fi := FileInfo{
		Name:   path.Base("/" + p),
		IsDir:  kv["st_ifmt"] == "S_IFDIR",
		IsLink: kv["st_ifmt"] == "S_IFLNK",
	}
	fi.Size, _ = strconv.ParseInt(kv["st_size"], 10, 64)
	if ns, err := strconv.ParseInt(kv["st_mtime"], 10, 64); err == nil {
		fi.ModTime = time.Unix(0, ns)
	}
	return fi, nil
}

func (c *Conn) DeviceInfo() (DeviceInfo, error) {
	resp, err := c.roundTrip(packet{op: opGetDevInfo})
	if err != nil {
		return DeviceInfo{}, err
	}
	kv := parseKV(resp.payload)
	var di DeviceInfo
	di.TotalBytes, _ = strconv.ParseUint(kv["FSTotalBytes"], 10, 64)
	di.FreeBytes, _ = strconv.ParseUint(kv["FSFreeBytes"], 10, 64)
	return di, nil
}

func (c *Conn) MkDir(p string) error {
	_, err := c.roundTrip(packet{op: opMakeDir, headerPayload: cstr(p)})
	if err != nil {
		return pathErr("mkdir", p, err)
	}
	return nil
}

// Remove deletes one file or one empty directory (AFC fails with
// DirNotEmpty=33 otherwise) — exactly the semantics NFS REMOVE/RMDIR need.
func (c *Conn) Remove(p string) error {
	_, err := c.roundTrip(packet{op: opRemovePath, headerPayload: cstr(p)})
	if err != nil {
		return pathErr("remove", p, err)
	}
	return nil
}

func (c *Conn) Rename(from, to string) error {
	_, err := c.roundTrip(packet{op: opRenamePath, headerPayload: cstr(from, to)})
	if err != nil {
		return pathErr("rename", from, err)
	}
	return nil
}

// SetMtime sets p's modification time (AFC takes nanoseconds since epoch).
func (c *Conn) SetMtime(p string, t time.Time) error {
	hp := make([]byte, 8)
	binary.LittleEndian.PutUint64(hp, uint64(t.UnixNano()))
	hp = append(hp, cstr(p)...)
	_, err := c.roundTrip(packet{op: opSetFileModTime, headerPayload: hp})
	if err != nil {
		return pathErr("chtimes", p, err)
	}
	return nil
}
```

```go
// internal/afc/file.go
package afc

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Open modes, per libimobiledevice afc.h. Note WROnly/WR truncate, and every
// mode except RDOnly creates missing files.
const (
	ModeRDOnly   uint64 = 1 // r
	ModeRW       uint64 = 2 // r+, creates
	ModeWROnly   uint64 = 3 // w, creates + truncates
	ModeWR       uint64 = 4 // w+, creates + truncates
	ModeAppend   uint64 = 5 // a
	ModeRDAppend uint64 = 6 // a+
)

// File is one open handle. The device tracks the position per handle, so
// independent Files never disturb each other; ops on the same File must not
// be concurrent (callers serialize, see nfsgate).
type File struct {
	c      *Conn
	handle uint64
	name   string
}

func (c *Conn) Open(p string, mode uint64) (*File, error) {
	hp := make([]byte, 8)
	binary.LittleEndian.PutUint64(hp, mode)
	hp = append(hp, cstr(p)...)
	resp, err := c.roundTrip(packet{op: opFileOpen, headerPayload: hp})
	if err != nil {
		return nil, pathErr("open", p, err)
	}
	if resp.op != opFileOpenResult || len(resp.headerPayload) < 8 {
		return nil, fmt.Errorf("afc: open %s: unexpected reply op %#x", p, resp.op)
	}
	return &File{c: c, handle: binary.LittleEndian.Uint64(resp.headerPayload), name: p}, nil
}

func (f *File) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	resp, err := f.c.roundTrip(packet{op: opFileRead, headerPayload: le64pair(f.handle, uint64(len(p)))})
	if err != nil {
		return 0, pathErr("read", f.name, err)
	}
	if len(resp.payload) == 0 {
		return 0, io.EOF
	}
	return copy(p, resp.payload), nil
}

func (f *File) Write(p []byte) (int, error) {
	hp := make([]byte, 8)
	binary.LittleEndian.PutUint64(hp, f.handle)
	if _, err := f.c.roundTrip(packet{op: opFileWrite, headerPayload: hp, payload: p}); err != nil {
		return 0, pathErr("write", f.name, err)
	}
	return len(p), nil
}

// Seek moves the device-side position. Whence follows io.Seek* (same values
// AFC uses). For non-SeekStart the new position is fetched with FileTell.
func (f *File) Seek(offset int64, whence int) (int64, error) {
	hp := make([]byte, 24)
	binary.LittleEndian.PutUint64(hp, f.handle)
	binary.LittleEndian.PutUint64(hp[8:], uint64(whence))
	binary.LittleEndian.PutUint64(hp[16:], uint64(offset))
	if _, err := f.c.roundTrip(packet{op: opFileSeek, headerPayload: hp}); err != nil {
		return 0, pathErr("seek", f.name, err)
	}
	if whence == io.SeekStart {
		return offset, nil
	}
	resp, err := f.c.roundTrip(packet{op: opFileTell, headerPayload: le64single(f.handle)})
	if err != nil {
		return 0, pathErr("tell", f.name, err)
	}
	if resp.op != opFileTellResult || len(resp.headerPayload) < 8 {
		return 0, fmt.Errorf("afc: tell %s: unexpected reply op %#x", f.name, resp.op)
	}
	return int64(binary.LittleEndian.Uint64(resp.headerPayload)), nil
}

func (f *File) Truncate(size int64) error {
	if _, err := f.c.roundTrip(packet{op: opFileSetSize, headerPayload: le64pair(f.handle, uint64(size))}); err != nil {
		return pathErr("truncate", f.name, err)
	}
	return nil
}

func (f *File) Close() error {
	if _, err := f.c.roundTrip(packet{op: opFileClose, headerPayload: le64single(f.handle)}); err != nil {
		return pathErr("close", f.name, err)
	}
	return nil
}

func le64single(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

func le64pair(a, b uint64) []byte {
	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out, a)
	binary.LittleEndian.PutUint64(out[8:], b)
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/afc/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w . && go vet ./...
git add internal/afc
git commit -m "feat(afc): Conn ops incl. seek/rename/set-mtime and File handles"
```

---

### Task 3: internal/afc — house_arrest vend

**Files:**
- Create: `internal/afc/vend.go`
- Test: `internal/afc/vend_test.go`

**Interfaces:**
- Consumes: `NewConn` (Task 2); go-ios `ios.ConnectToService`, `ios.NewPlistCodec`, `ios.ParsePlist`, `ios.DeviceEntry`.
- Produces: `func Vend(device ios.DeviceEntry, bundleID, command string) (*Conn, error)`; internal `vendExchange(rw io.ReadWriter, bundleID, command string) error` (tested directly).

- [ ] **Step 1: Write the failing test**

```go
// internal/afc/vend_test.go
package afc

import (
	"bytes"
	"testing"

	"github.com/danielpaulus/go-ios/ios"
)

// plistRW answers one codec-framed plist request with a canned response.
type plistRW struct {
	in   bytes.Buffer // what the client wrote
	out  *bytes.Reader
	resp map[string]interface{}
}

func newPlistRW(t *testing.T, resp map[string]interface{}) *plistRW {
	t.Helper()
	codec := ios.NewPlistCodec()
	raw, err := codec.Encode(resp)
	if err != nil {
		t.Fatal(err)
	}
	return &plistRW{out: bytes.NewReader(raw), resp: resp}
}

func (p *plistRW) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *plistRW) Read(b []byte) (int, error)  { return p.out.Read(b) }

func TestVendExchangeComplete(t *testing.T) {
	rw := newPlistRW(t, map[string]interface{}{"Status": "Complete"})
	if err := vendExchange(rw, "com.adobe.lrmobile", "VendDocuments"); err != nil {
		t.Fatal(err)
	}
	// the request must be a codec frame containing our command + identifier
	sent := rw.in.String()
	for _, want := range []string{"VendDocuments", "com.adobe.lrmobile", "Command", "Identifier"} {
		if !bytes.Contains([]byte(sent), []byte(want)) {
			t.Fatalf("request missing %q:\n%s", want, sent)
		}
	}
}

func TestVendExchangeError(t *testing.T) {
	rw := newPlistRW(t, map[string]interface{}{"Error": "InstallationLookupFailed"})
	err := vendExchange(rw, "com.example", "VendContainer")
	if err == nil || err.Error() != "InstallationLookupFailed" {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/afc/ -run TestVend -v`
Expected: FAIL — `undefined: vendExchange`.

- [ ] **Step 3: Write the implementation**

```go
// internal/afc/vend.go
package afc

import (
	"errors"
	"fmt"
	"io"

	"github.com/danielpaulus/go-ios/ios"
)

const houseArrestService = "com.apple.mobile.house_arrest"

// Vend opens house_arrest for bundleID with the given command
// ("VendDocuments" for file-sharing apps, "VendContainer" for the full
// sandbox) and returns an AFC conn rooted in the vended container. The same
// socket carries one plist exchange and then plain AFC.
func Vend(device ios.DeviceEntry, bundleID, command string) (*Conn, error) {
	conn, err := ios.ConnectToService(device, houseArrestService)
	if err != nil {
		return nil, fmt.Errorf("connect house_arrest: %w", err)
	}
	if err := vendExchange(conn, bundleID, command); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%s %s: %w", command, bundleID, err)
	}
	return NewConn(conn), nil
}

func vendExchange(rw io.ReadWriter, bundleID, command string) error {
	codec := ios.NewPlistCodec()
	msg, err := codec.Encode(map[string]interface{}{"Command": command, "Identifier": bundleID})
	if err != nil {
		return err
	}
	if _, err := rw.Write(msg); err != nil {
		return err
	}
	respBytes, err := codec.Decode(rw)
	if err != nil {
		return err
	}
	resp, err := ios.ParsePlist(respBytes)
	if err != nil {
		return err
	}
	if status, _ := resp["Status"].(string); status == "Complete" {
		return nil
	}
	if e, _ := resp["Error"].(string); e != "" {
		return errors.New(e)
	}
	return errors.New("unexpected house_arrest response")
}
```

Note: `ios.DeviceConnectionInterface` embeds `io.ReadWriteCloser`, so the service conn satisfies both `vendExchange`'s `io.ReadWriter` and `NewConn`'s `io.ReadWriteCloser`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/afc/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w . && go vet ./...
git add internal/afc
git commit -m "feat(afc): house_arrest vend handshake"
```

---

### Task 4: afcfs v2 (random access) + MemFS v2; drop mirror; interim cmd

This task swaps the FS boundary and removes everything that depended on the old copy-based interface. The build and all tests stay green; `lrpush` temporarily only lists what it found (full mount flow lands in Task 8).

**Files:**
- Rewrite: `internal/afcfs/afcfs.go`
- Rewrite: `internal/afcfs/memfs.go`
- Rewrite: `internal/afcfs/memfs_test.go`
- Modify: `internal/device/device.go` (vend via `internal/afc`, drop go-ios afc import)
- Modify: `internal/locate/locate.go` (delete `SelectCatalog`), `internal/locate/locate_test.go` (delete its tests)
- Modify: `cmd/lrpush/root.go` (interim run()), `cmd/lrpush/pickers.go` (delete `catalogPicker`)
- Delete: `internal/mirror/` (all four files), `internal/device/device_test.go` mirror-coupled cases if any reference removed methods (adjust, don't delete the file wholesale)

**Interfaces:**
- Consumes: `afc.Conn`, `afc.File`, `afc.Vend` (Tasks 2–3).
- Produces (used by Tasks 5, 8):

```go
package afcfs

type FileInfo struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

type File interface {
	io.Reader
	io.Writer
	io.Closer
	Seek(offset int64, whence int) (int64, error)
	Truncate(size int64) error
}

// FS is the device filesystem surface the rest of lrmount depends on.
// Paths use "/" separators relative to the AFC root, no leading slash.
type FS interface {
	List(p string) ([]string, error)
	Stat(p string) (FileInfo, error)
	MkDir(p string) error
	Remove(p string) error // non-recursive; fails on non-empty dirs
	Rename(from, to string) error
	SetMtime(p string, t time.Time) error
	// OpenFile takes os.O_* flags. Mirroring AFC semantics, any writable
	// mode creates a missing file.
	OpenFile(p string, flag int) (File, error)
	DeviceInfo() (total, free uint64, err error)
}

func Wrap(c *afc.Conn) FS
func NewMemFS() *MemFS // MemFS implements FS; helpers AddDir(p), AddFile(p string, size int64), Has(p) bool, Contents(p string) ([]byte, bool)
```

- [ ] **Step 1: Write the failing MemFS test**

Replace `internal/afcfs/memfs_test.go` entirely:

```go
package afcfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"
)

func TestMemFSListStat(t *testing.T) {
	m := NewMemFS()
	m.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 3)
	m.AddDir("Documents/empty")
	names, err := m.List("Documents")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "cat" || names[1] != "empty" {
		t.Fatalf("names = %v", names)
	}
	fi, err := m.Stat("Documents/cat/settings-acr/userStyles/a.xmp")
	if err != nil || fi.IsDir || fi.Size != 3 || fi.Name != "a.xmp" {
		t.Fatalf("fi = %+v err = %v", fi, err)
	}
	if _, err := m.Stat("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestMemFSOpenWriteReadBack(t *testing.T) {
	m := NewMemFS()
	m.AddDir("d")
	h, err := m.OpenFile("d/f.txt", os.O_RDWR|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write([]byte("WORLD")); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	data, ok := m.Contents("d/f.txt")
	if !ok || string(data) != "hello WORLD" {
		t.Fatalf("contents = %q %v", data, ok)
	}
	// read-only open of a missing file fails
	if _, err := m.OpenFile("d/none", os.O_RDONLY); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestMemFSTruncateAndSeekEnd(t *testing.T) {
	m := NewMemFS()
	m.AddFile("f", 0)
	h, _ := m.OpenFile("f", os.O_RDWR)
	_, _ = h.Write([]byte("0123456789"))
	if err := h.Truncate(4); err != nil {
		t.Fatal(err)
	}
	if pos, err := h.Seek(0, io.SeekEnd); err != nil || pos != 4 {
		t.Fatalf("seek end = %d %v", pos, err)
	}
	_ = h.Close()
}

func TestMemFSRemoveSemantics(t *testing.T) {
	m := NewMemFS()
	m.AddFile("d/sub/f", 1)
	if err := m.Remove("d/sub"); err == nil {
		t.Fatal("want error removing non-empty dir")
	}
	if err := m.Remove("d/sub/f"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove("d/sub"); err != nil {
		t.Fatal(err)
	}
	if m.Has("d/sub") {
		t.Fatal("dir still present")
	}
}

func TestMemFSRenameFileAndDir(t *testing.T) {
	m := NewMemFS()
	m.AddFile("a/f", 2)
	if err := m.Rename("a/f", "a/g"); err != nil {
		t.Fatal(err)
	}
	if m.Has("a/f") || !m.Has("a/g") {
		t.Fatal("file rename failed")
	}
	m.AddFile("dir/x/y", 1)
	if err := m.Rename("dir", "dir2"); err != nil {
		t.Fatal(err)
	}
	if !m.Has("dir2/x/y") || m.Has("dir/x/y") {
		t.Fatal("dir rename did not move subtree")
	}
}

func TestMemFSSetMtime(t *testing.T) {
	m := NewMemFS()
	m.AddFile("f", 0)
	want := time.Unix(0, 12345)
	if err := m.SetMtime("f", want); err != nil {
		t.Fatal(err)
	}
	fi, _ := m.Stat("f")
	if !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v", fi.ModTime)
	}
}

func TestMemFSDeviceInfo(t *testing.T) {
	m := NewMemFS()
	total, free, err := m.DeviceInfo()
	if err != nil || total == 0 || free == 0 {
		t.Fatalf("device info = %d %d %v", total, free, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/afcfs/ -v`
Expected: FAIL (compile errors — new methods missing).

- [ ] **Step 3: Rewrite `internal/afcfs/afcfs.go`**

```go
// Package afcfs is the single boundary between lrmount logic and the AFC
// protocol client. Logic depends only on the FS interface so it can be
// tested with MemFS.
package afcfs

import (
	"io"
	"os"
	"time"

	"github.com/davidliu/lrpush/internal/afc"
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

func (f *clientFS) MkDir(p string) error                    { return f.c.MkDir(p) }
func (f *clientFS) Remove(p string) error                   { return f.c.Remove(p) }
func (f *clientFS) Rename(from, to string) error            { return f.c.Rename(from, to) }
func (f *clientFS) SetMtime(p string, t time.Time) error    { return f.c.SetMtime(p, t) }

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
```

- [ ] **Step 4: Rewrite `internal/afcfs/memfs.go`**

```go
package afcfs

import (
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemFS is an in-memory FS for tests. Paths use "/" separators, no leading slash.
type MemFS struct {
	mu    sync.Mutex
	dirs  map[string]bool
	files map[string]*memFile
}

type memFile struct {
	data  []byte
	mtime time.Time
}

func NewMemFS() *MemFS {
	return &MemFS{dirs: map[string]bool{}, files: map[string]*memFile{}}
}

func clean(p string) string { return strings.Trim(p, "/") }

func parent(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

func notExist(op, p string) error {
	return &fs.PathError{Op: op, Path: p, Err: fs.ErrNotExist}
}

// AddDir registers a directory and all its parents.
func (m *MemFS) AddDir(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addDirLocked(clean(p))
}

func (m *MemFS) addDirLocked(p string) {
	for p != "" {
		m.dirs[p] = true
		p = parent(p)
	}
}

// AddFile registers a file (and its parent dirs) with the given size.
func (m *MemFS) AddFile(p string, size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	m.files[c] = &memFile{data: make([]byte, size), mtime: time.Unix(1, 0)}
	if d := parent(c); d != "" {
		m.addDirLocked(d)
	}
}

// Has reports whether a file or dir exists (test helper).
func (m *MemFS) Has(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	_, ok := m.files[c]
	return ok || m.dirs[c]
}

// Contents returns a copy of a file's bytes (test helper).
func (m *MemFS) Contents(p string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[clean(p)]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(f.data))
	copy(out, f.data)
	return out, true
}

func (m *MemFS) List(p string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = clean(p)
	if p != "" && !m.dirs[p] {
		return nil, notExist("list", p)
	}
	seen := map[string]bool{}
	var out []string
	collect := func(full string) {
		rest := full
		if p != "" {
			rest = strings.TrimPrefix(full, p+"/")
		}
		if rest == "" || strings.Contains(rest, "/") {
			return
		}
		if !seen[rest] {
			seen[rest] = true
			out = append(out, rest)
		}
	}
	for d := range m.dirs {
		if p == "" || strings.HasPrefix(d, p+"/") {
			collect(d)
		}
	}
	for f := range m.files {
		if p == "" || strings.HasPrefix(f, p+"/") {
			collect(f)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (m *MemFS) Stat(p string) (FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = clean(p)
	name := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		name = p[i+1:]
	}
	if p == "" || m.dirs[p] {
		return FileInfo{Name: name, IsDir: true}, nil
	}
	if f, ok := m.files[p]; ok {
		return FileInfo{Name: name, Size: int64(len(f.data)), ModTime: f.mtime}, nil
	}
	return FileInfo{}, notExist("stat", p)
}

func (m *MemFS) MkDir(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	if _, ok := m.files[c]; ok {
		return &fs.PathError{Op: "mkdir", Path: c, Err: fs.ErrExist}
	}
	m.addDirLocked(c)
	return nil
}

func (m *MemFS) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	if _, ok := m.files[c]; ok {
		delete(m.files, c)
		return nil
	}
	if m.dirs[c] {
		for d := range m.dirs {
			if strings.HasPrefix(d, c+"/") {
				return &fs.PathError{Op: "remove", Path: c, Err: fs.ErrInvalid}
			}
		}
		for f := range m.files {
			if strings.HasPrefix(f, c+"/") {
				return &fs.PathError{Op: "remove", Path: c, Err: fs.ErrInvalid}
			}
		}
		delete(m.dirs, c)
		return nil
	}
	return notExist("remove", c)
}

func (m *MemFS) Rename(from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	from, to = clean(from), clean(to)
	if f, ok := m.files[from]; ok {
		delete(m.files, from)
		m.files[to] = f
		if d := parent(to); d != "" {
			m.addDirLocked(d)
		}
		return nil
	}
	if m.dirs[from] {
		moved := map[string]*memFile{}
		for k, v := range m.files {
			if strings.HasPrefix(k, from+"/") {
				moved[to+strings.TrimPrefix(k, from)] = v
				delete(m.files, k)
			}
		}
		for k, v := range moved {
			m.files[k] = v
		}
		movedDirs := []string{}
		for d := range m.dirs {
			if strings.HasPrefix(d, from+"/") {
				movedDirs = append(movedDirs, d)
			}
		}
		for _, d := range movedDirs {
			delete(m.dirs, d)
			m.dirs[to+strings.TrimPrefix(d, from)] = true
		}
		delete(m.dirs, from)
		m.addDirLocked(to)
		return nil
	}
	return notExist("rename", from)
}

func (m *MemFS) SetMtime(p string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	if f, ok := m.files[c]; ok {
		f.mtime = t
		return nil
	}
	if m.dirs[c] {
		return nil // AFC sets dir times too; MemFS doesn't track them
	}
	return notExist("chtimes", c)
}

func (m *MemFS) OpenFile(p string, flag int) (File, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := clean(p)
	if m.dirs[c] {
		return nil, &fs.PathError{Op: "open", Path: c, Err: fs.ErrInvalid}
	}
	writable := flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE) != 0
	f, ok := m.files[c]
	if !ok {
		if !writable {
			return nil, notExist("open", c)
		}
		f = &memFile{mtime: time.Now()}
		m.files[c] = f
		if d := parent(c); d != "" {
			m.addDirLocked(d)
		}
	} else if flag&os.O_TRUNC != 0 {
		f.data = nil
		f.mtime = time.Now()
	}
	h := &memHandle{fs: m, f: f}
	if flag&os.O_APPEND != 0 {
		h.pos = int64(len(f.data))
	}
	return h, nil
}

func (m *MemFS) DeviceInfo() (uint64, uint64, error) {
	return 64 << 30, 32 << 30, nil
}

type memHandle struct {
	fs  *MemFS
	f   *memFile
	pos int64
}

func (h *memHandle) Read(p []byte) (int, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	if h.pos >= int64(len(h.f.data)) {
		return 0, io.EOF
	}
	n := copy(p, h.f.data[h.pos:])
	h.pos += int64(n)
	return n, nil
}

func (h *memHandle) Write(p []byte) (int, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	end := h.pos + int64(len(p))
	if int64(len(h.f.data)) < end {
		nd := make([]byte, end)
		copy(nd, h.f.data)
		h.f.data = nd
	}
	copy(h.f.data[h.pos:end], p)
	h.pos = end
	h.f.mtime = time.Now()
	return len(p), nil
}

func (h *memHandle) Seek(offset int64, whence int) (int64, error) {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	switch whence {
	case io.SeekStart:
		h.pos = offset
	case io.SeekCurrent:
		h.pos += offset
	case io.SeekEnd:
		h.pos = int64(len(h.f.data)) + offset
	}
	if h.pos < 0 {
		return 0, &fs.PathError{Op: "seek", Path: "", Err: fs.ErrInvalid}
	}
	return h.pos, nil
}

func (h *memHandle) Truncate(size int64) error {
	h.fs.mu.Lock()
	defer h.fs.mu.Unlock()
	if int64(len(h.f.data)) > size {
		h.f.data = h.f.data[:size]
	} else {
		nd := make([]byte, size)
		copy(nd, h.f.data)
		h.f.data = nd
	}
	h.f.mtime = time.Now()
	return nil
}

func (h *memHandle) Close() error { return nil }
```

- [ ] **Step 5: Run afcfs tests**

Run: `go test ./internal/afcfs/ -v`
Expected: PASS.

- [ ] **Step 6: Update dependents so `go build ./...` is green**

1. `internal/device/device.go`: delete the go-ios `afc` import and local `vend`/`openHouseArrest` plist code; use the new package:

```go
import (
	"fmt"

	"github.com/danielpaulus/go-ios/ios"

	"github.com/davidliu/lrpush/internal/afc"
	"github.com/davidliu/lrpush/internal/afcfs"
)

// openHouseArrest opens the house_arrest service and vends bundleID's
// container, trying each vend command until one succeeds.
func openHouseArrest(device ios.DeviceEntry, bundleID string) (*afc.Conn, error) {
	var lastErr error
	for _, cmd := range vendCommands {
		conn, err := afc.Vend(device, bundleID, cmd)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("house_arrest vend for %s failed: %w", bundleID, lastErr)
}
```

Delete the `vend()` function and the `houseArrestService` constant (both now live in `internal/afc`). In `Connect` and `DetectSessions`, keep `FS: afcfs.Wrap(client)` / `closer: client.Close` — the types line up with `*afc.Conn`.

2. Delete `internal/mirror/` entirely: `git rm -r internal/mirror`.
3. `internal/locate/locate.go`: delete `SelectCatalog` (and its error paths); `locate_test.go`: delete the `SelectCatalog` test functions. `DocumentsRoot`/`FindCatalogs` keep compiling — they only use `List`/`Stat`, whose signatures are unchanged.
4. `cmd/lrpush/pickers.go`: delete `catalogPicker` and the now-unused `locate` import.
5. `cmd/lrpush/root.go`: interim `run()` — replace steps 3–7 with:

```go
	// 3. Per-app: report what would be mounted (mount flow lands next).
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		cands, err := locate.FindCatalogs(s.FS, docs)
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		fmt.Printf("[%s] Documents root %q, %d catalog(s)\n", s.BundleID, docs, len(cands))
	}
	fmt.Println("NFS mount flow lands in a later commit on this branch.")
	return nil
```

Delete `appMirror`, `prefixLogger`, the mirror import, the signal/watcher block, and the `warningBanner()` call (banner.go + banner_test.go stay untouched until Task 8).

- [ ] **Step 7: Verify everything**

Run: `go build ./... && go test ./...`
Expected: all packages build; afc, afcfs, locate, device, cmd tests pass; no references to `internal/mirror` remain (`grep -r "internal/mirror" --include='*.go' .` is empty).

- [ ] **Step 8: Commit**

```bash
gofmt -w . && go vet ./...
git add -A
git commit -m "feat(afcfs): random-access FS interface; drop mirror flow"
```

---

### Task 5: internal/nfsgate — billy.Filesystem adapter

**Files:**
- Create: `internal/nfsgate/billyfs.go`
- Test: `internal/nfsgate/billyfs_test.go`

**Interfaces:**
- Consumes: `afcfs.FS`, `afcfs.File`, `afcfs.FileInfo`; `github.com/go-git/go-billy/v5` (`billy.Filesystem`, `billy.File`, `billy.Change`, `billy.Capability`, `billy.ErrNotSupported`).
- Produces (used by Task 6): `func NewBillyFS(fs afcfs.FS, root string) *BillyFS` where `*BillyFS` implements `billy.Filesystem`, `billy.Change`, and `Capabilities() billy.Capability` (no LockCapability). Files implement `billy.File` with per-file mutex; `ReadAt` = Seek+ReadFull.

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/go-git/go-billy/v5@latest
```

- [ ] **Step 2: Write the failing test**

```go
// internal/nfsgate/billyfs_test.go
package nfsgate

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func newFS(t *testing.T) (*afcfs.MemFS, *BillyFS) {
	t.Helper()
	m := afcfs.NewMemFS()
	m.AddFile("Documents/cat/settings-acr/userStyles/a.xmp", 4)
	return m, NewBillyFS(m, "Documents")
}

func TestResolveRootsAndGuards(t *testing.T) {
	m, b := newFS(t)
	// paths are relative to the Documents root
	fi, err := b.Stat("/cat/settings-acr/userStyles/a.xmp")
	if err != nil || fi.Size() != 4 || fi.IsDir() {
		t.Fatalf("fi = %+v err = %v", fi, err)
	}
	// ".." cannot escape the root
	if _, err := b.Stat("../secret"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist for escaped path, got %v", err)
	}
	_ = m
}

func TestStatRoot(t *testing.T) {
	_, b := newFS(t)
	fi, err := b.Stat("/")
	if err != nil || !fi.IsDir() {
		t.Fatalf("root stat = %+v %v", fi, err)
	}
}

func TestCreateWriteReadAt(t *testing.T) {
	m, b := newFS(t)
	f, err := b.Create("/new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("hello world")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if n, err := f.ReadAt(buf, 6); err != nil && err != io.EOF {
		t.Fatal(err)
	} else if n != 5 || string(buf) != "world" {
		t.Fatalf("ReadAt = %d %q", n, buf)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if data, ok := m.Contents("Documents/new.txt"); !ok || string(data) != "hello world" {
		t.Fatalf("device contents = %q %v", data, ok)
	}
}

func TestOpenFileWriteOnlyPreservesContent(t *testing.T) {
	m, b := newFS(t)
	// go-nfs WRITE opens O_RDWR; SETATTR-size opens O_WRONLY|O_EXCL. Neither
	// may truncate.
	f, err := b.OpenFile("/cat/settings-acr/userStyles/a.xmp", os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if data, _ := m.Contents("Documents/cat/settings-acr/userStyles/a.xmp"); len(data) != 4 {
		t.Fatalf("content truncated to %d bytes", len(data))
	}
}

func TestReadDirSortedWithInfo(t *testing.T) {
	m, b := newFS(t)
	m.AddFile("Documents/b.txt", 1)
	m.AddFile("Documents/a.txt", 2)
	infos, err := b.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 || infos[0].Name() != "a.txt" || infos[1].Name() != "b.txt" || !infos[2].IsDir() {
		names := []string{}
		for _, i := range infos {
			names = append(names, i.Name())
		}
		t.Fatalf("infos = %v", names)
	}
}

func TestMkdirAllRenameRemove(t *testing.T) {
	m, b := newFS(t)
	if err := b.MkdirAll("/x/y/z", 0o755); err != nil {
		t.Fatal(err)
	}
	if !m.Has("Documents/x/y/z") {
		t.Fatal("mkdirall did not create tree")
	}
	if err := b.Rename("/cat", "/cat2"); err != nil {
		t.Fatal(err)
	}
	if !m.Has("Documents/cat2/settings-acr/userStyles/a.xmp") {
		t.Fatal("rename lost subtree")
	}
	if err := b.Remove("/cat2"); err == nil {
		t.Fatal("want error removing non-empty dir")
	}
	if err := b.Remove("/x/y/z"); err != nil {
		t.Fatal(err)
	}
}

func TestChangeChtimes(t *testing.T) {
	m, b := newFS(t)
	want := time.Unix(0, 999)
	if err := b.Chtimes("/cat/settings-acr/userStyles/a.xmp", time.Now(), want); err != nil {
		t.Fatal(err)
	}
	fi, _ := m.Stat("Documents/cat/settings-acr/userStyles/a.xmp")
	if !fi.ModTime.Equal(want) {
		t.Fatalf("mtime = %v", fi.ModTime)
	}
	// Chmod/Chown are silent no-ops: AFC has no permission model, and
	// failing them would break Finder copies.
	if err := b.Chmod("/anything", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.Chown("/anything", 1, 1); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedSurface(t *testing.T) {
	_, b := newFS(t)
	if err := b.Symlink("a", "b"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("Symlink err = %v", err)
	}
	if _, err := b.Readlink("a"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("Readlink err = %v", err)
	}
	if _, err := b.TempFile("", "x"); !errors.Is(err, billy.ErrNotSupported) {
		t.Fatalf("TempFile err = %v", err)
	}
}

func TestCapabilitiesExcludeLock(t *testing.T) {
	_, b := newFS(t)
	caps := b.Capabilities()
	if caps&billy.LockCapability != 0 {
		t.Fatal("must not advertise lock capability")
	}
	for _, c := range []billy.Capability{billy.WriteCapability, billy.ReadCapability, billy.SeekCapability, billy.TruncateCapability} {
		if caps&c == 0 {
			t.Fatalf("missing capability %b", c)
		}
	}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/nfsgate/ -v`
Expected: FAIL — `undefined: NewBillyFS`.

- [ ] **Step 4: Write the implementation**

```go
// internal/nfsgate/billyfs.go
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

	"github.com/davidliu/lrpush/internal/afcfs"
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

func (b *BillyFS) Symlink(string, string) (err error)      { return billy.ErrNotSupported }
func (b *BillyFS) Readlink(string) (string, error)         { return "", billy.ErrNotSupported }
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
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/nfsgate/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
gofmt -w . && go vet ./... && go mod tidy
git add -A
git commit -m "feat(nfsgate): billy.Filesystem adapter over afcfs"
```

---

### Task 6: internal/nfsgate — NFS server + end-to-end write-through test

**Files:**
- Create: `internal/nfsgate/server.go`
- Test: `internal/nfsgate/server_test.go`

**Interfaces:**
- Consumes: `NewBillyFS` (Task 5), `afcfs.FS`; `willscott/go-nfs` (`nfs.Serve`, `nfs.Handler`, `nfs.FSStat`), helpers (`NewNullAuthHandler`, `NewCachingHandler`).
- Produces (used by Task 8): `func Serve(l net.Listener, fs afcfs.FS, root string) error` — serves both MOUNT and NFSv3 on one listener until it closes.

- [ ] **Step 1: Add dependencies**

```bash
go get github.com/willscott/go-nfs@latest
go get github.com/willscott/go-nfs-client@latest
```

- [ ] **Step 2: Write the failing test**

The client library is the one go-nfs uses in its own tests. A write acknowledged to the NFS client must already be visible in the backing store — that is the write-through property Finder eject depends on.

```go
// internal/nfsgate/server_test.go
package nfsgate

import (
	"bytes"
	"net"
	"testing"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	rpc "github.com/willscott/go-nfs-client/nfs/rpc"

	"github.com/davidliu/lrpush/internal/afcfs"
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
	fsstat, err := target.FSStat()
	if err != nil {
		t.Fatal(err)
	}
	if fsstat.TotalSize == 0 || fsstat.FreeSize == 0 {
		t.Fatalf("fsstat = %+v; Finder refuses copies onto 0-byte volumes", fsstat)
	}
}
```

Note: if `target.FSStat()` / `target.Mkdir` signatures differ in the client lib version, check `$(go env GOMODCACHE)/github.com/willscott/go-nfs-client*/nfs/target.go` and adjust the calls — the assertions stay the same.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./internal/nfsgate/ -run 'TestWrite|TestRead|TestFSStat' -v`
Expected: FAIL — `undefined: Serve`.

- [ ] **Step 4: Write the implementation**

```go
// internal/nfsgate/server.go
package nfsgate

import (
	"context"
	"net"

	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"github.com/davidliu/lrpush/internal/afcfs"
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
	bfs := NewBillyFS(fs, root)
	h := &handler{Handler: nfshelper.NewNullAuthHandler(bfs), fs: fs}
	return nfs.Serve(l, nfshelper.NewCachingHandler(h, handleCacheSize))
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./internal/nfsgate/ -v`
Expected: PASS (all Task 5 + Task 6 tests).

- [ ] **Step 6: Commit**

```bash
gofmt -w . && go vet ./... && go mod tidy
git add -A
git commit -m "feat(nfsgate): NFS server with device-backed FSStat; e2e write-through test"
```

---

### Task 7: internal/mountctl — mount, eject detection, unmount

**Files:**
- Create: `internal/mountctl/mountctl.go`
- Test: `internal/mountctl/mountctl_test.go`

**Interfaces:**
- Consumes: `golang.org/x/sys/unix` (add as direct dep: `go get golang.org/x/sys@latest`).
- Produces (used by Task 8):
  - `func MountNFS(mountpoint string, port int) error`
  - `func IsMounted(mountpoint string) bool`
  - `func WaitUnmount(ctx context.Context, mountpoint string) error` — nil on eject, ctx.Err() on cancel
  - `func Unmount(mountpoint string, force bool) error`
  - `func Cleanup(mountpoint string)`

- [ ] **Step 1: Write the failing test**

```go
// internal/mountctl/mountctl_test.go
//go:build darwin

package mountctl

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fakeStatfs(fstype string, fail bool) func(string, *unix.Statfs_t) error {
	return func(_ string, st *unix.Statfs_t) error {
		if fail {
			return errors.New("no such file")
		}
		copy(st.Fstypename[:], fstype)
		return nil
	}
}

func TestIsMounted(t *testing.T) {
	orig := statfsFn
	defer func() { statfsFn = orig }()

	statfsFn = fakeStatfs("nfs", false)
	if !IsMounted("/Volumes/x") {
		t.Fatal("want mounted for nfs fstype")
	}
	statfsFn = fakeStatfs("apfs", false)
	if IsMounted("/Volumes/x") {
		t.Fatal("want unmounted for apfs fstype")
	}
	statfsFn = fakeStatfs("", true)
	if IsMounted("/Volumes/x") {
		t.Fatal("want unmounted on statfs error")
	}
}

func TestWaitUnmountReturnsOnEject(t *testing.T) {
	orig, origPoll := statfsFn, pollInterval
	defer func() { statfsFn, pollInterval = orig, origPoll }()
	pollInterval = 5 * time.Millisecond

	var calls atomic.Int32
	statfsFn = func(_ string, st *unix.Statfs_t) error {
		if calls.Add(1) < 3 {
			copy(st.Fstypename[:], "nfs")
		} else {
			copy(st.Fstypename[:], "apfs")
		}
		return nil
	}
	if err := WaitUnmount(context.Background(), "/Volumes/x"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitUnmountHonorsContext(t *testing.T) {
	orig, origPoll := statfsFn, pollInterval
	defer func() { statfsFn, pollInterval = orig, origPoll }()
	pollInterval = 5 * time.Millisecond
	statfsFn = fakeStatfs("nfs", false)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := WaitUnmount(ctx, "/Volumes/x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/mountctl/ -v`
Expected: FAIL — package missing.

- [ ] **Step 3: Write the implementation**

```go
// internal/mountctl/mountctl.go
//go:build darwin

// Package mountctl drives macOS's built-in NFS client: mount, eject
// detection, unmount. Nothing here talks to the device.
package mountctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// swappable in tests
var (
	statfsFn     = unix.Statfs
	pollInterval = time.Second
)

// MountNFS mounts the localhost NFS server listening on port at mountpoint.
// nolocks: go-nfs implements no NLM lock protocol. Default hard-mount
// semantics are kept deliberately: transient stalls retry instead of
// dropping writes.
func MountNFS(mountpoint string, port int) error {
	opts := fmt.Sprintf("port=%d,mountport=%d,tcp,vers=3,nolocks", port, port)
	out, err := exec.Command("/sbin/mount", "-t", "nfs", "-o", opts, "localhost:/", mountpoint).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount %s: %w: %s", mountpoint, err, out)
	}
	return nil
}

// IsMounted reports whether mountpoint currently carries an NFS filesystem.
// After an eject, statfs sees the parent (apfs) filesystem instead.
func IsMounted(mountpoint string) bool {
	var st unix.Statfs_t
	if err := statfsFn(mountpoint, &st); err != nil {
		return false
	}
	return unix.ByteSliceToString(st.Fstypename[:]) == "nfs"
}

// WaitUnmount blocks until mountpoint stops being an NFS mount (Finder
// eject) or ctx is cancelled. The macOS NFS client flushes all dirty pages
// before an unmount can succeed, so returning nil here means every write
// has been acknowledged by the AFC layer.
func WaitUnmount(ctx context.Context, mountpoint string) error {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if !IsMounted(mountpoint) {
				return nil
			}
		}
	}
}

// Unmount unmounts via diskutil, which performs the same flush as a Finder
// eject. force escalates a volume that stays busy.
func Unmount(mountpoint string, force bool) error {
	args := []string{"unmount"}
	if force {
		args = append(args, "force")
	}
	args = append(args, mountpoint)
	out, err := exec.Command("/usr/sbin/diskutil", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("diskutil unmount %s: %w: %s", mountpoint, err, out)
	}
	return nil
}

// Cleanup removes an empty leftover mountpoint directory; errors are
// ignored (a non-empty or already-removed dir is fine to leave).
func Cleanup(mountpoint string) { _ = os.Remove(mountpoint) }
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/mountctl/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -w . && go vet ./... && go mod tidy
git add -A
git commit -m "feat(mountctl): NFS mount, eject watch, diskutil unmount"
```

---

### Task 8: cmd — full mount orchestration

**Files:**
- Create: `cmd/lrpush/volume.go`
- Test: `cmd/lrpush/volume_test.go`
- Modify: `cmd/lrpush/root.go`, `cmd/lrpush/banner.go`, `cmd/lrpush/banner_test.go`

**Interfaces:**
- Consumes: everything above (`device.DetectSessions`, `locate.DocumentsRoot/FindCatalogs`, `nfsgate.Serve`, `mountctl.*`).
- Produces: the `lrpush` binary's final behavior (renamed in Task 9).

- [ ] **Step 1: Write the failing test for the pure helpers**

```go
// cmd/lrpush/volume_test.go
package main

import (
	"strings"
	"testing"
)

func TestVolumeName(t *testing.T) {
	if got := volumeName("David's iPhone", "com.adobe.lrmobilephone", false); got != "David's iPhone Lightroom" {
		t.Fatalf("got %q", got)
	}
	if got := volumeName("iPad", "com.adobe.lrmobile", true); got != "iPad Lightroom lrmobile" {
		t.Fatalf("got %q", got)
	}
	// path-hostile characters are replaced
	if got := volumeName("we/ird:name", "b", false); strings.ContainsAny(got, "/:") {
		t.Fatalf("got %q", got)
	}
}

func TestHintPath(t *testing.T) {
	got := hintPath("/Volumes/iPad Lightroom", "Documents", "Documents/cat/settings-acr/userStyles")
	if got != "/Volumes/iPad Lightroom/cat/settings-acr/userStyles" {
		t.Fatalf("got %q", got)
	}
	// root "" means the AFC root already is Documents
	got = hintPath("/Volumes/x", "", "cat/settings-acr/userStyles")
	if got != "/Volumes/x/cat/settings-acr/userStyles" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./cmd/lrpush/ -run 'TestVolumeName|TestHintPath' -v`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Write `cmd/lrpush/volume.go`**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidliu/lrpush/internal/mountctl"
)

// volumeName builds the Finder volume name for one (device, app) pair.
// multi appends the bundle id's last segment when a device has more than
// one Lightroom app.
func volumeName(deviceName, bundleID string, multi bool) string {
	clean := strings.Map(func(r rune) rune {
		if r == '/' || r == ':' {
			return '-'
		}
		return r
	}, deviceName)
	name := clean + " Lightroom"
	if multi {
		parts := strings.Split(bundleID, ".")
		name += " " + parts[len(parts)-1]
	}
	return name
}

// hintPath maps a device-side userStyles path onto its mounted location.
func hintPath(mountpoint, root, devicePath string) string {
	rel := strings.Trim(strings.TrimPrefix(devicePath, strings.Trim(root, "/")), "/")
	return filepath.Join(mountpoint, rel)
}

// pickMountpoint creates and returns a usable mountpoint dir for name,
// preferring /Volumes and falling back to ~/lrmount-volumes. An existing
// empty dir (leftover from a crash) is reused; live mounts and non-empty
// dirs get a numeric suffix.
func pickMountpoint(name string) (string, error) {
	home, _ := os.UserHomeDir()
	fallback := filepath.Join(home, "lrmount-volumes")
	for _, base := range []string{"/Volumes", fallback} {
		if base == fallback {
			if err := os.MkdirAll(base, 0o755); err != nil {
				continue
			}
		}
		for i := 1; i <= 9; i++ {
			n := name
			if i > 1 {
				n = fmt.Sprintf("%s %d", name, i)
			}
			mp := filepath.Join(base, n)
			if mountctl.IsMounted(mp) {
				continue
			}
			if err := os.Mkdir(mp, 0o755); err == nil {
				return mp, nil
			}
			if entries, err := os.ReadDir(mp); err == nil && len(entries) == 0 {
				return mp, nil
			}
		}
	}
	return "", fmt.Errorf("no usable mountpoint for %q", name)
}
```

- [ ] **Step 4: Run helper tests**

Run: `go test ./cmd/lrpush/ -run 'TestVolumeName|TestHintPath' -v`
Expected: PASS.

- [ ] **Step 5: Rewrite `cmd/lrpush/banner.go` and its test**

```go
package main

func warningBanner() string {
	return "" +
		"========================== IMPORTANT ==========================\n" +
		" Fully close Lightroom on the device while volumes are mounted\n" +
		" (swipe it away in the app switcher). Reopen it after ejecting.\n" +
		"\n" +
		" Edits are written straight to the device. Eject a volume in\n" +
		" Finder when you are done; lrmount exits after the last eject.\n" +
		"\n" +
		" Note: presets written this way may appear only on this device\n" +
		" and may NOT sync to Creative Cloud.\n" +
		"===============================================================\n"
}
```

Update `banner_test.go` to assert the new key phrases ("close Lightroom", "Eject", "Creative Cloud") — keep its existing structure, swap the expected substrings.

- [ ] **Step 6: Rewrite `cmd/lrpush/root.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/locate"
	"github.com/davidliu/lrpush/internal/mountctl"
	"github.com/davidliu/lrpush/internal/nfsgate"
)

// lightroomBundleIDs are probed in order; the iPhone app comes first.
var lightroomBundleIDs = []string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}

var rootCmd = &cobra.Command{
	Use:           "lrmount",
	Short:         "Mount each iPhone/iPad Lightroom app's Documents as an ejectable Finder volume",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          func(cmd *cobra.Command, args []string) error { return run() },
}

func Execute() error { return rootCmd.Execute() }

type volume struct {
	sess       *device.Session
	name       string
	root       string // Documents root on the device
	hints      []string
	mountpoint string
	ln         net.Listener
}

func run() error {
	// 1. Pick device.
	infos, err := device.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return fmt.Errorf("no USB device found; connect and trust your device")
	}
	chosen := infos[0]
	if len(infos) > 1 {
		labels := make([]string, len(infos))
		for i, d := range infos {
			labels[i] = fmt.Sprintf("%s  (%s, iOS %s, %s)", d.Name, d.ProductType, d.Version, d.UDID)
		}
		idx, err := pickIndex("Select a device", labels)
		if err != nil {
			return err
		}
		chosen = infos[idx]
	}

	// 2. Open a session per installed Lightroom app.
	sessions, err := device.DetectSessions(chosen.UDID, lightroomBundleIDs)
	if err != nil {
		return err
	}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()
	multi := len(sessions) > 1

	// 3. Build volume descriptions.
	var vols []*volume
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] skipped: %v\n", s.BundleID, err)
			continue
		}
		v := &volume{sess: s, root: docs, name: volumeName(chosen.Name, s.BundleID, multi)}
		if cands, err := locate.FindCatalogs(s.FS, docs); err == nil {
			for _, c := range cands {
				v.hints = append(v.hints, c.UserStyles)
			}
		}
		vols = append(vols, v)
	}
	if len(vols) == 0 {
		return fmt.Errorf("no Lightroom app with a usable Documents folder found")
	}

	fmt.Print(warningBanner())

	// 4. Serve + mount every volume; failures skip that volume only.
	var mounted []*volume
	for _, v := range vols {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] listen: %v\n", v.name, err)
			continue
		}
		v.ln = ln
		go func(v *volume) {
			if err := nfsgate.Serve(v.ln, v.sess.FS, v.root); err != nil && !errors.Is(err, net.ErrClosed) {
				fmt.Fprintf(os.Stderr, "[%s] nfs server: %v\n", v.name, err)
			}
		}(v)
		mp, err := pickMountpoint(v.name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", v.name, err)
			ln.Close()
			continue
		}
		port := ln.Addr().(*net.TCPAddr).Port
		if err := mountctl.MountNFS(mp, port); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] %v\n", v.name, err)
			ln.Close()
			mountctl.Cleanup(mp)
			continue
		}
		v.mountpoint = mp
		mounted = append(mounted, v)
		fmt.Printf("mounted  %s\n", mp)
		for _, h := range v.hints {
			fmt.Printf("  presets: %s\n", hintPath(mp, v.root, h))
		}
	}
	if len(mounted) == 0 {
		return fmt.Errorf("all volumes failed to mount")
	}
	fmt.Println("\nEject the volume(s) in Finder when done, or press Ctrl-C.")

	// 5. Wait for ejects; Ctrl-C unmounts what is left.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	for _, v := range mounted {
		wg.Add(1)
		go func(v *volume) {
			defer wg.Done()
			if err := mountctl.WaitUnmount(ctx, v.mountpoint); err != nil {
				return // Ctrl-C: main unmounts below
			}
			fmt.Printf("ejected  %s\n", v.mountpoint)
			v.ln.Close()
			mountctl.Cleanup(v.mountpoint)
		}(v)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done: // every volume ejected in Finder
	case <-ctx.Done(): // Ctrl-C
		fmt.Println("\nunmounting…")
		force := make(chan os.Signal, 1)
		signal.Notify(force, os.Interrupt)
		defer signal.Stop(force)
		for _, v := range mounted {
			unmountWithRetry(v, force)
		}
		wg.Wait()
	}

	fmt.Println("\nAll volumes ejected. Reopen Lightroom so it rebuilds its preset index.")
	return nil
}

// unmountWithRetry keeps trying a graceful unmount (which flushes like a
// Finder eject); a second Ctrl-C escalates to a forced unmount.
func unmountWithRetry(v *volume, force <-chan os.Signal) {
	defer func() {
		v.ln.Close()
		mountctl.Cleanup(v.mountpoint)
	}()
	for mountctl.IsMounted(v.mountpoint) {
		err := mountctl.Unmount(v.mountpoint, false)
		if err == nil {
			fmt.Printf("ejected  %s\n", v.mountpoint)
			return
		}
		fmt.Fprintf(os.Stderr, "%s is busy — close open files, retrying in 2s (Ctrl-C again to force)\n", v.mountpoint)
		select {
		case <-force:
			if err := mountctl.Unmount(v.mountpoint, true); err != nil {
				fmt.Fprintf(os.Stderr, "force unmount %s: %v\n", v.mountpoint, err)
			}
			return
		case <-time.After(2 * time.Second):
		}
	}
}
```

- [ ] **Step 7: Verify build and tests**

Run: `go build ./... && go test ./...`
Expected: everything green.

- [ ] **Step 8: Manual smoke test on a real device** (needs an iPhone/iPad with Lightroom, USB, trusted)

```bash
make build && ./lrpush
```

Verify, in order: banner prints; `mounted /Volumes/<name> Lightroom` appears; the volume shows in Finder with an eject control; copying an .xmp preset into the printed presets path succeeds; ejecting in Finder prints `ejected …` and (if it was the only volume) the process exits; rerun and Ctrl-C unmounts cleanly. If `mount` fails with a permission error, retry the option string with `,resvport` appended in `mountctl.MountNFS` — some macOS configurations require a reserved source port even for localhost.

- [ ] **Step 9: Commit**

```bash
gofmt -w . && go vet ./...
git add -A
git commit -m "feat(cmd): mount Lightroom Documents as ejectable Finder volumes"
```

---

### Task 9: Rename lrpush → lrmount (binary, module, docs)

**Files:**
- Rename: `cmd/lrpush/` → `cmd/lrmount/`
- Modify: `go.mod` (module path), every `.go` import, `Makefile`, `.gitignore`, `README.md`
- Delete: stale `./lrpush` binary at the repo root

**Interfaces:** none new — pure rename; `rootCmd.Use` is already "lrmount" from Task 8.

- [ ] **Step 1: Rename directory and module**

```bash
git mv cmd/lrpush cmd/lrmount
go mod edit -module github.com/david-zw-liu/lrmount
find . -name '*.go' -not -path './.git/*' -exec sed -i '' 's|github.com/davidliu/lrpush|github.com/david-zw-liu/lrmount|g' {} +
rm -f lrpush
go mod tidy
```

- [ ] **Step 2: Update Makefile**

```makefile
BINARY := lrmount
PKG := ./cmd/lrmount

.PHONY: build test vet fmt clean

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
```

- [ ] **Step 3: Update .gitignore**

```gitignore
# built binary
/lrmount

# SDD controller scratch (ledger, briefs, review packages)
/.superpowers/

# macOS
.DS_Store
```

(The `/sync/` entry goes away with the mirror flow.)

- [ ] **Step 4: Rewrite README.md**

```markdown
# lrmount

Mount an iPhone/iPad Adobe Lightroom app's `Documents/` folder as an
ejectable volume in Finder, over USB — using Apple house_arrest + AFC via
an embedded localhost NFS server and macOS's built-in NFS client. No
jailbreak, no kernel extensions, nothing to install.

## How it works

Running `lrmount`:

1. Picks a connected USB device (auto if one, arrow-key menu if several).
2. Detects every installed Lightroom app (`com.adobe.lrmobilephone`, then
   `com.adobe.lrmobile`) and starts one embedded NFS server per app,
   bridging NFS operations straight to the device over AFC.
3. Mounts each app's `Documents/` at `/Volumes/<device> Lightroom` with
   the built-in macOS NFS client. User presets live under
   `<catalog>/settings-acr/userStyles/` (paths are printed at startup).
4. Waits. Eject a volume in Finder when you are done — macOS flushes every
   pending write to the device before the eject completes. When the last
   volume is ejected (or on Ctrl-C), lrmount exits.

Writes are write-through: every file operation is acknowledged only after
the device has confirmed it. There is no local cache to lose.

## Requirements

- macOS with the device connected via USB and **trusted**.
- Go 1.26+ to build.

## Build

    make build        # produces ./lrmount
    # or: go build -o lrmount ./cmd/lrmount

## Use

    ./lrmount

Pick a device if prompted, then **fully close Lightroom** on the device
(swipe it away in the app switcher) while volumes are mounted. Edit presets
in Finder under the printed paths. Eject in Finder when done, then reopen
Lightroom so it rebuilds its preset index.

### Safety

- Deletions and edits act directly on the device. There are no backups.
- Close Lightroom while volumes are mounted; reopen it after ejecting.
- Finder writes `.DS_Store` / `._*` files onto the device; Lightroom
  ignores them.
- Presets pushed this way may appear only on the device and may not sync
  to Creative Cloud.

## Troubleshooting

**No device found:** connect via USB, unlock, and accept "Trust This
Computer".

**Lightroom not found:** the app must be installed and expose file sharing.

**Mount fails with a permission error:** some configurations require a
reserved source port; see the `resvport` note in `internal/mountctl`.

**Volume shows as full:** the free-space numbers come from the device;
reconnect and rerun if they look stale.
```

- [ ] **Step 5: Verify and commit**

Run: `go build ./... && go test ./... && grep -r "davidliu/lrpush" --include='*.go' .` (last one must be empty)

```bash
gofmt -w . && go vet ./...
git add -A
git commit -m "chore: rename lrpush to lrmount (binary, module path, docs)"
```

---

### Task 10: Folder rename, push, and acceptance

**Files:** none in-repo (environment + verification).

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/nfs-volume
```

(origin already points at `git@github.com:david-zw-liu/lrmount.git`.)

- [ ] **Step 2: Manual acceptance checklist** (real device, human-in-the-loop)

1. Single device, one Lightroom app: mount → edit a preset .xmp in Finder → eject → reopen Lightroom → preset visible.
2. Copy a folder of presets into userStyles via Finder drag-and-drop; verify all files arrive (compare `ls -R` against source).
3. Rename and delete a preset in Finder; verify on-device via re-mount.
4. iPad with both Lightroom apps installed (if available): two volumes, distinct names, independent eject.
5. `lrmount`, then Ctrl-C with no Finder windows open: graceful unmount, clean exit.
6. Open a file on the volume (e.g. `tail -f`), try to eject in Finder: eject refused; close file, eject succeeds.
7. Unplug the USB cable mid-session: I/O error surfaces, `lrmount` force-cleans the stale mountpoint and reports it.
8. Rerun after a `kill -9` of a previous session: stale mountpoint is reused/cleaned without manual intervention.

- [ ] **Step 3: Rename the working folder (LAST — invalidates the running session's paths)**

```bash
mv ~/Desktop/ios-lightroom-presets-importer ~/Desktop/lrmount
```

Note for the human: restart your Claude Code session from `~/Desktop/lrmount` afterwards; the memory directory keyed to the old path will start fresh.

---

## Self-Review Notes

- **Spec coverage:** zero-install (Tasks 6–7 use only built-in mount/diskutil) ✓; whole-Documents scope, no catalog picker (Task 4 removes SelectCatalog, Task 8 prints hints) ✓; replaces mirror (Task 4 deletes it) ✓; lifecycle all-ejected-exit / Ctrl-C / second-Ctrl-C-force (Task 8) ✓; write-through guarantee (go-nfs verified synchronous + Task 6 e2e test asserts it) ✓; FSStat from device (Task 6) ✓; startup isolation per volume, stale-mount cleanup, noise files passthrough (Tasks 7–8, README) ✓; AFC gap seek/rename/mtime/truncate (Tasks 1–2, nanosecond mtime) ✓; rename/migration (Tasks 9–10, origin already updated) ✓.
- **Known deviations from spec:** none in behavior. The spec's "internal/afc copied from go-ios" became a fresh implementation with the same wire format (go-ios's codec is private and carries quirks — missing NUL terminators, unchecked reply opcodes — we deliberately do not copy).
- **Type consistency check:** `afcfs.FS.OpenFile(p string, flag int)` is what both MemFS and clientFS implement and what BillyFS consumes ✓; `nfsgate.Serve(l, fs, root)` matches Task 8's call ✓; `mountctl` function set matches Task 8 usage ✓; `volumeName`/`hintPath`/`pickMountpoint` defined in Task 8 where used ✓.
- **Verify-at-implementation flags:** (a) go-nfs-client `Target` method signatures in Task 6 test (adjust calls, keep assertions); (b) `mount` option string may need `,resvport` on some systems (Task 8 Step 8) — note rclone's nfsmount passes only `port,mountport,tcp` and works without it; (c) AFC `MakeDir` may or may not create parents — `MkdirAll` loops segments so either works; (d) fileids: with `Sys() = nil`, go-nfs falls back to a stable FNV-1a hash of the full path, which is sufficient on macOS — if Finder ever shows flaky/duplicate listings, populate `Sys()` with `*file.FileInfo{Fileid: <stable id>}` the way rclone does (it was required on Linux).
```
