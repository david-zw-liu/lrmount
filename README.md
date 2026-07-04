# lrmount

Mount an iPhone/iPad Adobe Lightroom app's `Documents/` folder as an
ejectable volume in Finder, over USB — using Apple house_arrest + AFC via
an embedded localhost NFS server and macOS's built-in NFS client. No
jailbreak, no kernel extensions, nothing to install.

## How it works

Running `lrmount`:

1. Picks a connected USB device (auto if one, arrow-key menu if several).
2. Detects every installed Lightroom app (`com.adobe.lrmobilephone`, then
   `com.adobe.lrmobile`) and starts one embedded NFS server per app,
   bridging NFS operations straight to the device over AFC.
3. Mounts each app's `Documents/` at `/Volumes/<device> Lightroom` with
   the built-in macOS NFS client. User presets live under
   `<catalog>/settings-acr/userStyles/` (paths are printed at startup).
4. Waits. Eject a volume in Finder when you are done — macOS flushes every
   pending write to the device before the eject completes. When the last
   volume is ejected (or on Ctrl-C), lrmount exits.

Writes are write-through: every file operation is acknowledged only after
the device has confirmed it. There is no local cache to lose.

## Requirements

- macOS with the device connected via USB and **trusted**.
- Go 1.26+ to build.

## Build

    make build        # produces ./lrmount
    # or: go build -o lrmount ./cmd/lrmount

## Use

    ./lrmount

Pick a device if prompted, then **fully close Lightroom** on the device
(swipe it away in the app switcher) while volumes are mounted. Edit presets
in Finder under the printed paths. Eject in Finder when done, then reopen
Lightroom so it rebuilds its preset index.

### Safety

- Deletions and edits act directly on the device. There are no backups.
- Close Lightroom while volumes are mounted; reopen it after ejecting.
- Finder writes `.DS_Store` / `._*` files onto the device; Lightroom
  ignores them.
- Presets pushed this way may appear only on the device and may not sync
  to Creative Cloud.

## Troubleshooting

**No device found:** connect via USB, unlock, and accept "Trust This
Computer".

**Lightroom not found:** the app must be installed and expose file sharing.

**Mount fails with a permission error:** some configurations require a
reserved source port; see the `resvport` note in `internal/mountctl`.

**Volume shows as full:** the free-space numbers come from the device;
reconnect and rerun if they look stale.
