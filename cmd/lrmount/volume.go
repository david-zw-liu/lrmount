package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/david-zw-liu/lrmount/internal/mountctl"
)

// appLabel maps a Lightroom bundle id to the human-readable name shown as the
// Finder volume name and used as the mount-path segment.
func appLabel(bundleID string) string {
	switch bundleID {
	case "com.adobe.lrmobilephone":
		return "Lightroom Mobile"
	case "com.adobe.lrmobile":
		return "Lightroom for iPad"
	default:
		return "Lightroom"
	}
}

// sanitizeSeg makes s safe as a single filesystem path segment.
func sanitizeSeg(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ':' {
			return '-'
		}
		return r
	}, s)
}

// hintPath maps a device-side userStyles path onto its mounted location:
// <mountpoint>/<app>/<path within Documents>. app is the virtual
// subdirectory the Router serves that device's app under.
func hintPath(mountpoint, app, root, devicePath string) string {
	rel := strings.Trim(strings.TrimPrefix(devicePath, strings.Trim(root, "/")), "/")
	return filepath.Join(mountpoint, app, rel)
}

// mountpointFor returns the mount directory ~/lrmount/{device}, creating the
// parent hierarchy. A live mount already there (leftover from a prior run)
// gets a numeric suffix so we never mount two volumes on one dir.
func mountpointFor(deviceName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot resolve home directory: %w", err)
	}
	base := filepath.Join(home, "lrmount")
	for i := 1; i <= 9; i++ {
		leaf := sanitizeSeg(deviceName)
		if i > 1 {
			leaf = fmt.Sprintf("%s %d", leaf, i)
		}
		mp := filepath.Join(base, leaf)
		if mountctl.IsMounted(mp) {
			continue
		}
		if err := os.MkdirAll(mp, 0o755); err != nil {
			return "", err
		}
		return mp, nil
	}
	return "", fmt.Errorf("no usable mountpoint under %q", base)
}
