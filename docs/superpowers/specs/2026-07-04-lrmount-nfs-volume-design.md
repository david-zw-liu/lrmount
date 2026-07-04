# lrmount: mount iOS Lightroom Documents as Finder volumes

Date: 2026-07-04
Status: approved design, pre-implementation

## Goal

Replace the mirror+watch flow (`./sync/` working copy + fsnotify) with direct
Finder volumes. For every connected iOS device and every Lightroom app on it,
mount that app's entire `Documents/` as a read-write volume in Finder. Each
volume is ejectable from Finder; ejecting guarantees all writes have reached
the device. When every volume is ejected (or on Ctrl-C), the program exits.

The project, binary, and module are renamed from `lrpush` to `lrmount`.

## Decisions (confirmed with the user)

- **Zero install**: no macFUSE/FUSE-T/kexts/app bundles. Built-in macOS
  mechanisms only.
- **Scope**: whole `Documents/` of each Lightroom app — one volume per
  (device, app) pair. No catalog picker; the banner prints each catalog's
  `settings-acr/userStyles` path as guidance.
- **Replaces** mirror+watch entirely. `internal/mirror` and `./sync/` are
  removed.
- **Lifecycle**: mount all at startup; Finder eject unmounts one volume;
  program exits when all volumes are gone or on Ctrl-C. No hotplug daemon.
- **Write guarantee**: eject must not complete until all data is on the
  device.

## Approach: embedded localhost NFSv3 server + built-in mount_nfs

A pure-Go NFSv3 server (`github.com/willscott/go-nfs`, Apache-2.0) runs on a
random localhost port per volume, bridging NFS operations to the device over
AFC (house_arrest). The volume is mounted with macOS's built-in NFS client:

    mount -t nfs -o port=P,mountport=P,tcp,vers=3,nolocks localhost:/ <mountpoint>

Precedent: rclone's `serve nfs`/`nfsmount` uses exactly this architecture on
macOS for the same reason (FUSE install burden on Apple Silicon).

Rejected alternatives (researched 2026-07):

- **WebDAV + mount_webdav**: zero-install but Finder's WebDAV client is slow,
  spams `._` AppleDouble requests, caches stale data; `x/net/webdav` is in
  maintenance mode. Fallback only.
- **macFUSE / FUSE-T**: requires third-party install (macFUSE's kext-free
  FSKit backend needs macOS 26+ for network-type filesystems anyway).
- **Apple FSKit**: requires a signed+notarized .app with a Swift appex; a
  plain CLI cannot host it; non-block-device support still immature.
- **NSFileProvider**: replication model, not passthrough; appears in the
  sidebar without eject semantics.
- **Pure-Go SMB server**: no production-quality implementation exists.

### AFC capability gap

