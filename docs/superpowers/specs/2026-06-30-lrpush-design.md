# lrpush — iOS Lightroom Preset 批次推送工具 設計文件

- 日期：2026-06-30
- 狀態：設計確認中
- 語言/工具鏈：Go 1.26（darwin/arm64）

## 1. 目標

一個 Go CLI，把本機 Lightroom preset（styles）透過 USB（Apple house_arrest + AFC，免越獄、免 tunnel）批次推進 iPhone 上 Adobe Lightroom mobile（bundle id `com.adobe.lrmobile`）的 app container 內 `Documents/{numbers}/settings-acr/userStyles/`。

核心走 `github.com/danielpaulus/go-ios` 當**函式庫**（不 shell out）：
- `ios`：`DeviceEntry`、`ListDevices`/`GetDevice`
- `ios/house_arrest`：`house_arrest.New(device, bundleID) -> *afc.Client`
- `ios/afc`：`ListFiles`/`GetFileInfo`/`Pull`/`Push`/`MkDir`/`RemoveAll`/`TreeView` 等

> 確切 method 名以 pkg.go.dev 最新 API 為準，實作時先查再寫。

## 2. 兩個未知數（不寫死，靠 inspect 取得事實）

1. **house_arrest 的 AFC root 是 container 還是 Documents？**
   `inspect` 會自動偵測：列根目錄，若根下有 `Documents/` 子目錄 → root 是 container，路徑前綴用 `Documents/{numbers}/...`；否則 root 即 Documents，前綴用 `{numbers}/...`。可用 `--path-prefix` 手動覆寫當逃生口。
2. **userStyles 內現有檔案的真實副檔名/格式。**
   `inspect` 會 pull 一個現有檔到本機讓我們確認命名規則與副檔名/格式（肉眼比對，確保準備的來源檔格式與裝置一致）。push 照來源資料夾/檔案的內容直接推。

### 函式庫限制（已查證）

go-ios v1.2.0（`go get @latest` 取得）的 `afc.FileInfo` 只有 `Name / Type / Mode / Size / LinkTarget`，**沒有修改時間**。其 `Stat`（`client.go:262-315`）解析 AFC 回應時只取 `st_ifmt / st_size / st_mode / st_linktarget`，**丟棄 `st_mtime`**，且底層封包原語（`sendPacket`/`readPacket`/`fileInfo` opcode）皆未匯出，無法在不 fork 的情況下自行重作 stat 取 mtime。

→ **後果：原 spec「依資料夾 mtime 取最新」無法用 stock library 達成。** 改用 §6 的選路策略。

### 實機調查結果（2026-06-30，iPad16,1 / iPadOS 27.0 / com.adobe.lrmobile）

實機跑 inspect 與診斷後確認以下事實（取代上面兩個「未知數」的假設）：

1. **必須用 `VendDocuments`，不能用 `VendContainer`。**
   go-ios 的 `house_arrest.New` 送的是 `VendContainer`，本機回 `InstallationLookupFailed`（即使 `installationproxy` 確認 app 已安裝、`UIFileSharingEnabled=true`）。改送 `VendDocuments` → `Status=Complete`，AFC 可用。
   → **device 層不能直接用 `house_arrest.New`**，要自己送 `VendDocuments`（可保留 `VendContainer` 當 fallback），再 `afc.NewFromConn`。go-ios 的 `ios.ConnectToService` / `ios.NewPlistCodec` / `afc.NewFromConn` 皆已匯出，可自行組。
2. **AFC root 無法直接列。** `List("")` / `List("/")` / `List(".")` 全回 afc error code 10（NOT FOUND），只有具名子目錄（如 `List("Documents")`）可列。
   → 原 `DocumentsRoot` 用 `fs.List("")` 偵測前綴的做法**失效**。改為：預設前綴 `Documents`（用 `List("Documents")` 探測確認），`--path-prefix` 仍可覆寫。
3. **真實 target：** `Documents/0355bf64576a4f8da2aef4d5e2746bba/settings-acr/userStyles/`。`settings-acr` 下還有 `userPrefs/`（RawDefaults.xmp、FavoriteStyles.xmp）。
4. **userStyles 不是平的**：底下是**多個 preset 群組子資料夾**（如 `C4-英泽君经典富士Vivid250D/`，共 13 個），每個資料夾內是一堆 `.xmp`。→ 證實「保留來源資料夾層級」的設計正確。
5. **preset 檔是標準 Adobe XMP**（`.xmp`、純文字，每檔含 `crs:PresetType` / `crs:UUID` / `crs:Cluster`）。
6. **⚠️ userStyles 根有一個 `Index.dat`（約 505 KB，二進位）**：版本(4B) + 數量(4B) + 重複 [長度(4B)][路徑字串] 結構，列出所有 preset 的相對路徑（含內建 `AppBundle-FolderPlaceHolder/Adobe/Presets/...`）。
   Index.dat 經確認**含使用者自己的 preset**（"英泽君"×842、"Vivid250D"×84、"C4-"×44，每筆有 LookName/LookUUID/PresetName/PresetType/SortName/Supports 等 metadata），不只內建。

   **決策（使用者拍板）：採方案 B —— push 只丟檔案、不動 Index.dat，由 Lightroom 自己重建。** 因此 push 設計（§5）維持原樣，不做 Index.dat 解析或刪除。實際是否成功重建，於第一次實機 push 測試時驗證；若 Lightroom 未自動重建，再回頭評估刪除/改寫 Index.dat。

