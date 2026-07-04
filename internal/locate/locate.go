// Package locate finds the Lightroom userStyles directory inside the app container.
package locate

import (
	"fmt"
	"strings"

	"github.com/david-zw-liu/lrmount/internal/afcfs"
)

func join(parts ...string) string {
	var nonEmpty []string
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, strings.Trim(p, "/"))
		}
	}
	return strings.Join(nonEmpty, "/")
}

// DocumentsRoot determines the path prefix that contains the catalog folders.
// override wins. Otherwise we probe for a listable "Documents" dir and return
// "Documents" if found; this works even on house_arrest VendDocuments transports
// where the AFC root itself is not listable (List("") returns afc error 10). If
// "Documents" is not listable, we fall back to listing the root for a Documents
// child, and finally assume the root already is Documents (return "").
func DocumentsRoot(fs afcfs.FS, override string) (string, error) {
	if override != "" {
		return strings.Trim(override, "/"), nil
	}
	// Preferred probe: the root may be unlistable, but a named child is not.
	if _, err := fs.List("Documents"); err == nil {
		return "Documents", nil
	}
	// Fall back to listing the root (some transports allow it).
	entries, err := fs.List("")
	if err != nil {
		// Root not listable and no Documents dir: assume the root is Documents.
		return "", nil
	}
	for _, e := range entries {
		if e == "Documents" {
			if fi, err := fs.Stat("Documents"); err == nil && fi.IsDir {
				return "Documents", nil
			}
		}
	}
	return "", nil
}

// Catalog is one Lightroom catalog/account folder that has a settings-acr dir.
type Catalog struct {
	Name        string
	Dir         string
	UserStyles  string
	PresetCount int
}

// FindCatalogs lists docsRoot's children and keeps those containing settings-acr.
func FindCatalogs(fs afcfs.FS, docsRoot string) ([]Catalog, error) {
	children, err := fs.List(docsRoot)
	if err != nil {
		return nil, fmt.Errorf("list %q: %w", docsRoot, err)
	}
	var out []Catalog
	for _, name := range children {
		dir := join(docsRoot, name)
		fi, err := fs.Stat(dir)
		if err != nil || !fi.IsDir {
			continue
		}
		settings := join(dir, "settings-acr")
		if sfi, err := fs.Stat(settings); err != nil || !sfi.IsDir {
			continue
		}
		userStyles := join(settings, "userStyles")
		count := 0
		if entries, err := fs.List(userStyles); err == nil {
			count = len(entries)
		}
		out = append(out, Catalog{Name: name, Dir: dir, UserStyles: userStyles, PresetCount: count})
	}
	return out, nil
}
