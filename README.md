# lrpush

Push local Lightroom presets onto an iPhone's Adobe Lightroom mobile app
(`com.adobe.lrmobile`) over USB, using Apple house_arrest + AFC via
[go-ios](https://github.com/danielpaulus/go-ios). No jailbreak, no tunnel.

## How it works

Lightroom mobile stores user presets (styles) inside its app container at
`Documents/{catalog}/settings-acr/userStyles/`. lrpush connects to that
container over USB and copies presets in.

## Requirements

- macOS with the iPhone connected via USB and **trusted** (tap "Trust This
  Computer" on the phone; unlock once so the pairing is valid).
- Go 1.26+ to build.
- Dependencies (Go modules): `github.com/danielpaulus/go-ios`,
  `github.com/spf13/cobra`, `github.com/charmbracelet/huh` (rm -i menu),
  `golang.org/x/term`.

## Build

    make build        # produces ./lrpush
    # or: go build -o lrpush ./cmd/lrpush

Other targets: `make test`, `make vet`, `make fmt`, `make clean`.

## Safety first

- Commands are **dry-run by default**. Add `--commit` to actually write.
- Before any `--commit`, fully **close Lightroom** on the iPhone (swipe it
  away). Re-open it afterwards. Otherwise the app may overwrite the changes.
- `push` backs up the whole `userStyles` to `./_userStyles_backup/<timestamp>/`
  before writing; `rm` backs up each target before deleting.
- Presets pushed this way may appear only on the device and may not sync to
  Creative Cloud.

## Workflow

### 0. List devices (when more than one is attached)

    ./lrpush devices

Prints each connected device's udid, name, model, and iOS version. Use a udid
with `--udid` to target a specific device (the default "first device" is
non-deterministic when several are attached).

### 1. Inspect

    ./lrpush inspect

Lists catalogs containing `settings-acr`, selects the userStyles target, and
lists `userStyles`' first-level contents (its preset groups and loose files).

### 2. Push (dry-run, then commit)

A folder's **contents** are mirrored into userStyles (the source folder name is
NOT added as a wrapper level). Each top-level subfolder becomes a preset group
directly under userStyles, and top-level loose files land directly in userStyles:

    ./source/A/        -> userStyles/A/
    ./source/B/        -> userStyles/B/
    ./source/xxx.xmp   -> userStyles/xxx.xmp

Each top-level subfolder that already exists on the device is replaced wholesale
(old group removed, backed up first); loose files overwrite. Any existing
userStyles content the source does not mention is left untouched. The whole
userStyles is backed up before any change.

    # preview
    ./lrpush push --source ./source
    # apply
    ./lrpush push --source ./source --commit

Single file (lands at `userStyles/foo.xmp`):

    ./lrpush push --source ./foo.xmp --commit

### 3. Remove

Paths are relative to userStyles; multiple allowed; files or folders. Paths that
escape userStyles (`..`, absolute, `.`) are refused.

    ./lrpush rm my-presets foo.xmp            # dry-run
    ./lrpush rm my-presets foo.xmp --commit   # apply (backs up first)

Interactive multi-select (pick from userStyles' first-level entries, confirm,
then back up + delete — no `--commit` needed, the confirmation is the gate):

    ./lrpush rm -i

## Troubleshooting

**Multiple devices connected:** `lrpush` uses the first USB device by default,
which is non-deterministic when several are attached. Run `lrpush devices` to
list udids, then pass `--udid <udid>` to target a specific one.

**`InstallationLookupFailed` / lockdown errors:** make sure the device is
unlocked and trusted (accept the "Trust This Computer" prompt). `lrpush` opens
the app's documents container via house_arrest `VendDocuments` (falling back to
`VendContainer`), so the target app must be installed and expose file sharing.

**Pushed presets don't appear in Lightroom:** fully close Lightroom (swipe it
away) before pushing and reopen it after, so it re-reads its preset index.
Presets pushed this way may not sync to Creative Cloud.

## Flags

- `--udid` — target device (default: first USB device)
- `--bundle-id` — default `com.adobe.lrmobile`
- `--path-prefix` — override AFC root prefix if auto-detection is wrong
- `--catalog` — pick catalog by name (non-interactive; otherwise a menu appears
  when multiple catalogs exist)
- `--source` — (push) local file or folder; a folder's contents are mirrored
  into userStyles
- `-i`, `--interactive` — (rm) pick targets from a multi-select menu, confirm,
  then back up + delete
- `--backup-dir`, `--commit` — see Safety
