# lrmount

Mount an iPhone/iPad Adobe Lightroom app's `Documents/` folder as an
ejectable volume in Finder, over USB — using Apple house_arrest + AFC via
an embedded localhost NFS server and macOS's built-in NFS client. No
jailbreak, no kernel extensions, nothing to install.

## How it works

`lrmount` runs as a resident daemon and mounts every USB-connected device —
no prompt, no menu. Devices reachable only over Wi-Fi are ignored. It polls
usbmux twice a second and reconciles what is mounted:

1. **New device** → detects every installed Lightroom app on it
   (`com.adobe.lrmobilephone`, then `com.adobe.lrmobile`), serves them
   through one embedded NFS server, and mounts it with the built-in macOS
   NFS client. The volume appears in Finder named after the device.
2. **Inside the volume**, each Lightroom app is a subfolder (`Lightroom
   Mobile` / `Lightroom for iPad`) containing that app's `Documents/`. User
   presets live under `<app>/<catalog>/settings-acr/userStyles/` (the full
   paths are printed at startup).
3. **Unplug a device** → its volume is auto-ejected, but lrmount keeps
   running and re-mounts it if you reconnect.
4. **Eject a volume in Finder** → just that device is unmounted; lrmount
   quits once the last mounted device is ejected. Ctrl-C also quits,
   unmounting everything first.

The mountpoint is throwaway scratch under a random per-run directory
(`$TMPDIR/lrmount-<random>/<device>-<random>/`), removed on exit — the data
lives on the device, and Finder takes the volume name from the NFS share,
not this path.

Writes are write-through: every file operation is acknowledged only after
the device has confirmed it. There is no local cache to lose — except writes
the macOS NFS client had buffered but not yet sent when a cable is pulled;
those are lost, which is inherent to unplugging. Eject in Finder for a clean
flush.

## Requirements

- macOS with the device connected via USB and **trusted**.
- Go 1.26+ to build.

## Build

    make build        # produces ./lrmount
    # or: go build -o lrmount ./cmd/lrmount

## Use

    ./lrmount        # or: make (builds, then runs)

Every USB device mounts automatically. **Fully close Lightroom** on a device
(swipe it away in the app switcher) while its volume is mounted, then edit
presets in Finder under the printed paths. Eject a volume in Finder when you
are done with that device; lrmount quits once the last one is ejected
(Ctrl-C also quits). Reopen Lightroom afterward so it rebuilds its preset
index.

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
reserved client source port; lrmount automatically retries the mount once
with `resvport` when the first attempt fails. If it still fails after the
retry, please report it along with the mount output.

**Volume shows as full:** the free-space numbers come from the device;
reconnect and rerun if they look stale.
