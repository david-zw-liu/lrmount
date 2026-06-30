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
		entries, err := fs.List(chosen.UserStyles)
		if err != nil {
			fmt.Fprintf(w, "could not list userStyles for sampling: %v\n", err)
			return nil
		}
		if err := os.MkdirAll(opts.SampleDir, 0o755); err != nil {
			fmt.Fprintf(w, "could not create sample dir %s: %v\n", opts.SampleDir, err)
			return nil
		}
		pulled := 0
		for _, name := range entries {
			if pulled >= opts.Samples {
				break
			}
			src := chosen.UserStyles + "/" + name
			if fi, err := fs.Stat(src); err != nil || fi.IsDir {
				continue
			}
			dst := filepath.Join(opts.SampleDir, name)
			if err := fs.Pull(src, dst); err != nil {
				fmt.Fprintf(w, "sample pull failed %s: %v\n", name, err)
				continue
			}
			fmt.Fprintf(w, "pulled sample: %s -> %s\n", src, dst)
			pulled++
		}
		if pulled == 0 {
			fmt.Fprintln(w, "no existing files to sample in userStyles")
		}
	}
	return nil
}
