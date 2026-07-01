# lrpush mirror + watcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace lrpush's subcommands with a single flagless flow that mirrors each installed Lightroom app's `userStyles` into a local `./sync/{bundle-id}/userStyles/` folder and live-syncs local edits back to the device.

**Architecture:** A new `internal/mirror` package owns the two device-mutating operations — `PullReplace` (device→local on startup) and `Reconcile` (local→device per debounced change) — plus a thin fsnotify `Watcher` at the edge. `internal/device` gains multi-app detection by probing house_arrest vend. `cmd/lrpush` collapses to one `run()` orchestration: pick device → detect apps → per-app locate+pick catalog → wipe `./sync` → pull-replace → warn → watch until Ctrl-C. The `inspect`/`push`/`rm`/`devices` subcommands and the `inspect`/`pushsync`/`rmsync` packages are deleted.

**Tech Stack:** Go 1.26, go-ios v1.2.0 (house_arrest + AFC), cobra (single command + `--help`), charmbracelet/huh (device/catalog pickers), fsnotify (new), golang.org/x/term.

## Global Constraints

- Module path: `github.com/davidliu/lrpush`; Go `1.26.4`.
- **Zero CLI flags.** Bare `lrpush` only. No `--udid`, `--bundle-id`, `--path-prefix`, `--catalog`.
- Lightroom bundle ids, probed in this precedence order: `com.adobe.lrmobilephone` (iPhone), then `com.adobe.lrmobile` (iPad/universal).
- Local mirror root: `./sync/{bundle-id}/userStyles/`. `./sync/` is wiped at the start of every run; it is NOT deleted on exit.
- No backups. A single startup warning; the user is trusted.
- Device filesystem is accessed only through `afcfs.FS`; all logic is unit-tested against `afcfs.MemFS` + a real temp dir for the local side. fsnotify itself is not unit-tested — the tested core is `Reconcile` and pure helpers.
- Watcher debounce: 400ms.
- `afcfs.FS.MkDir` is mkdir-p; `PushFile` writes an exact device path; `Pull` recurses; the AFC root is often unlistable (list named children only).
- End every commit message with: `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`

## File Structure

**New**
- `internal/mirror/mirror.go` — `PullReplace`, `Reconcile`, and their unexported helpers (`pullTree`, `pushDir`, `deviceMkdirAll`, `safeRel`, `deviceJoinRel`).
- `internal/mirror/watcher.go` — `Watcher` (fsnotify recursive watch + debounce → `Reconcile`) and helpers `relFromEventPath`, `subdirs`.
- `internal/mirror/mirror_test.go` — tests for `PullReplace`, `Reconcile`, `safeRel`.
- `internal/mirror/watcher_test.go` — tests for `relFromEventPath`, `subdirs`.
- `cmd/lrpush/pickers.go` — `pickIndex`, `catalogPicker`.

**Modify**
- `internal/device/device.go` — add `collectVendable` (pure) + `DetectSessions` (real probe); keep `List`, `Session`, `Close`.
- `internal/device/device_test.go` — new: test `collectVendable`.
- `cmd/lrpush/root.go` — replace subcommand wiring with the single `run()` flow; drop all flags.
- `go.mod` / `go.sum` — add `github.com/fsnotify/fsnotify`.
- `.gitignore` — add `/sync/`; drop the now-unused `_userStyles_backup` lines.
- `README.md` — rewrite for the single flow.

**Delete**
- `cmd/lrpush/devices.go`, `inspect.go`, `push.go`, `rm.go`, `interactive.go`, `tui.go`, `interactive_test.go`, `root_test.go`.
- `internal/inspect/`, `internal/pushsync/`, `internal/rmsync/`.
- `_userStyles_backup/` directory (and its `.keep`).

Keep `cmd/lrpush/banner.go` + `banner_test.go` (the warning banner is reused).

---

### Task 1: mirror package — `PullReplace` (device → local)

**Files:**
- Create: `internal/mirror/mirror.go`
- Test: `internal/mirror/mirror_test.go`

**Interfaces:**
- Consumes: `afcfs.FS` (`List`, `Stat`, `Pull`, `MkDir`, `RemoveAll`, `PushFile`), `afcfs.MemFS` (`AddFile`, `AddDir`, `Pulled`).
- Produces:
  - `func PullReplace(fs afcfs.FS, deviceUserStyles, localDir string, log func(string)) error`
  - `func deviceJoinRel(root, rel string) string` (unexported; `rel` is slash-separated, may be "")
  - `func safeRel(rel string) (string, error)` (unexported; used by Task 2)

- [ ] **Step 1: Write the failing test**

