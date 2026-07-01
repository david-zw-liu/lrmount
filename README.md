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
and left on disk afterward for review.

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
