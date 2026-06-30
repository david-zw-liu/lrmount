package afcfs

import (
	"fmt"
	"sort"
	"strings"
)

// MemFS is an in-memory FS for tests. Paths use "/" separators, no leading slash.
type MemFS struct {
	dirs   map[string]bool
	files  map[string]int64   // path -> size
	Pushed map[string]string  // deviceDst -> localSrc (recorded by PushFile)
	Pulled map[string]string  // deviceSrc -> localDst (recorded by Pull)
}

func NewMemFS() *MemFS {
	return &MemFS{
		dirs:   map[string]bool{},
		files:  map[string]int64{},
		Pushed: map[string]string{},
		Pulled: map[string]string{},
	}
}

func clean(p string) string { return strings.Trim(p, "/") }

func parent(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return ""
	}
	return p[:i]
}

// AddDir registers a directory and all its parents.
func (m *MemFS) AddDir(p string) {
	p = clean(p)
	for p != "" {
		m.dirs[p] = true
		p = parent(p)
	}
}

// AddFile registers a file (and its parent dirs) with the given size.
func (m *MemFS) AddFile(p string, size int64) {
	p = clean(p)
	m.files[p] = size
	if d := parent(p); d != "" {
		m.AddDir(d)
	}
}

func (m *MemFS) List(p string) ([]string, error) {
	p = clean(p)
	if p != "" && !m.dirs[p] {
		return nil, fmt.Errorf("not a directory: %s", p)
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
	p = clean(p)
	name := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		name = p[i+1:]
	}
	if m.dirs[p] {
		return FileInfo{Name: name, IsDir: true}, nil
	}
	if size, ok := m.files[p]; ok {
		return FileInfo{Name: name, IsDir: false, Size: size}, nil
	}
	return FileInfo{}, fmt.Errorf("no such path: %s", p)
}

func (m *MemFS) MkDir(p string) error { m.AddDir(p); return nil }

func (m *MemFS) RemoveAll(p string) error {
	p = clean(p)
	delete(m.dirs, p)
	for d := range m.dirs {
		if strings.HasPrefix(d, p+"/") {
			delete(m.dirs, d)
		}
	}
	delete(m.files, p)
	for f := range m.files {
		if strings.HasPrefix(f, p+"/") {
			delete(m.files, f)
		}
	}
	return nil
}

func (m *MemFS) Pull(src, dst string) error {
	m.Pulled[clean(src)] = dst
	return nil
}

func (m *MemFS) PushFile(localSrc, deviceDst string) error {
	m.AddFile(deviceDst, 0)
	m.Pushed[clean(deviceDst)] = localSrc
	return nil
}

// Has reports whether a file or dir exists (test helper).
func (m *MemFS) Has(p string) bool {
	p = clean(p)
	return m.dirs[p] || func() bool { _, ok := m.files[p]; return ok }()
}