The AFC protocol supports everything a filesystem backend needs (random-access
read/write via seek, rename, truncate, set-mtime — all implemented in
libimobiledevice's afcd client). go-ios v1.2.0 (latest) exposes only
sequential read/write, stat (without mtime), list, mkdir, remove. Its packet
codec is private, so we vendor our own minimal AFC client.

## Components

    cmd/lrmount        pick device(s) → build one Volume per (device, app) → wait
    internal/device    (as-is) device enumeration
    internal/locate    (simplified) detect installed Lightroom apps; catalog
                       discovery kept only to print userStyles hint paths
    internal/afc       NEW: own AFC client — copied from go-ios afc (MIT) and
                       extended with Seek, Rename, Truncate, SetFileTime,
                       st_mtime parsing, and the house_arrest vend handshake
                       (connect service, send VendDocuments plist, wrap conn)
    internal/afcfs     CHANGED: FS interface upgraded from whole-file Pull/Push
                       to random access: Open(flags) → handle with
                       ReadAt/WriteAt/Truncate/Close; Rename; SetMtime; Stat
                       with ModTime. MemFS upgraded in lockstep for tests.
    internal/nfsgate   NEW: afcfs.FS → billy.Filesystem adapter + go-nfs server
                       per volume (nfshelper NullAuth + CachingHandler for
                       stable file handles), bound to 127.0.0.1:0
    internal/mountctl  NEW: run /sbin/mount, poll statfs (~1s) to detect
                       unmount, `diskutil unmount` on shutdown
    internal/mirror    REMOVED (watcher + mirror + tests)

Volume naming: `/Volumes/<device name> Lightroom`, with a bundle-id suffix
when a device has both Lightroom apps, and a numeric suffix on collisions. If
creating the mountpoint under `/Volumes` fails (permissions), fall back to a
directory under the user's home — a mounted NFS volume shows in Finder's
sidebar with an eject control regardless of mountpoint location.

## Write guarantee and lifecycle

**Core rule: the server is write-through with zero write buffering.** Every
NFS WRITE is synchronously translated to AFC seek+write and only acknowledged
after the device's afcd replies. The server never holds data the device does
not have.

Eject then works with the grain of macOS:

1. User ejects in Finder → the macOS NFS client flushes dirty pages
   (WRITE+COMMIT) to our server before unmounting.
2. Each write is on the device before it is acknowledged, so
   **unmount success ⇒ all data is on the device**.
3. We detect the unmount (statfs poll), close that volume's AFC connection
   and NFS server. When the last volume goes, the process exits.
4. If another app holds files open, Finder refuses to eject — desired
   protection, not a defect.

Mount uses the default hard semantics (no `soft`): a transient stall retries
instead of silently dropping data. `nolocks` because go-nfs does not
implement NLM.

**Ctrl-C**: run `diskutil unmount` per volume (triggers the same flush).
Volumes that are busy are listed and waited on; a second Ctrl-C forces
(`diskutil unmount force`). Invariant: a volume's NFS server is shut down
only after its unmount succeeds — never before.

**Implementation-time assumption to verify first**: go-nfs must call the
billy backend synchronously and only then reply to WRITE (with FILE_SYNC
stability). Read its source; if it buffers, enforce write-through in the
adapter or use the lower-level handler interface.

## Error handling

- **Startup isolation**: a volume that fails to mount (house_arrest denied,
  port taken, mountpoint creation failed) is skipped with a warning; others
  proceed. Exit non-zero only if all volumes fail.
- **AFC connection death** (unplug, device reboot): subsequent NFS ops on
  that volume return EIO; on detecting the dead connection, force-unmount the
  stale mountpoint, print a warning listing writes that were in flight. No
  auto-reconnect — replug and rerun.
- **Stale state at startup**: dead mounts from a crashed previous run are
  force-unmounted; leftover empty mountpoint directories are reused.
- **Finder noise files** (`.DS_Store`, `._*`): passed through untouched.
  Swallowing them at the FS layer would create "write succeeded but file
  absent" semantics — debugging hell. Documented in README instead.
- **Concurrent device-side changes**: NFS client attribute caching delays
  visibility by a few seconds. The banner already tells users to close
  Lightroom during the session; documented, not mitigated.
- **Case sensitivity**: AFC/iOS is case-insensitive/case-preserving, matching
  macOS defaults. No special handling.

## Testing

- **internal/afc**: unit tests over packet encode/decode with golden bytes;
  real-device behavior of Seek/Rename/SetFileTime verified via a manual
  checklist attached to the PR.
- **internal/afcfs**: MemFS gains random access; all higher layers keep
  testing against the FS interface, never a real device.
- **internal/nfsgate**: highest-value tests — start a real go-nfs server over
  MemFS in-process and exercise read/write/rename/delete/mtime round-trips.
  Write-through is asserted with an interposing FS that records that data hit
  MemFS before WRITE was acknowledged. A `darwin_e2e`-tagged test does a real
  `mount` for manual runs.
- **internal/mountctl**: unmount detection tested with an injected statfs
  func.
- **Manual acceptance checklist** (in the PR): single device mount → edit
  preset in Finder → eject → relaunch Lightroom and confirm; dual-app iPad;
  unplug mid-session; eject refused while a file is held open.

## Rename / migration plan

Executed as part of implementation:

1. `cmd/lrpush` → `cmd/lrmount`; Makefile target and README updated; old
   `lrpush` binary removed from the repo root and .gitignore updated.
2. go.mod module path → `github.com/david-zw-liu/lrmount` (also fixes the
   current `davidliu` account mismatch); all imports rewritten.
3. GitHub repo already renamed to `david-zw-liu/lrmount` by the user; origin
   remote URL updated accordingly (done 2026-07-04).
4. Local folder `~/Desktop/ios-lightroom-presets-importer` → `~/Desktop/lrmount`
   as the final step (the running session's paths go stale after the move).
