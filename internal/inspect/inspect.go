// Package inspect dumps the app container tree and locates userStyles, pulling
// a sample preset so its real extension/format can be confirmed.
package inspect

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidliu/lrpush/internal/afcfs"
	"github.com/davidliu/lrpush/internal/locate"
)

// Options configures an inspect run.
type Options struct {
	PathPrefix  string
	Samples     int
	SampleDir   string
	CatalogFlag string
	Picker      func([]locate.Catalog) (int, error)
}

// TreeLines returns an indented directory tree rooted at root.
func TreeLines(fs afcfs.FS, root string, maxDepth int) ([]string, error) {
	var lines []string
	var walk func(p string, depth int) error
	walk = func(p string, depth int) error {
		if depth > maxDepth {
			return nil
		}
		entries, err := fs.List(p)
		if err != nil {
			return nil // unreadable dir: skip quietly
		}
		for _, name := range entries {
			child := strings.Trim(p+"/"+name, "/")
			fi, err := fs.Stat(child)
			indent := strings.Repeat("  ", depth)
			if err == nil && fi.IsDir {
				lines = append(lines, fmt.Sprintf("%s%s/", indent, name))
				if err := walk(child, depth+1); err != nil {
					return err
				}
			} else {
				lines = append(lines, fmt.Sprintf("%s%s", indent, name))
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return lines, nil
}

// Run performs the full inspection and writes a human report to w.
func Run(fs afcfs.FS, w io.Writer, opts Options) error {
	docsRoot, err := locate.DocumentsRoot(fs, opts.PathPrefix)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "AFC root prefix (Documents): %q\n\n", docsRoot)

	fmt.Fprintln(w, "Directory tree:")
	lines, err := TreeLines(fs, docsRoot, 6)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintln(w, "  "+l)
	}
	fmt.Fprintln(w)

	cands, err := locate.FindCatalogs(fs, docsRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Catalogs with settings-acr (%d):\n", len(cands))
	for i, c := range cands {
		fmt.Fprintf(w, "  [%d] %s  (userStyles files: %d)  -> %s\n", i, c.Name, c.PresetCount, c.UserStyles)
	}
	fmt.Fprintln(w)

	chosen, err := locate.SelectCatalog(cands, opts.CatalogFlag, opts.Picker)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Selected catalog: %s\n  target userStyles: %s\n\n", chosen.Name, chosen.UserStyles)

	if opts.Samples > 0 {
		candidates := sampleCandidates(fs, chosen.UserStyles, opts.Samples)
		if len(candidates) == 0 {
			fmt.Fprintln(w, "no existing preset files to sample in userStyles")
			return nil
		}
		if err := os.MkdirAll(opts.SampleDir, 0o755); err != nil {
			fmt.Fprintf(w, "could not create sample dir %s: %v\n", opts.SampleDir, err)
			return nil
		}
		for _, src := range candidates {
			dst := filepath.Join(opts.SampleDir, baseName(src))
			if err := fs.Pull(src, dst); err != nil {
				fmt.Fprintf(w, "sample pull failed %s: %v\n", src, err)
				continue
			}
			fmt.Fprintf(w, "pulled sample: %s -> %s\n", src, dst)
		}
	}
	return nil
}

// indexFileName is Lightroom's binary preset index; it is not a representative
// preset, so sampling skips it.
const indexFileName = "Index.dat"

func baseName(devicePath string) string {
	if i := strings.LastIndex(devicePath, "/"); i >= 0 {
		return devicePath[i+1:]
	}
	return devicePath
}

// sampleCandidates returns up to max device file paths under userStyles suitable
// as format samples. Real presets live inside per-group subfolders, so it
// prefers files one level down, then falls back to top-level files, always
// skipping Index.dat.
func sampleCandidates(fs afcfs.FS, userStyles string, max int) []string {
	entries, err := fs.List(userStyles)
	if err != nil {
		return nil
	}
	var files []string
	// Pass 1: files inside subfolders (the actual presets).
	for _, name := range entries {
		if len(files) >= max {
			return files
		}
		child := userStyles + "/" + name
		fi, err := fs.Stat(child)
		if err != nil || !fi.IsDir {
			continue
		}
		sub, err := fs.List(child)
		if err != nil {
			continue
		}
		for _, sn := range sub {
			if len(files) >= max {
				break
			}
			sc := child + "/" + sn
			if sfi, err := fs.Stat(sc); err != nil || sfi.IsDir {
				continue
			}
			files = append(files, sc)
		}
	}
	// Pass 2: top-level files (except Index.dat) if more are still needed.
	for _, name := range entries {
		if len(files) >= max {
			break
		}
		if name == indexFileName {
			continue
		}
		child := userStyles + "/" + name
		if fi, err := fs.Stat(child); err != nil || fi.IsDir {
			continue
		}
		files = append(files, child)
	}
	return files
}