```go
package mirror

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidliu/lrpush/internal/afcfs"
)

func TestPullReplaceMirrorsTreeAndCreatesLocalDirs(t *testing.T) {
	fs := afcfs.NewMemFS()
	// device userStyles tree
	fs.AddFile("Documents/cat/settings-acr/userStyles/A/foo.xmp", 10)
	fs.AddFile("Documents/cat/settings-acr/userStyles/B/bar.xmp", 20)
	fs.AddFile("Documents/cat/settings-acr/userStyles/Index.dat", 30)

	local := filepath.Join(t.TempDir(), "sync", "com.adobe.lrmobile", "userStyles")
	root := "Documents/cat/settings-acr/userStyles"

	var logged []string
	if err := PullReplace(fs, root, local, func(s string) { logged = append(logged, s) }); err != nil {
		t.Fatal(err)
	}

	// local subdirs created
	for _, d := range []string{"A", "B"} {
		if fi, err := os.Stat(filepath.Join(local, d)); err != nil || !fi.IsDir() {
			t.Errorf("expected local dir %s", d)
		}
	}
	// every device file was pulled to its mirrored local path
	wants := map[string]string{
		root + "/A/foo.xmp":  filepath.Join(local, "A", "foo.xmp"),
		root + "/B/bar.xmp":  filepath.Join(local, "B", "bar.xmp"),
		root + "/Index.dat":  filepath.Join(local, "Index.dat"),
	}
	for src, dst := range wants {
		if got := fs.Pulled[src]; got != dst {
			t.Errorf("Pulled[%q] = %q, want %q", src, got, dst)
		}
	}
	if len(logged) != 3 {
		t.Errorf("expected 3 per-file log lines, got %d: %v", len(logged), logged)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run TestPullReplace -v`
Expected: FAIL — package/function does not compile (`undefined: PullReplace`).

- [ ] **Step 3: Write minimal implementation**