## 3. 架構

```
cmd/lrpush/main.go          // cobra root + 全域旗標 + 三個子指令註冊
internal/device/            // ListDevices/GetDevice、--udid 解析、取第一台
internal/afcfs/             // 薄封裝 afc.Client：Tree/List/Stat/Pull/Push/MkDir/RemoveAll、遞迴 pull/push helper
internal/locate/            // 路徑選擇：找含 settings-acr 的資料夾、挑 mtime 最新、組 userStyles target
internal/inspect/           // dump 目錄樹、偵測 AFC root、列候選、pull 樣本檔
internal/pushsync/          // 備份 + 推送流程（合併語意）
internal/rmsync/            // 備份 + 刪除流程
```

每個 internal 套件單一職責、可獨立測試。`afcfs` 是唯一直接碰 afc.Client 的層，其餘走它的介面。

## 4. CLI

CLI 框架：**cobra + 子指令**。

### 全域旗標
- `--udid string`：指定裝置；預設取第一台 USB 裝置。
- `--bundle-id string`：預設 `com.adobe.lrmobile`。
- `--path-prefix string`：覆寫 AFC root 前綴（自動偵測失敗時用）。

### `lrpush inspect`
調查指令，可重複跑，也是 push/rm 在定位 target 時的共用邏輯來源。
- dump house_arrest AFC root 的目錄樹。
- 自動偵測 AFC root 是 container 還是 Documents。
- 在 Documents 下列出所有子資料夾，篩出含 `settings-acr` 子目錄者，依 mtime 列出候選並標明會採用最新的哪一個。
- pull 一個現有 userStyles 檔到本機（`./_inspect_sample/`）讓使用者確認副檔名/格式。
- 旗標：`--sample int`（pull 幾個樣本，預設 1）、`--no-sample`。

### `lrpush push`
主流程，**預設 `--dry-run`**，要 `--commit` 才實際寫入。
- 旗標：
  - `--source string`：本機來源，**可為單一檔案或資料夾**。資料夾則把其內容鏡射進 userStyles（見 §5）。
  - `--backup-dir string`：預設 `./_userStyles_backup/<timestamp>/`。
  - `--dry-run`（預設 true）/ `--commit`。
- 行為見 §5。

### `lrpush rm`
從裝置 userStyles 刪檔/刪資料夾，**預設 `--dry-run`**，要 `--commit`。
- 用法：`lrpush rm <path>...`，路徑皆**相對 userStyles 根**，可一次多個，檔與資料夾皆可。
- `--backup-dir string`：刪前先把目標 pull 到備份目錄（預設同 push 的備份位置慣例）。
- 找不到的路徑：印 warning 並跳過該目標，不中斷其餘。
- **安全防護**：拒絕逃出 userStyles 的路徑（絕對路徑、`..`、`.`），這類目標標記 unsafe、不刪並回非零結束碼。
- **`--interactive` / `-i`（互動多選）**：不需位置參數；列出 userStyles **第一層**的目錄與檔案成編號選單，使用者多選（如 `1 3 5` 或 `all`），印出選中項並要求輸入 `yes` 確認，確認後**先備份再刪除**（互動模式自帶確認關卡，不需 `--commit`）。選空或未確認則中止。

## 5. push 語意（關鍵）

定位 target：依 §6 找到 `userStyles`。

**來源映射（contents-push，鏡射 source 頂層進 userStyles；不包 source basename 那層）：**
- 來源是資料夾 `./source/`（結尾有無 `/` 皆同）→ 把 source 的**內容鏡射進 userStyles**：
  - source 頂層每個**子資料夾** `A/`、`B/` → `userStyles/A/`、`userStyles/B/`，底下遞迴保留結構。
  - source 頂層每個**零散檔** `xxx.xmp` → `userStyles/xxx.xmp`。
  - 即每個檔 `source/<rel>` → 裝置 `userStyles/<rel>`（`<rel>` 為相對 source 的路徑，以 `/` 組裝）。
- 來源是單一檔案 `foo.xmp` → 推到 `userStyles/foo.xmp`。

