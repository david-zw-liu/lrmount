# lrmount

Mount an iPhone/iPad Adobe Lightroom app's `Documents/` folder as an
ejectable volume in Finder, over USB — using Apple house_arrest + AFC via
an embedded localhost NFS server and macOS's built-in NFS client. No
jailbreak, no kernel extensions, nothing to install.

## How it works

Running `lrmount`:

1. Picks a connected USB device (auto if one, arrow-key menu if several). If
   none is attached, it waits for one to appear.
2. Detects every installed Lightroom app (`com.adobe.lrmobilephone`, then
   `com.adobe.lrmobile`) and starts one embedded NFS server per app,
   bridging NFS operations straight to the device over AFC.
3. Mounts the device once at `/tmp/lrmount/<device>/` with the built-in
   macOS NFS client; the volume shows in Finder named after the device (the
   mountpoint is throwaway scratch — the data lives on the device). Inside it
   each Lightroom app is a subfolder (`Lightroom Mobile` /
   `Lightroom for iPad`) containing that app's `Documents/`. User presets
   live under `<app>/<catalog>/settings-acr/userStyles/` (paths are printed
   at startup).
4. Stays running. lrmount is resident: unplug the cable and it auto-ejects
   the volumes, then re-mounts them when you reconnect the device — it does
   not exit. Eject a volume in Finder when you are done (macOS flushes every
   pending write to the device before the eject completes). It exits only
   when you eject every volume in Finder or press Ctrl-C.

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
reserved client source port; lrmount automatically retries the mount once
with `resvport` when the first attempt fails. If it still fails after the
retry, please report it along with the mount output.

**Volume shows as full:** the free-space numbers come from the device;
reconnect and rerun if they look stale.
