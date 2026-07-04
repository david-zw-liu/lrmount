# lrmount

[中文說明](#中文說明) | [English](#english)

---

## 中文說明

透過 USB 將 iPhone / iPad 上 Adobe Lightroom App 的 `Documents/` 資料夾，
掛載成 Finder 中可退出（Eject）的磁碟區 —— 使用 Apple 的 house_arrest + AFC，
搭配內嵌的 localhost NFS 伺服器與 macOS 內建的 NFS 客戶端。
不需越獄、不需 kernel extension、不需安裝任何東西。

### 運作方式

`lrmount` 以常駐（daemon）方式執行，會自動掛載所有以 USB 連接的裝置 ——
不會跳出提示、也沒有選單。只透過 Wi-Fi 連線的裝置會被忽略。
它每 0.5 秒輪詢一次 usbmux，並持續核對目前掛載的狀態：

1. **偵測到新裝置** → 找出裝置上所有已安裝的 Lightroom App
   （先找 `com.adobe.lrmobilephone`，再找 `com.adobe.lrmobile`），
   透過同一個內嵌 NFS 伺服器提供內容，並以 macOS 內建 NFS 客戶端掛載。
   磁碟區會以裝置名稱出現在 Finder 中。
2. **磁碟區內部**：每個 Lightroom App 是一個子資料夾
   （`Lightroom Mobile` / `Lightroom for iPad`），內容即該 App 的
   `Documents/`。使用者預設集（presets）位於
   `<app>/<catalog>/settings-acr/userStyles/`（完整路徑會在啟動時印出）。
3. **拔掉裝置** → 其磁碟區會自動退出，但 lrmount 會繼續執行，
   重新接上時會再次掛載。
4. **在 Finder 中退出磁碟區** → 只卸載該裝置；當最後一個掛載中的裝置
   被退出後，lrmount 隨即結束。Ctrl-C 也會結束程式，並先卸載所有磁碟區。

掛載點是每次執行時隨機產生的暫存目錄
（`$TMPDIR/lrmount-<random>/<device>-<random>/`），結束時會刪除 ——
資料本體都在裝置上，且 Finder 顯示的磁碟區名稱取自 NFS 分享名稱，
與這個路徑無關。

寫入採 write-through（直寫）：每個檔案操作都要等裝置確認後才回報完成。
沒有本機快取可以遺失 —— 唯一例外是拔線瞬間 macOS NFS 客戶端已緩衝
但尚未送出的寫入，那部分會遺失，這是拔線本身無法避免的。
想要乾淨地寫完，請在 Finder 中退出磁碟區。

### 需求

- macOS，裝置以 USB 連接且已**信任**這台電腦。
- 建置需要 Go 1.26+。

### 建置

    make build        # 產生 ./lrmount
    # 或：go build -o lrmount ./cmd/lrmount

### 使用

    ./lrmount        # 或：make（先建置，再執行）

所有 USB 裝置都會自動掛載。掛載期間請在裝置上**完全關閉 Lightroom**
（在 App 切換器中把它滑掉），然後在 Finder 中依啟動時印出的路徑編輯
預設集。完成後在 Finder 中退出該裝置的磁碟區；最後一個被退出時
lrmount 就會結束（Ctrl-C 也可以）。之後重新打開 Lightroom，
讓它重建預設集索引。

#### 安全注意事項

- 刪除與修改都直接作用在裝置上，**沒有備份**。
- 掛載期間關閉 Lightroom；退出磁碟區後再重新開啟。
- Finder 會在裝置上寫入 `.DS_Store` / `._*` 檔案；Lightroom 會忽略它們。
- 以這種方式放入的預設集可能只出現在該裝置上，不一定會同步到
  Creative Cloud。

### 疑難排解

**找不到裝置：** 請以 USB 連接、解鎖裝置，並點選「信任這部電腦」。

**找不到 Lightroom：** App 必須已安裝，且開放檔案共享。

**掛載時出現權限錯誤：** 某些環境要求使用保留的客戶端來源連接埠；
第一次掛載失敗時，lrmount 會自動帶上 `resvport` 重試一次。
若重試後仍失敗，請連同掛載輸出一起回報。

**磁碟區顯示已滿：** 剩餘空間數字來自裝置本身；若看起來過期，
請重新連接並重新執行。

---

## English

Mount an iPhone/iPad Adobe Lightroom app's `Documents/` folder as an
ejectable volume in Finder, over USB — using Apple house_arrest + AFC via
an embedded localhost NFS server and macOS's built-in NFS client. No
jailbreak, no kernel extensions, nothing to install.

### How it works

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

### Requirements

- macOS with the device connected via USB and **trusted**.
- Go 1.26+ to build.

### Build

    make build        # produces ./lrmount
    # or: go build -o lrmount ./cmd/lrmount

### Use

    ./lrmount        # or: make (builds, then runs)

Every USB device mounts automatically. **Fully close Lightroom** on a device
(swipe it away in the app switcher) while its volume is mounted, then edit
presets in Finder under the printed paths. Eject a volume in Finder when you
are done with that device; lrmount quits once the last one is ejected
(Ctrl-C also quits). Reopen Lightroom afterward so it rebuilds its preset
index.

#### Safety

- Deletions and edits act directly on the device. There are no backups.
- Close Lightroom while volumes are mounted; reopen it after ejecting.
- Finder writes `.DS_Store` / `._*` files onto the device; Lightroom
  ignores them.
- Presets pushed this way may appear only on the device and may not sync
  to Creative Cloud.

### Troubleshooting

**No device found:** connect via USB, unlock, and accept "Trust This
Computer".

**Lightroom not found:** the app must be installed and expose file sharing.

**Mount fails with a permission error:** some configurations require a
reserved client source port; lrmount automatically retries the mount once
with `resvport` when the first attempt fails. If it still fails after the
retry, please report it along with the mount output.

**Volume shows as full:** the free-space numbers come from the device;
reconnect and rerun if they look stale.
