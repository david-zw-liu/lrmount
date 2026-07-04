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
	hp := []byte("from\x00to\x00\x00")
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
	hp := le64(3)           // e.g. a file handle
	body := []byte("hello") // bulk payload (file write)
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