> 此取代了舊版「包 source basename 一層」（`my-presets/` → `userStyles/my-presets/`）的設計。因裝置 userStyles 是扁平的一層 group 結構，鏡射頂層才能讓每個 group 直接落在 userStyles 下。

**對既有內容的處理：**
- userStyles 底下**其他**既有檔案/資料夾（source 沒有的、Lightroom 自己的 preset）→ **永遠不碰，完全保留**。
- source 頂層**每個子資料夾**對應的 `userStyles/<name>` 若已存在 → **先 `RemoveAll` 整個刪掉舊 group，再整包 push 新的**（per-group wholesale replace，不逐檔合併，舊檔不殘留）。使用者明確選擇；刪前已備份。
- source 頂層**零散檔**對應的 `userStyles/<file>` 若同名已存在 → 直接覆寫。
- 單一檔案來源若同名已存在 → 直接覆寫。

**順序（commit 模式）：**
1. 印警告橫幅（見 §8）。
2. 把**整個 userStyles** pull 到 `--backup-dir`。
3. 對每個「要取代的頂層子資料夾」`userStyles/<name>`，若已存在 → `RemoveAll`（全部刪完再進下一步，避免被緊接的 push 重建後又被刪）。
4. 逐檔遞迴 `MkDir` 父目錄 + `PushFile`，逐檔印結果（OK/fail）。

dry-run 模式：只印「選到的 target、會備份整個 userStyles、會被整個取代的頂層 group 清單、會推哪些檔（含對應裝置路徑）」，完全不寫入、不刪除、不備份。

## 6. 路徑選擇邏輯

1. 取得 AFC root 前綴（自動偵測或 `--path-prefix`）。
2. 在 `Documents` 下列所有子資料夾（`{numbers}` 等）。
3. 篩出**含 `settings-acr` 子目錄**者為候選。
4. 選定採用的 catalog（因 library 無 mtime，**不依時間自動挑**）：
   - **0 個候選** → 明確報錯，提示可能 app 尚未產生 catalog 或路徑前綴需手動指定。
   - **1 個候選** → 自動採用。
   - **多個候選** → 印編號清單（含每個 catalog 名稱與其 `userStyles` 內檔案數），在終端機讀使用者輸入選一個（**互動選單**）。
   - `--catalog <name>` 旗標可非互動覆寫（指定後直接採用該名稱，不顯示選單；給 dry-run 跑在腳本/無 TTY 時用）。指定了不存在的名稱 → 報錯並列出可用名稱。
5. `target = {chosen}/settings-acr/userStyles`。

## 7. 備份

- push 與 rm 在 commit 前都先 pull 目標到本機備份目錄。
- push 備份整個 userStyles；rm 備份各刪除目標。
- 備份目錄結構保留原樹狀，時間戳記資料夾隔離每次執行。

## 8. 安全與輸出

- 啟動橫幅警告（push/rm commit 時必印）：
  - 執行前請在 iPhone 上「完全關閉」Lightroom（上滑殺掉），推完再重啟，否則 app 的存檔流程可能蓋掉寫入。
  - 提醒：此法 preset 可能只在本機出現、未必同步到 Creative Cloud。
- 預設 `--dry-run`：只印計畫不動手；要 `--commit` 才寫。
- 每個檔案推送/刪除結果逐筆 log（OK/skip/fail + 裝置端路徑）。
- 清楚的錯誤處理：裝置找不到、house_arrest 連線失敗、AFC 操作失敗都回有意義的訊息。

## 9. 風險註記

- **子資料夾是否被 Lightroom 認得未知**：使用者要求 push 保留來源資料夾層級（`userStyles/my-presets/...`）。inspect 跑完若發現現有 userStyles 實際是純平的、Lightroom 不認 userStyles 下的子資料夾，需回頭討論是否改拍平。先按使用者要求保留結構實作。
- AFC root 與副檔名在接上實機 inspect 前皆為假設，預設值待 inspect 後填入。

## 10. 交付與里程碑

- **里程碑 1（首交付）：** 可編譯可跑的 `inspect`（含 device/afcfs/locate 基礎）。在使用者實機跑一次，取得真實 AFC root 前綴，並肉眼確認現有 preset 的副檔名/格式以便準備來源檔。
- **里程碑 2：** 以里程碑 1 取得的事實，完成 `push`（合併語意、備份、dry-run/commit）。
- **里程碑 3：** `rm` 刪除子指令。
- 完整 `go.mod`、錯誤處理、逐檔 log。
- README：相依套件、USB 配對/信任前置、使用範例（dry-run → commit）、inspect → push → rm 流程。

## 11. 相依

- `github.com/danielpaulus/go-ios`
- `github.com/spf13/cobra`
