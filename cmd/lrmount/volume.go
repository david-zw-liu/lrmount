package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/david-zw-liu/lrmount/internal/mountctl"
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
	bases := []string{"/Volumes"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		bases = append(bases, filepath.Join(home, "lrmount-volumes"))
	}
	for baseIdx, base := range bases {
		if baseIdx > 0 {
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
