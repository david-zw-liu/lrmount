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
