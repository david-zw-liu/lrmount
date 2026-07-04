package main

import (
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

// mountBase is where volumes are mounted. A mountpoint is throwaway scratch
// (the data is on the device; Finder names the volume from the NFS share), so
// it lives under the per-user temp dir, $TMPDIR (/var/folders/.../T on macOS).
// If $TMPDIR resolves to a location that refuses user NFS mounts (e.g. the
// shared /private/tmp), the mount simply fails and is reported — no fallback.
func mountBase() string {
	return filepath.Join(os.TempDir(), "lrmount")
}

// mountAt mounts the NFS server on port as deviceName and returns the
// mountpoint. A live leftover mount at the path gets a numeric suffix so two
// volumes never share a dir.
func mountAt(deviceName string, port int) (string, error) {
	mp, err := makeMountpoint(mountBase(), deviceName)
	if err != nil {
		return "", err
	}
	if err := mountctl.MountNFS(mp, deviceName, port); err != nil {
		mountctl.Cleanup(mp)
		return "", err
	}
	return mp, nil
}

// makeMountpoint creates a fresh, uniquely-named mount directory under base
// for deviceName and returns its canonical path. The random suffix guarantees
// no collision with a leftover mount from a previous run. The path is resolved
// through symlinks here, while it is still a plain directory, because the
// kernel mount table reports real paths (e.g. /private/var, not the /var
// symlink) — comparing an unresolved path against it would make a live mount
// look unmounted.
func makeMountpoint(base, deviceName string) (string, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	mp, err := os.MkdirTemp(base, sanitizeSeg(deviceName)+"-*")
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(mp); err == nil {
		mp = real
	}
	return mp, nil
}
