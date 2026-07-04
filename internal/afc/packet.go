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