```go
// Package mirror mirrors a device Lightroom userStyles folder to a local
// directory and syncs local edits back to the device.
package mirror

import (
	"os"
	"path"
	"path/filepath"

	"github.com/davidliu/lrpush/internal/afcfs"
)

// deviceJoinRel joins a device root with a slash-separated relative path.
// An empty rel returns the root unchanged.
func deviceJoinRel(root, rel string) string {
	if rel == "" {
		return root
	}
	return root + "/" + rel
}

// PullReplace recreates localDir and recursively pulls the device userStyles
// tree into it, logging one line per file. The device is never written. The
// caller is responsible for wiping ./sync beforehand.
func PullReplace(fs afcfs.FS, deviceUserStyles, localDir string, log func(string)) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return err
	}
	return pullTree(fs, deviceUserStyles, localDir, "", log)
}

func pullTree(fs afcfs.FS, deviceRoot, localRoot, rel string, log func(string)) error {
	entries, err := fs.List(deviceJoinRel(deviceRoot, rel))
	if err != nil {
		return err
	}
	for _, name := range entries {
		childRel := path.Join(rel, name)
		devPath := deviceJoinRel(deviceRoot, childRel)
		fi, err := fs.Stat(devPath)
		if err != nil {
			return err
		}
		localPath := filepath.Join(localRoot, filepath.FromSlash(childRel))
		if fi.IsDir {
			if err := os.MkdirAll(localPath, 0o755); err != nil {
				return err
			}
			if err := pullTree(fs, deviceRoot, localRoot, childRel, log); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			return err
		}
		if err := fs.Pull(devPath, localPath); err != nil {
			return err
		}
		log("pulled " + childRel)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mirror/ -run TestPullReplace -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mirror/mirror.go internal/mirror/mirror_test.go
git commit -m "$(printf 'feat(mirror): PullReplace mirrors device userStyles to local\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: mirror package — `Reconcile` (local → device)

**Files:**
- Modify: `internal/mirror/mirror.go`
- Modify: `internal/mirror/mirror_test.go`

**Interfaces:**
- Consumes: `afcfs.FS`, `deviceJoinRel` (Task 1).
- Produces:
  - `func Reconcile(fs afcfs.FS, localDir, deviceUserStyles string, changed []string, log func(string)) error`
  - `func safeRel(rel string) (string, error)` — cleans a slash/native rel path; error if it escapes (`..`, absolute, empty).

Behaviour of `Reconcile` for each `changed` rel path:
- local path missing → `fs.RemoveAll(devicePath)` (mirror the deletion).
- local path is a file → mkdir-p parent on device, `fs.PushFile`.
- local path is a dir → mkdir-p on device and push every file under it.
- rel path that fails `safeRel` → log and skip (never touch the device).
- Any single-path error is logged and skipped; `Reconcile` continues and returns nil (a live session must survive one bad op).

- [ ] **Step 1: Write the failing tests**

```go
func TestSafeRelRejectsEscapes(t *testing.T) {
	bad := []string{"", ".", "..", "../x", "/abs", "a/../../b"}
	for _, r := range bad {
		if _, err := safeRel(r); err == nil {
			t.Errorf("safeRel(%q): expected error", r)
		}
	}
	good := map[string]string{"A/foo.xmp": "A/foo.xmp", "a/b/c": "a/b/c", "foo.xmp": "foo.xmp"}
	for in, want := range good {
		got, err := safeRel(in)
		if err != nil || got != want {
			t.Errorf("safeRel(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
}

func TestReconcilePushesNewFile(t *testing.T) {
	fs := afcfs.NewMemFS()
	local := t.TempDir()
	writeLocal(t, local, "A/foo.xmp", "hi")
	root := "Documents/cat/settings-acr/userStyles"

	if err := Reconcile(fs, local, root, []string{"A/foo.xmp"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !fs.Has(root + "/A/foo.xmp") {
		t.Error("expected pushed file on device")
	}
}

func TestReconcilePushesNewDirRecursively(t *testing.T) {
	fs := afcfs.NewMemFS()
	local := t.TempDir()
	writeLocal(t, local, "Grp/one.xmp", "a")
	writeLocal(t, local, "Grp/sub/two.xmp", "b")
	root := "Documents/cat/settings-acr/userStyles"

	if err := Reconcile(fs, local, root, []string{"Grp"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{root + "/Grp/one.xmp", root + "/Grp/sub/two.xmp"} {
		if !fs.Has(p) {
			t.Errorf("expected %s pushed", p)
		}
	}
}

func TestReconcileDeletesMissingLocalPath(t *testing.T) {
	fs := afcfs.NewMemFS()
	root := "Documents/cat/settings-acr/userStyles"
	fs.AddFile(root+"/Old/gone.xmp", 5)
	local := t.TempDir() // Old/ does not exist locally

	if err := Reconcile(fs, local, root, []string{"Old"}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if fs.Has(root + "/Old") || fs.Has(root+"/Old/gone.xmp") {
		t.Error("expected device path removed")
	}
}

func TestReconcileSkipsEscapePath(t *testing.T) {
	fs := afcfs.NewMemFS()
	root := "Documents/cat/settings-acr/userStyles"
	fs.AddFile(root+"/keep.xmp", 1)
	local := t.TempDir()

	var logged []string
	if err := Reconcile(fs, local, root, []string{"../evil"}, func(s string) { logged = append(logged, s) }); err != nil {
		t.Fatal(err)
	}
	if !fs.Has(root + "/keep.xmp") {
		t.Error("device content must be untouched by an escape path")
	}
	if len(logged) == 0 {
		t.Error("expected a log line for the refused path")
	}
}

// writeLocal creates local/rel with the given content, making parent dirs.
func writeLocal(t *testing.T, local, rel, content string) {
	t.Helper()
	p := filepath.Join(local, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run 'TestReconcile|TestSafeRel' -v`
Expected: FAIL — `undefined: Reconcile`, `undefined: safeRel`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/mirror/mirror.go` (add `"errors"`, `"fmt"`, `"io/fs"` as `iofs`, `"strings"` to imports):

```go
// safeRel normalises a relative path and rejects anything that escapes the
// mirror root (empty, ".", absolute, or containing a leading "..").
func safeRel(rel string) (string, error) {
	c := path.Clean(filepath.ToSlash(rel))
	if c == "" || c == "." {
		return "", fmt.Errorf("empty path")
	}
	if path.IsAbs(c) || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path escapes userStyles: %q", rel)
	}
	return c, nil
}

// deviceMkdirAll makes a device directory; afcfs.MkDir is already mkdir-p.
func deviceMkdirAll(fs afcfs.FS, dir string) error { return fs.MkDir(dir) }

// Reconcile applies a set of changed local relative paths to the device:
// missing local path -> RemoveAll on device; file -> PushFile; dir -> push all
// files under it. Per-path errors and refused (escaping) paths are logged and
// skipped so the session survives; Reconcile returns nil.
func Reconcile(fs afcfs.FS, localDir, deviceUserStyles string, changed []string, log func(string)) error {
	for _, raw := range changed {
		rel, err := safeRel(raw)
		if err != nil {
			log("skip " + err.Error())
			continue
		}
		localPath := filepath.Join(localDir, filepath.FromSlash(rel))
		devPath := deviceJoinRel(deviceUserStyles, rel)

		fi, statErr := os.Stat(localPath)
		if errors.Is(statErr, os.ErrNotExist) {
			if err := fs.RemoveAll(devPath); err != nil {
				log("delete failed " + rel + ": " + err.Error())
				continue
			}
			log("deleted " + rel)
			continue
		}
		if statErr != nil {
			log("stat failed " + rel + ": " + statErr.Error())
			continue
		}
		if fi.IsDir() {
			if err := pushDir(fs, localPath, devPath, log); err != nil {
				log("push dir failed " + rel + ": " + err.Error())
			}
			continue
		}
		if err := deviceMkdirAll(fs, path.Dir(devPath)); err != nil {
			log("mkdir failed " + rel + ": " + err.Error())
			continue
		}
		if err := fs.PushFile(localPath, devPath); err != nil {
			log("push failed " + rel + ": " + err.Error())
			continue
		}
		log("pushed " + rel)
	}
	return nil
}

// pushDir mkdir-p's deviceDir and pushes every file under localDir into it.
func pushDir(fs afcfs.FS, localDir, deviceDir string, log func(string)) error {
	if err := deviceMkdirAll(fs, deviceDir); err != nil {
		return err
	}
	return filepath.WalkDir(localDir, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		dev := deviceDir
		if relSlash != "." {
			dev = deviceDir + "/" + relSlash
		}
		if d.IsDir() {
			return deviceMkdirAll(fs, dev)
		}
		if err := fs.PushFile(p, dev); err != nil {
			return err
		}
		log("pushed " + relSlash)
		return nil
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mirror/ -v`
Expected: PASS (all Task 1 + Task 2 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/mirror/mirror.go internal/mirror/mirror_test.go
git commit -m "$(printf 'feat(mirror): Reconcile pushes/deletes local changes to device\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: device — multi-app detection by vend probe

**Files:**
- Modify: `internal/device/device.go`
- Create: `internal/device/device_test.go`

**Interfaces:**
- Consumes: existing `Session`, `openHouseArrest`, `ios.GetDevice`, `afcfs.Wrap`.
- Produces:
  - `func DetectSessions(udid string, bundleIDs []string) ([]*Session, error)` — probes each bundle id; returns a `*Session` for every one that vends; error only if none vend.
  - `func collectVendable(bundleIDs []string, probe func(string) (*Session, error)) ([]*Session, error)` — pure; loops in order, keeps successes, errors if none.

- [ ] **Step 1: Write the failing test**

```go
package device

import (
	"fmt"
	"testing"
)

func TestCollectVendableKeepsSuccessesInOrder(t *testing.T) {
	installed := map[string]bool{"com.adobe.lrmobile": true} // only iPad app present
	probe := func(id string) (*Session, error) {
		if installed[id] {
			return &Session{BundleID: id}, nil
		}
		return nil, fmt.Errorf("not installed")
	}
	got, err := collectVendable([]string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].BundleID != "com.adobe.lrmobile" {
		t.Fatalf("got %+v, want single com.adobe.lrmobile", got)
	}
}

func TestCollectVendableBothInstalled(t *testing.T) {
	probe := func(id string) (*Session, error) { return &Session{BundleID: id}, nil }
	got, err := collectVendable([]string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BundleID != "com.adobe.lrmobilephone" {
		t.Fatalf("got %+v, want both, mobilephone first", got)
	}
}

func TestCollectVendableNoneInstalled(t *testing.T) {
	probe := func(id string) (*Session, error) { return nil, fmt.Errorf("nope") }
	if _, err := collectVendable([]string{"a", "b"}, probe); err == nil {
		t.Fatal("expected error when nothing vends")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/device/ -run TestCollectVendable -v`
Expected: FAIL — `undefined: collectVendable`.

- [ ] **Step 3: Write minimal implementation**

Append to `internal/device/device.go`:

```go
// collectVendable probes each bundle id in order and returns a Session for
// every one that vends successfully. It errors only if none vend.
func collectVendable(bundleIDs []string, probe func(string) (*Session, error)) ([]*Session, error) {
	var out []*Session
	var lastErr error
	for _, id := range bundleIDs {
		s, err := probe(id)
		if err != nil {
			lastErr = err
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("no Lightroom app vends on this device: %w", lastErr)
		}
		return nil, fmt.Errorf("no Lightroom app found on this device")
	}
	return out, nil
}

// DetectSessions resolves the device and opens a house_arrest AFC session for
// every installed Lightroom app (each bundle id that vends). At least one must
// vend or it returns an error. Callers own Close() on every returned session.
func DetectSessions(udid string, bundleIDs []string) ([]*Session, error) {
	d, err := ios.GetDevice(udid)
	if err != nil {
		return nil, fmt.Errorf("resolve device: %w", err)
	}
	return collectVendable(bundleIDs, func(id string) (*Session, error) {
		client, err := openHouseArrest(d, id)
		if err != nil {
			return nil, err
		}
		return &Session{
			FS:       afcfs.Wrap(client),
			Label:    DescribeDevice(d),
			BundleID: id,
			closer:   client.Close,
		}, nil
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/device/ -run TestCollectVendable -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/device/device.go internal/device/device_test.go
git commit -m "$(printf 'feat(device): DetectSessions vends every installed Lightroom app\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 4: mirror — fsnotify Watcher

**Files:**
- Create: `internal/mirror/watcher.go`
- Create: `internal/mirror/watcher_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `Reconcile` (Task 2), `github.com/fsnotify/fsnotify`.
- Produces:
  - `func NewWatcher(fs afcfs.FS, localDir, deviceUserStyles string, log func(string)) (*Watcher, error)`
  - `func (w *Watcher) Run(ctx context.Context) error` — adds recursive watches, debounces events (400ms), calls `Reconcile` per batch, adds watches for newly-created dirs; returns when `ctx` is cancelled.
  - `func relFromEventPath(localDir, evPath string) (string, bool)` (unexported; false if the path is the root or escapes).
  - `func subdirs(root string) ([]string, error)` (unexported; every directory at/under root).

- [ ] **Step 1: Add the fsnotify dependency**

Run:
```bash
go get github.com/fsnotify/fsnotify@latest
```
Expected: `go.mod` gains `github.com/fsnotify/fsnotify`.

- [ ] **Step 2: Write the failing test**

```go
package mirror

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestRelFromEventPath(t *testing.T) {
	local := "/tmp/sync/app/userStyles"
	if rel, ok := relFromEventPath(local, filepath.Join(local, "A", "foo.xmp")); !ok || rel != "A/foo.xmp" {
		t.Errorf("got %q,%v want A/foo.xmp,true", rel, ok)
	}
	if _, ok := relFromEventPath(local, local); ok {
		t.Error("root itself should be rejected")
	}
	if _, ok := relFromEventPath(local, "/etc/passwd"); ok {
		t.Error("outside path should be rejected")
	}
}

func TestSubdirs(t *testing.T) {
	root := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(root, "A", "sub"), 0o755))
	must(t, os.MkdirAll(filepath.Join(root, "B"), 0o755))

	got, err := subdirs(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{root, filepath.Join(root, "A"), filepath.Join(root, "A", "sub"), filepath.Join(root, "B")}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mirror/ -run 'TestRelFromEventPath|TestSubdirs' -v`
Expected: FAIL — `undefined: relFromEventPath`, `undefined: subdirs`.

- [ ] **Step 4: Write the implementation**

```go
package mirror

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/davidliu/lrpush/internal/afcfs"
)

const debounce = 400 * time.Millisecond

// Watcher mirrors local edits under localDir up to the device userStyles.
type Watcher struct {
	fs               afcfs.FS
	localDir         string
	deviceUserStyles string
	log              func(string)
	w                *fsnotify.Watcher
}

func NewWatcher(fs afcfs.FS, localDir, deviceUserStyles string, log func(string)) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{fs: fs, localDir: localDir, deviceUserStyles: deviceUserStyles, log: log, w: w}, nil
}

// relFromEventPath converts an absolute event path to a slash relative path
// under localDir. It returns false for the root itself or any path outside it.
func relFromEventPath(localDir, evPath string) (string, bool) {
	rel, err := filepath.Rel(localDir, evPath)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// subdirs returns root and every directory beneath it.
func subdirs(root string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs, err
}

// Run watches localDir recursively and pushes debounced batches of changes to
// the device via Reconcile until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	dirs, err := subdirs(w.localDir)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if err := w.w.Add(d); err != nil {
			return err
		}
	}

	pending := map[string]struct{}{}
	var timerC <-chan time.Time

	flush := func() {
		if len(pending) == 0 {
			return
		}
		changed := make([]string, 0, len(pending))
		for r := range pending {
			changed = append(changed, r)
		}
		pending = map[string]struct{}{}
		if err := Reconcile(w.fs, w.localDir, w.deviceUserStyles, changed, w.log); err != nil {
			w.log("reconcile error: " + err.Error())
		}
	}

	for {
		select {
		case <-ctx.Done():
			return w.w.Close()
		case ev, ok := <-w.w.Events:
			if !ok {
				return nil
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := statDir(ev.Name); err == nil && fi {
					_ = w.w.Add(ev.Name) // watch newly-created subdir
				}
			}
			if rel, ok := relFromEventPath(w.localDir, ev.Name); ok {
				pending[rel] = struct{}{}
				timerC = time.After(debounce)
			}
		case <-timerC:
			timerC = nil
			flush()
		case err, ok := <-w.w.Errors:
			if !ok {
				return nil
			}
			w.log("watch error: " + err.Error())
		}
	}
}
```

Add `"os"` to the import block and define the small dir-check helper used by
`Run` when it sees a Create event:

```go
func statDir(p string) (bool, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return false, err
	}
	return fi.IsDir(), nil
}
```

- [ ] **Step 5: Run tests + build + vet**

Run:
```bash
go test ./internal/mirror/ -v
go build ./...
go vet ./internal/mirror/
```
Expected: tests PASS; build and vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/mirror/watcher.go internal/mirror/watcher_test.go go.mod go.sum
git commit -m "$(printf 'feat(mirror): fsnotify Watcher debounces local edits to device\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 5: cmd/lrpush — collapse to the single mirror flow

This task deletes the old subcommands/packages and rewires `cmd/lrpush` so the
binary is the new tool. It is one task because the build only compiles once all
the pieces move together.

**Files:**
- Create: `cmd/lrpush/pickers.go`
- Rewrite: `cmd/lrpush/root.go`
- Delete: `cmd/lrpush/devices.go`, `inspect.go`, `push.go`, `rm.go`, `interactive.go`, `tui.go`, `interactive_test.go`, `root_test.go`
- Delete dirs: `internal/inspect/`, `internal/pushsync/`, `internal/rmsync/`
- Keep: `cmd/lrpush/main.go`, `banner.go`, `banner_test.go`

**Interfaces:**
- Consumes: `device.List`, `device.DetectSessions`, `device.Info`, `device.Session`; `locate.DocumentsRoot/FindCatalogs/SelectCatalog/Catalog`; `mirror.PullReplace/NewWatcher/Watcher`; `warningBanner` (banner.go).
- Produces (within package `main`):
  - `func run() error`
  - `func pickIndex(title string, labels []string) (int, error)`
  - `func catalogPicker(cands []locate.Catalog) (int, error)`
  - `var lightroomBundleIDs = []string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}`

- [ ] **Step 1: Delete the old subcommands, packages, and their tests**

Run:
```bash
git rm cmd/lrpush/devices.go cmd/lrpush/inspect.go cmd/lrpush/push.go \
       cmd/lrpush/rm.go cmd/lrpush/interactive.go cmd/lrpush/tui.go \
       cmd/lrpush/interactive_test.go cmd/lrpush/root_test.go
git rm -r internal/inspect internal/pushsync internal/rmsync
git rm -r _userStyles_backup
```
Expected: files staged for deletion. (The build is intentionally broken until Step 3.)

- [ ] **Step 2: Write `cmd/lrpush/pickers.go`**

```go
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/davidliu/lrpush/internal/locate"
)

// pickIndex shows an arrow-key menu (huh) on a TTY, or a numbered stdin prompt
// otherwise, and returns the chosen 0-based index.
func pickIndex(title string, labels []string) (int, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		var sel int
		opts := make([]huh.Option[int], len(labels))
		for i, l := range labels {
			opts[i] = huh.NewOption(l, i)
		}
		err := huh.NewSelect[int]().Title(title).Options(opts...).Value(&sel).Run()
		if err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				return -1, fmt.Errorf("selection cancelled")
			}
			return -1, err
		}
		return sel, nil
	}
	// non-TTY fallback
	fmt.Println(title)
	for i, l := range labels {
		fmt.Printf("  [%d] %s\n", i+1, l)
	}
	fmt.Print("Enter number: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return -1, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(labels) {
		return -1, fmt.Errorf("invalid selection %q", strings.TrimSpace(line))
	}
	return n - 1, nil
}

// catalogPicker adapts pickIndex to locate.SelectCatalog's picker signature.
func catalogPicker(cands []locate.Catalog) (int, error) {
	labels := make([]string, len(cands))
	for i, c := range cands {
		labels[i] = fmt.Sprintf("%s (%d presets)", c.Name, c.PresetCount)
	}
	return pickIndex("Select a catalog", labels)
}
```

- [ ] **Step 3: Rewrite `cmd/lrpush/root.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"

	"github.com/spf13/cobra"

	"github.com/davidliu/lrpush/internal/device"
	"github.com/davidliu/lrpush/internal/locate"
	"github.com/davidliu/lrpush/internal/mirror"
)

// lightroomBundleIDs are probed in order; the iPhone app comes first.
var lightroomBundleIDs = []string{"com.adobe.lrmobilephone", "com.adobe.lrmobile"}

var rootCmd = &cobra.Command{
	Use:           "lrpush",
	Short:         "Mirror an iPhone/iPad Lightroom app's userStyles to ./sync and live-sync edits back",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          func(cmd *cobra.Command, args []string) error { return run() },
}

func Execute() error { return rootCmd.Execute() }

// prefixLogger returns a logger that tags each line with the bundle id when
// more than one app is being mirrored concurrently.
func prefixLogger(bundleID string, multi bool) func(string) {
	return func(s string) {
		if multi {
			fmt.Printf("[%s] %s\n", bundleID, s)
		} else {
			fmt.Println(s)
		}
	}
}

type appMirror struct {
	sess       *device.Session
	userStyles string
	localDir   string
}

func run() error {
	// 1. Pick device.
	infos, err := device.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return fmt.Errorf("no USB device found; connect and trust your device")
	}
	chosen := infos[0]
	if len(infos) > 1 {
		labels := make([]string, len(infos))
		for i, d := range infos {
			labels[i] = fmt.Sprintf("%s  (%s, iOS %s, %s)", d.Name, d.ProductType, d.Version, d.UDID)
		}
		idx, err := pickIndex("Select a device", labels)
		if err != nil {
			return err
		}
		chosen = infos[idx]
	}

	// 2. Detect every installed Lightroom app.
	sessions, err := device.DetectSessions(chosen.UDID, lightroomBundleIDs)
	if err != nil {
		return err
	}
	defer func() {
		for _, s := range sessions {
			s.Close()
		}
	}()

	// 3. Per-app: locate userStyles + choose catalog + compute local dir.
	var mirrors []appMirror
	for _, s := range sessions {
		docs, err := locate.DocumentsRoot(s.FS, "")
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		cands, err := locate.FindCatalogs(s.FS, docs)
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		cat, err := locate.SelectCatalog(cands, "", catalogPicker)
		if err != nil {
			return fmt.Errorf("[%s] %w", s.BundleID, err)
		}
		mirrors = append(mirrors, appMirror{
			sess:       s,
			userStyles: cat.UserStyles,
			localDir:   filepath.Join("sync", s.BundleID, "userStyles"),
		})
	}
	multi := len(mirrors) > 1

	// 4. Wipe ./sync then pull-replace each app.
	if err := os.RemoveAll("sync"); err != nil {
		return fmt.Errorf("clear ./sync: %w", err)
	}
	for _, m := range mirrors {
		log := prefixLogger(m.sess.BundleID, multi)
		if err := mirror.PullReplace(m.sess.FS, m.userStyles, m.localDir, log); err != nil {
			return fmt.Errorf("[%s] initial pull: %w", m.sess.BundleID, err)
		}
	}

	// 5. Warn once, then print absolute watch paths.
	fmt.Print(warningBanner())
	for _, m := range mirrors {
		abs, _ := filepath.Abs(m.localDir)
		fmt.Printf("editing → %s  (watching for changes; Ctrl-C to stop)\n", abs)
	}

	// 6. Start a watcher per app; run until Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var wg sync.WaitGroup
	for _, m := range mirrors {
		w, err := mirror.NewWatcher(m.sess.FS, m.localDir, m.userStyles, prefixLogger(m.sess.BundleID, multi))
		if err != nil {
			return err
		}
		wg.Add(1)
		go func(w *mirror.Watcher, bundle string) {
			defer wg.Done()
			if err := w.Run(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] watcher error: %v\n", bundle, err)
			}
		}(w, m.sess.BundleID)
	}
	<-ctx.Done()
	wg.Wait()

	// 7. Closing reminder.
	fmt.Println("\nStopped. Reopen Lightroom so it rebuilds its preset index.")
	return nil
}
```

- [ ] **Step 4: Tidy modules, build, vet, test**

Run:
```bash
go mod tidy
go build ./...
go vet ./...
go test ./...
```
Expected: build/vet clean; all tests PASS (mirror, device, banner). No references to the deleted packages remain.

- [ ] **Step 5: Manual smoke check of the CLI surface**

Run:
```bash
go run ./cmd/lrpush --help
```
Expected: help shows a single `lrpush` command with **no** flags beyond cobra's built-in `-h/--help`, and no `devices`/`inspect`/`push`/`rm` subcommands.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(printf 'feat(cmd): collapse lrpush to single mirror+watch flow\n\nRemove inspect/push/rm/devices subcommands and their packages; bare\nlrpush now picks a device, mirrors each installed Lightroom app to\n./sync/{bundle-id}/userStyles, and live-syncs edits back until Ctrl-C.\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 6: docs — README, .gitignore

**Files:**
- Modify: `.gitignore`
- Rewrite: `README.md`

**Interfaces:** none (documentation only).

- [ ] **Step 1: Update `.gitignore`**

Replace the `_userStyles_backup` block so the file reads:

```gitignore
# Ephemeral local mirror (wiped at the start of every run)
/sync/

# built binary
/lrpush

# SDD controller scratch (ledger, briefs, review packages)
/.superpowers/

# macOS
.DS_Store
```

- [ ] **Step 2: Rewrite `README.md`**

```markdown
# lrpush

Mirror an iPhone/iPad Adobe Lightroom app's user presets (styles) to a local
folder over USB, then live-sync your local edits back to the device — using
Apple house_arrest + AFC via [go-ios](https://github.com/danielpaulus/go-ios).
No jailbreak, no tunnel.

## How it works

Lightroom mobile stores user presets inside its app container at
`Documents/{catalog}/settings-acr/userStyles/`. Running `lrpush`:

1. Picks a connected device (auto if one, arrow-key menu if several).
2. Detects every installed Lightroom app (probing `com.adobe.lrmobilephone`
   then `com.adobe.lrmobile`) and mirrors each one.
3. Pulls the device's `userStyles` down into `./sync/{bundle-id}/userStyles/`,
   replacing anything there.
4. Watches that local folder and pushes any change — new files, edits, and
   deletions — back to the device in real time until you press Ctrl-C.

`./sync/` is an ephemeral working copy: it is wiped at the start of every run
and left on disk afterward for inspection.

## Requirements

- macOS with the device connected via USB and **trusted**.
- Go 1.26+ to build.
- Dependencies: `github.com/danielpaulus/go-ios`, `github.com/spf13/cobra`,
  `github.com/charmbracelet/huh`, `github.com/fsnotify/fsnotify`,
  `golang.org/x/term`.

## Build

    make build        # produces ./lrpush
    # or: go build -o lrpush ./cmd/lrpush

## Use

    ./lrpush

Pick a device if prompted, pick a catalog if prompted, then **fully close
Lightroom** when the banner appears. Edit presets under the printed
`./sync/{bundle-id}/userStyles/` path; every change syncs to the device. Press
Ctrl-C to stop, then reopen Lightroom so it rebuilds its preset index.

### Safety

- Deletions mirror: removing a file/folder under `./sync/...` deletes it on the
  device. There are no backups.
- Close Lightroom while syncing; reopen it afterward.
- Presets pushed this way may appear only on the device and may not sync to
  Creative Cloud.

## Troubleshooting

**No device found:** connect via USB, unlock, and accept "Trust This Computer".

**Lightroom not found:** the app must be installed and expose file sharing.
lrpush recognises `com.adobe.lrmobilephone` (iPhone) and `com.adobe.lrmobile`
(iPad/universal).

**Changes don't appear in Lightroom:** fully close and reopen it so it re-reads
its preset index.
```

- [ ] **Step 3: Verify docs reference nothing removed**

Run:
```bash
grep -nE "inspect|/rm|push --| devices" README.md || echo "clean"
```
Expected: `clean` (no stale subcommand references).

- [ ] **Step 4: Commit**

```bash
git add README.md .gitignore
git commit -m "$(printf 'docs: README + gitignore for the mirror+watch flow\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Self-Review

**Spec coverage**
- Bare flagless flow → Task 5 (`run()`, no flags). ✓
- Device pick (0 error / 1 auto / >1 picker) → Task 5 step 3. ✓
- App detection by vend probe, mirror every installed, no bundle picker → Task 3 + Task 5. ✓
- Precedence order mobilephone→mobile → Global Constraints + Task 5 `lightroomBundleIDs`. ✓
- Per-app catalog select (1 auto / >1 picker) → Task 5 + `catalogPicker`. ✓
- Local root `./sync/{bundle-id}/userStyles/` → Task 5. ✓
- Wipe `./sync` at startup, not on exit → Task 5 step 4 (`os.RemoveAll("sync")`), no exit cleanup. ✓
- Pull-and-replace, per-file log → Task 1. ✓
- Warn once + absolute watch paths → Task 5 step 5. ✓
- Recursive fsnotify + 400ms debounce, mirror deletes, containment guard → Task 2 (`Reconcile`/`safeRel`) + Task 4 (`Watcher`). ✓
- Errors during watch logged, session survives → Task 2 (per-path log+continue), Task 4 (`reconcile error` log). ✓
- Concurrent multi-app sessions, bundle-id-prefixed logs → Task 5 (`prefixLogger`, goroutine per app). ✓
- Closing reopen-Lightroom reminder → Task 5 step 7. ✓
- Reuse detection session (no second vend) → Task 3 (`DetectSessions` returns the opened session), Task 5 uses it directly. ✓
- Remove inspect/push/rm/devices + packages → Task 5 step 1. ✓
- `.gitignore` adds `/sync/` → Task 6. ✓
- Testing via MemFS + temp dir; fsnotify untested, Reconcile tested → Tasks 1–4. ✓

**Placeholder scan:** none — every code step is complete.

**Type consistency:** `Reconcile(fs, localDir, deviceUserStyles, changed, log)` — same signature in Task 2 definition and Task 4 caller. `PullReplace(fs, deviceUserStyles, localDir, log)` — same in Task 1 and Task 5. `DetectSessions(udid, bundleIDs)` / `collectVendable(bundleIDs, probe)` — consistent Task 3 ↔ Task 5. `pickIndex(title, labels) (int, error)` — consistent Task 5 pickers.go ↔ root.go. `Session` fields `FS/Label/BundleID` and `Close()` used as defined in `internal/device/device.go`. ✓
