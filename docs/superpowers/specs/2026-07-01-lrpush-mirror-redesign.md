# lrpush redesign: continuous mirror + live watcher

Date: 2026-07-01

## Summary

Replace lrpush's discrete subcommands (`inspect` / `push` / `rm` / `devices`)
with a single, flagless flow. Running bare `./lrpush`:

1. Picks a connected device.
2. Detects every installed Lightroom app on it.
3. For each app, mirrors the device's `userStyles` down into a local folder
   `./sync/{bundle-id}/userStyles/` (pull-and-replace).
4. Warns the user to close Lightroom, then watches each local folder and
   auto-pushes any change back up to the device in real time (including
   deletions), until the user hits Ctrl-C.
5. On exit, deletes everything under `./sync/`.

The local folder is an ephemeral working copy: edit presets there and they land
on the device, but nothing persists between runs (`./sync/` is cleared on both
startup and exit). Direction is never ambiguous — **device → local at startup,
local → device during the session** — so there is no conflict resolution.

## Non-goals

- No two-way merge, no persistent sync state, no digesting on startup (startup
  is a wholesale pull-and-replace).
- No backups (the startup pull is non-destructive to the device; local is
  disposable and re-pullable). The user is trusted after a single warning.
- No Creative Cloud sync. Presets pushed this way live on the device.

## Invocation

Bare `./lrpush`. **Zero flags.** `--udid`, `--bundle-id`, `--path-prefix`,
`--catalog` are all removed. Everything resolves by auto-detection or an
interactive picker.

## Flow

### 1. Device selection
`device.List()` returns connected USB devices (deduped by udid).

- 0 devices → error and exit.
- 1 device → auto-select.
- >1 → arrow-key picker (huh) listing name / model / udid.

### 2. Lightroom app detection (no bundle picker)
For the chosen device, enumerate installed apps via `installationproxy` and
intersect with the known Lightroom bundle ids, probed in this precedence order:

1. `com.adobe.lrmobilephone` (iPhone)
2. `com.adobe.lrmobile` (iPad / universal)

- 0 installed → error and exit ("Lightroom not found on this device").
- ≥1 installed → **mirror every one of them.** Each installed app becomes an
  independent mirror session with its own local root and its own watcher, all
  running concurrently. Because each app's local tree is namespaced by bundle
  id (`./sync/{bundle-id}/`), there is no collision and therefore no bundle
  picker.

In the common case exactly one Lightroom app is installed, so this degenerates
to a single session.

### 3. Per-app setup
For each installed Lightroom app:

1. Connect house_arrest (`VendDocuments`, existing logic) to that bundle id.
2. Locate the userStyles target: `locate.DocumentsRoot` → `locate.FindCatalogs`
   → `locate.SelectCatalog`.
   - 1 catalog → auto-select.
   - >1 → arrow-key picker.
3. Compute the local root: `./sync/{bundle-id}/userStyles/`.

Catalog pickers (if any) are resolved up front, before any watcher starts, so
the interactive phase is over before mirroring begins.

### 4. Pull-and-replace (device → local)
First, remove `./sync/` entirely (clearing any leftovers from a prior crashed
run). Then, for each app's local root:

1. Create `./sync/{bundle-id}/userStyles/`.
2. Recursively pull the entire device `userStyles` tree into it.
3. Log per file.

The device is never written during this phase. A pull failure aborts that
session with an error (re-run to retry — local is just a mirror).

### 5. Warn once
Before starting the watchers, print the existing "fully close Lightroom now,
reopen when you're done so it rebuilds its preset index" banner a single time.
No per-change backups.

### 6. Watch (local → device)
For each app, a recursive fsnotify watcher over its local `userStyles` tree:

- Watches are added on the root and every subfolder, and dynamically
  added/removed as folders appear/disappear.
- Raw events are coalesced with a debounce (~400 ms) into a deduped set of
  changed **relative paths**.
- Each debounced batch is reconciled to the device via a pure function
  `Reconcile(fs, localDir, deviceUserStyles, changedRelPaths, out)`:
  - local path exists as file → `MkDir -p` parent on device + `PushFile`.
  - local path exists as dir → ensure the dir and push its contents (walk).
  - local path missing → `RemoveAll` on device (mirror the deletion).
  - rename → surfaces as delete(old) + create(new); handled by the above.
- Every device path is passed through the existing path-containment guard so a
  local path can never escape `userStyles` on the device.

fsnotify stays at the edge (collect paths + debounce only). The device-mutating
logic lives entirely in `Reconcile`, which is unit-testable without fsnotify.

### 7. Shutdown
Run until Ctrl-C (SIGINT). On signal, stop all watchers, close all AFC sessions
cleanly, then delete everything under `./sync/`. The cleanup runs on normal exit
paths (signal or fatal error after mirroring started); a hard crash is covered
by the startup wipe in step 4.

## Error handling

- No device / no Lightroom app → error and exit before any mirroring.
- Initial pull failure → that session aborts with an error.
- During watching, a push/delete error is **logged and the session keeps
  running**. A device disconnect surfaces as repeated op errors rather than a
  crash. (Whether to auto-exit on a hard disconnect is left to implementation
  judgement; logging-and-continuing is the baseline.)
- Concurrent sessions are independent: one app's failure does not stop another.

## Packages

**Keep**
- `internal/afcfs` (+ `MemFS`) — AFC filesystem boundary and in-memory fake.
- `internal/device` — `List`, `Connect`, `DescribeDevice`; **add** installed-app
  detection (installationproxy intersect with the Lightroom bundle ids).
- `internal/locate` — DocumentsRoot / FindCatalogs / SelectCatalog.

**New**
- `internal/mirror` —
  - `PullReplace(fs, deviceUserStyles, localDir, out) error` — wipe local, pull
    the whole device tree.
  - `Reconcile(fs, localDir, deviceUserStyles, changedRelPaths, out) error` —
    the pure device-mutating core (push/delete for a set of changed paths).
  - `Watcher` — wraps fsnotify: recursive watches, debounce, calls `Reconcile`.

**Remove**
- Packages: `internal/inspect`, `internal/pushsync`, `internal/rmsync`.
- cmd files: `inspect.go`, `push.go`, `rm.go`, `devices.go`, `interactive.go`,
  and the rm-select `tui.go`. The device picker and catalog picker move into a
  small shared file under `cmd/lrpush`.

**Dependencies**
- Add `github.com/fsnotify/fsnotify`.
- Keep `cobra` (single command + `--help`), `huh` (device/catalog pickers),
  `golang.org/x/term`.

## Local folder layout

```
./sync/                         (wiped on startup and on exit)
  {bundle-id}/
    userStyles/
      <preset groups and loose files, mirrored from the device>
```

`.gitignore` adds `/sync/` (defensive — the folder should not survive a normal
run, but a crash could leave it behind).

## Testing

- `PullReplace`: `MemFS` device side + a temp dir local side; asserts local is
  wiped and repopulated to exactly match the device tree.
- `Reconcile`: `MemFS` device + temp local; table-driven cases for create,
  modify, delete, new nested dir, rename (delete+create), and a
  path-containment-escape attempt (must be refused).
- Device app detection: unit-test the intersection/precedence logic against a
  fake installed-apps list.
- fsnotify itself is not unit-tested; the reconcile logic is exercised directly.

## Rationale for the removed subcommands

`inspect` (listing) is subsumed by the local folder itself — after startup you
just look at `./sync/{bundle-id}/userStyles/`. `push`/`rm` are
subsumed by editing that folder while the watcher runs. `devices` is subsumed by
the startup device picker. Git history preserves the removed code.
