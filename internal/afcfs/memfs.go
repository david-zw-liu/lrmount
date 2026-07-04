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
