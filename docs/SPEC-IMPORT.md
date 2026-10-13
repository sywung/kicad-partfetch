# kicad-partfetch 匯入規格（M3）

> 2026-10-12。LCSC／EasyEDA 沒有的零件（例如開發板、冷門模組），改從使用者**已經下載到本機**的 KiCad 檔案匯入：
> SnapEDA／Ultra Librarian／SamacSys 下載的 zip、GitHub 上別人做好的元件庫資料夾、或單一檔案。
> 併進與 `add` 相同的元件庫佈局（`docs/SPEC.md` 第 2 節）。不連網、不登入任何網站。
> **驗收基準是 KiCad 10**（本機 `kicad-cli` 10.0.4）：匯入後的元件庫必須能被它載入。

## 1. 輸入

每個輸入路徑是一個「包」（bundle），符號↔封裝↔3D 的對應只在同一包內解析。

| 輸入 | 處理 |
|---|---|
| 目錄 | 遞迴掃描 |
| `.zip` | 掃描所有項目（不解壓到磁碟、不處理 zip 內的 zip） |
| 單一 `.kicad_sym`／`.kicad_mod`／3D 檔 | 當成只有一個檔案的包 |

收集的檔案（副檔名不分大小寫）：

- 符號庫 `.kicad_sym`
- 封裝 `.kicad_mod`
- 3D：`.step`、`.stp`、`.wrl`

忽略：`__MACOSX/` 底下的一切、檔名以 `.` 開頭的項目、其他副檔名。
同一包內的檔案依**完整路徑字串排序**後處理，結果才可重現。

上限（超過 → 該包 `Error`，不寫入任何檔案）：單檔 64 MiB、整包未壓縮合計 512 MiB、5000 個項目。zip 的大小要以**實際讀到的位元組數**計，不可信任標頭宣告的大小。

舊格式：包內有 `.lib`／`.dcm`／`.mod` 但沒有任何 `.kicad_sym`／`.kicad_mod` → `Error`「只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式」。兩者並存時忽略舊格式並加一則 warning。

包內沒有任何可收集的檔案 → `Error`「找不到可匯入的 KiCad 檔案」。

已知的下載包佈局（**憑印象整理，沒有拿真實下載檔驗過**，所以實作不得依賴固定路徑，一律遞迴掃副檔名）：

- SnapEDA：zip 根目錄直接放 `PART.kicad_sym`、`PART.kicad_mod`、`PART.step`
- Ultra Librarian：`KiCAD/<日期>/` 下有 `.kicad_sym`、`footprints.pretty/*.kicad_mod`，STEP 在另一個子目錄
- SamacSys：`LIB_PART/PART/KiCad/` 下有 `.kicad_sym`、`.kicad_mod`（還有舊的 `.lib`／`.dcm`／`.mod`），`LIB_PART/PART/3D/PART.stp`

## 2. 目的地檔名

- 符號：名稱不變。
- 封裝：`<lib>.pretty/<Sanitize(原檔名去副檔名)>.kicad_mod`（KiCad 以檔名當封裝名稱）。
- 3D：`<lib>.3dshapes/<Sanitize(原檔名去副檔名)><原副檔名>`。
- 同一包內兩個檔案得出相同目的地檔名 → 只用排序後的第一個，其餘加 warning。
- 所有寫入都只落在 `<out>` 內，目的地一律由檔名組出，**不使用**輸入檔內含的目錄路徑（zip slip 防護）。

## 3. 改寫（只動必要的位元組，其餘原樣保留）

### 3.1 符號的 `Footprint` 屬性

對每個最上層符號，取 `(property "Footprint" "V" ...)` 的 V，令 ref = V 最後一個 `:` 之後的部分（沒有 `:` 就是 V）：

1. ref 非空，且等於某個匯入封裝的原檔名（去副檔名）或其 Sanitize 結果——先區分大小寫比對，沒有再不分大小寫——→ 把 V 改成 `<lib>:<該封裝的目的地檔名去副檔名>`。
2. V 為空字串，且包內**恰好一個**封裝 → 改成 `<lib>:<那個封裝>`。
3. V 非空但對不到 → 不改，加 warning「符號 X 的封裝 V 不在匯入內容中，保留原值」。
4. 沒有 `Footprint` 屬性 → 不新增、不改；包內恰好一個封裝時加 warning「符號 X 沒有 Footprint 屬性，未自動指定封裝」。

只替換該屬性值的字串內容（含正確的跳脫），不得重排或重新格式化符號的其他部分。屬性的定位要用括號／字串狀態掃描（同 `topSymbols` 的等級），不可用 `bytes.Index` 在整段文字裡找。

### 3.2 封裝的 `(model "PATH" ...)`

對封裝檔內每個 model 路徑，取檔名（`/` 與 `\` 都是分隔），在包內的 3D 檔裡比對：完全相同 → 不分大小寫相同 → 去副檔名後相同（多個候選時優先 `.step`，其次 `.stp`，再來 `.wrl`）。

- 對到 → 路徑改成 `<ModelBase>/<lib>.3dshapes/<目的地檔名>`（`ModelBase` 的組法與 `add` 寫封裝時完全相同；Windows 上的寫法也要與 `add` 一致）。
- 對不到 → 不改，加 warning「封裝 X 的 3D 模型 PATH 不在匯入內容中」。
- 封裝**完全沒有** model 區塊，且包內恰好一個封裝、至少一個 3D 檔 → 在封裝最外層的結尾 `)` 之前插入
  `(model "<路徑>" (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0)))`，選檔優先序同上、同副檔名取排序第一個；加 warning「已自動掛上 3D 模型 F，位置未經校正，請在 KiCad 檢查」。

沒被任何封裝引用的 3D 檔仍然照樣複製。

### 3.3 封裝格式

接受 `(footprint "NAME" ...)` 與舊寫法 `(module NAME ...)`，內容除 model 路徑外原樣複製。最外層不是這兩者之一 → 該封裝 `failed`，warning 說明。

## 4. 寫入與結果

```go
package importer

type Item struct{ Name, Status string } // Status: added | updated | exists | failed
type Result struct {
    Source     string
    Symbols    []Item
    Footprints []Item
    Models     []Item
    Warnings   []string
    Error      string
}
func Import(opts library.Options, paths []string) []Result
```

- 每個輸入路徑一筆 `Result`，順序同輸入。所有 slice 一律非 nil。
- 寫入順序：3D → 封裝 → 符號。3D 與封裝沿用 `add` 的語意：檔案已存在且 `Overwrite` 為 false → `exists`（不動檔案）；存在且 `Overwrite` → `updated`；否則 `added`。
- 符號：該包所有符號**一次**併入 `<lib>.kicad_sym`。已有同名符號且 `Overwrite` 為 false → `exists`（保留舊的）；`Overwrite` → `updated`。有任何符號實際寫入、且符號庫原本存在 → 先備份成 `.bak`（同 `add`）。沒有任何符號要寫入時不產生 `.bak`、不改檔。
- 新增的符號**依包內順序**（檔案路徑排序、檔內原順序）附加在既有符號之後，不得重新排序——衍生符號 `(extends "P")` 要排在父符號之後。
- 衍生符號的父符號既不在本次要寫入的符號中、也不在既有元件庫裡 → 該符號 `failed`，warning「符號 X 的父符號 P 不存在」，不寫入。
- `Item.Name`：符號是符號名稱；封裝與 3D 是目的地檔名（封裝不含副檔名，3D 含副檔名）。
- 一個包的 `.kicad_sym` 解析失敗 → 該檔的符號全部略過並加 warning，其餘檔案照常處理；若因此整包沒有寫入任何東西 → `Error`。

### 4.1 符號庫檔頭版本（重要）

別人的檔案常是較新的格式（KiCad 10 是 `(version 20251024)`，我們輸出的是 `20231120`）。規則：

- 新增 `kicad.LibraryVersion(data []byte) int`：回傳最外層 `(version N)` 的 N，沒有則 0。
- 寫出的符號庫檔頭版本 = max(既有元件庫的版本, 本次匯入各 `.kicad_sym` 的版本, 20231120)。
- **`MergeSymbols`（`add` 用的那個）也要改成保留較高的版本**：既有檔的版本高於 20231120 時沿用既有版本，不可降回去。除此之外 `add` 的輸出不得有任何變化（既有測試與 `testdata/expect` 不動）。
- 匯入的版本高於 20231120 時加一則 warning「符號格式版本 N 較新，KiCad 8／9 可能無法開啟此元件庫」。

## 5. CLI

```
partfetch import --out DIR --lib NAME [--overwrite] [--model-base P] [--json] PATH...
```

- usage 改為 `usage: partfetch <add|info|list|remove|import> ...`
- 文字輸出，每包：
  ```
  <PATH>:
    symbol <NAME>: <status>
    footprint <NAME>: <status>
    model <NAME>: <status>
    warning: <文字>
    error: <文字>
  ```
- `--json`：每包一行一個物件，欄位 `source`、`symbols`、`footprints`、`models`（各為 `[{"name","status"}]`，空時是 `[]`）、`warnings`（空則省略）、`error`（空則省略）。
- 沒給 PATH → stderr `import requires one or more files or directories`、exit 2。
- exit code：任何包有 `Error` 或任何項目 `failed` → 1；否則 0。
- 不得呼叫 `source.Source`（不連網）。

## 6. GUI

綁定：

```go
type ImportItem struct{ Name, Status string }
type ImportResult struct {
    Source                      string
    Symbols, Footprints, Models []ImportItem
    Warnings                    []string
    Error                       string
}
func (a *App) PickImportFiles() ([]string, error)     // 原生多選檔案對話框：*.zip;*.kicad_sym;*.kicad_mod;*.step;*.stp;*.wrl
func (a *App) PickImportDirectory() (string, error)  // 原生目錄對話框
func (a *App) Import(paths []string) ([]ImportResult, error)
```

- 對話框與 `PickDirectory` 一樣可由 `Options` 注入假的實作以便測試；使用者取消 → 回空 slice／空字串、nil error。
- `Import`：`paths` 為空 → error「請先選擇要匯入的檔案」。使用已儲存的設定（同 `Add`，含 `Overwrite` 與 3D 路徑模式；「下載 3D 模型」的勾選**不影響**匯入）。所有 slice 經 JSON 後不得是 `null`，要有序列化測試。
- 更新 `frontend/src/types/wails.d.ts`、`frontend/src/lib/bridge.ts`。

前端（LibraryPane，「加入元件庫」按鈕下方）：

- 一列兩個次要按鈕：「匯入檔案…」「匯入資料夾…」。匯入或加入進行中兩者皆 disabled。
- 選完就直接匯入（取消則什麼都不做）。完成後重新載入已安裝清單。
- 結果顯示在既有的「匯入紀錄」區，與 `add` 的紀錄用同一個清單、每包一列：標題是來源路徑的最後一段；狀態摘要「符號 N · 封裝 N · 3D N」（N 是 `added`+`updated` 的數量，另有 `exists` 時加「（已存在 M）」）；有 `Error` 顯示紅色 ×；warnings 逐條列出（沿用 `△` 樣式）。新的匯入或新的加入會清掉前一次的紀錄（現況即如此）。
- 狀態列：全部成功「匯入完成。」；有 `Error` 或 `failed`「部分項目匯入失敗，詳見匯入紀錄。」（error 樣式）。
- 沿用既有 tokens，不新增色彩或字型；CSS 一條規則一行。不得使用 `window.confirm`／`alert`。

## 7. 測試（照這張表寫；期望值寫死在測試裡，不由被測函式產生）

zip 一律在測試裡用 `archive/zip` 於記憶體／`t.TempDir()` 組出，不放二進位檔進 repo。

`internal/kicad`：
- `LibraryVersion`：有版本、沒有版本、版本出現在字串內的陷阱。
- `MergeSymbols`：既有檔頭 `20251024` → 輸出檔頭仍是 `20251024`；既有檔頭 `20231120` 或沒有既有檔 → 輸出與修改前**逐位元組相同**（用修改前的實際輸出當寫死的期望值）。
- Footprint 屬性改寫：一般情況；值含跳脫引號；符號內另有子符號也帶同名屬性字樣的字串陷阱；改寫後其餘位元組不變（期望值寫死）。
- 封裝 model 路徑改寫：一個、兩個、路徑含 `\`、字串內含 `(model "` 的陷阱；插入 model 區塊的位置（最外層結尾 `)` 之前）與內容。

`internal/importer`：
- 三種佈局各一（第 1 節的 SnapEDA／Ultra Librarian／SamacSys）：符號的 Footprint 變成 `<lib>:<fp>`、封裝的 model 路徑變成 `<ModelBase>/<lib>.3dshapes/<file>`、三類檔案都落在正確位置。
- 目錄輸入與 zip 輸入對同一批內容產生相同結果。
- 單一 `.kicad_mod` 檔輸入。
- `__MACOSX/` 與 `.` 開頭檔案被忽略。
- zip slip：項目名稱 `../../evil.kicad_mod` 與 `/abs/evil.step` → 檔案只會出現在 `<out>` 內；`<out>` 外不得多出任何檔案。
- 大小上限：一個宣告大小很小但實際解壓超過單檔上限的項目 → `Error`、`<out>` 內沒有任何檔案（上限要可由測試縮小，不要真的產生 64 MiB）。
- 只有 `.lib`／`.mod` → 指定的 Error 文字；空包 → 指定的 Error 文字。
- 已存在且不覆寫 → 三類都是 `exists`、檔案位元組不變、沒有 `.bak`；`Overwrite` → `updated`、有 `.bak` 且內容等於匯入前。
- 併入既有元件庫：既有符號原樣保留、新符號附加在後且順序同包內順序。
- 檔頭版本：既有 `20231120`＋匯入 `20251024` → `20251024` 並有 warning；之後再對同一元件庫呼叫 `library.Write`（`add` 的路徑）→ 仍是 `20251024`。
- 衍生符號：父子同包（順序保留）；父符號不存在 → `failed`＋warning、其餘符號照常寫入。
- 3.1 的四種情況、3.2 的三種情況各一個案例。
- 同一包兩個檔案撞名 → 只用第一個＋warning。
- 兩個輸入路徑 → 兩筆結果、彼此的對應不互相影響。

`internal/cli`：文字與 `--json` 輸出格式、exit code 0／1／2。

`app`：`Import` 空輸入的錯誤、結果經 `json.Marshal` 不含 `null`、對話框取消回空值、`Overwrite` 設定有傳到底層。

所有既有測試維持通過；不得修改 `testdata/expect/*.json`。

## 8. 不做

- 不連任何網站、不處理登入與下載。
- 不轉換舊格式（`.lib`／`.mod`）、不轉換 Altium／Eagle 等其他 EDA 格式。
- 不在匯入前預覽；匯入錯了用 M2b 的移除功能處理。
- 匯入的符號沒有 LCSC 編號，已安裝清單裡不可點選預覽（現況）。
- 不做拖放匯入。
- 不校正外來 3D 模型的位置。
- 不碰 `build/`。

## 9. 舊版符號庫（`.lib`）自動轉換（M3b，2026-10-12）

起因：`github:GillesOdb/ttgo-tdisplay-kicad` 的符號只有舊格式 `ttgo_esp32.lib`，匯入後只進了兩個封裝，
「已安裝符號」清單沒有任何東西，使用者看起來就是「沒辦法匯入」。本節取代第 1 節關於 `.lib` 的規定；`.mod`（舊版封裝）仍不支援。

實測（KiCad 10.0.4）：`kicad-cli sym upgrade -o <out.kicad_sym> <in.lib>` 可把舊格式轉成 `(version 20251024)` 的符號庫；同目錄有同主檔名的 `.dcm` 時一併讀取。

### 9.1 轉換

- `internal/kicadcli` 新增 `UpgradeSymbolLibrary(ctx, cli, libData, dcmData []byte) ([]byte, error)`：在暫存目錄寫出 `in.lib`（與 `in.dcm`，若有），執行 `kicad-cli sym upgrade -o <tmp>/out.kicad_sym <tmp>/in.lib`，讀回結果。逾時 30 秒、引數陣列、不經 shell、暫存目錄成功失敗都刪。
- `importer` 新增可選的轉換器：
  ```go
  type LegacyConverter func(lib, dcm []byte) ([]byte, error)
  func ImportWith(opts library.Options, paths []string, convert LegacyConverter) []Result
  ```
  `Import(opts, paths)` 等同 `ImportWith(opts, paths, nil)`，行為與現在完全相同。
- 收集階段把 `.lib` 與 `.dcm` 的內容也讀進來（計入第 1 節的大小與數量上限）。
- 對包內每個 `.lib`（依路徑排序）：
  - `convert` 為 nil → 不轉換，加 warning「發現舊版符號庫 X.lib，但找不到 kicad-cli，無法自動轉換」。
  - 否則呼叫 `convert(lib, 同目錄同主檔名（不分大小寫）的 .dcm 或 nil)`。成功 → 結果視為一個 `.kicad_sym`，虛擬路徑是原路徑把副檔名換成 `.kicad_sym`，加 warning「已用 kicad-cli 將舊版符號庫 X.lib 轉為新格式」。失敗 → warning「舊版符號庫 X.lib 轉換失敗：<原因>」，其餘檔案照常處理。
  - 同一目錄已有同主檔名的 `.kicad_sym` → 不轉換、不警告（新格式優先）。
- 轉換出來的符號與一般符號走完全相同的流程（3.1 的 Footprint 改寫、4.1 的檔頭版本、衍生符號檢查）。
- 錯誤文字調整：包內只有 `.mod`（沒有 `.lib`、沒有任何新格式檔）→ 維持「只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式」。包內有 `.lib` 但全部轉換失敗或沒有轉換器、且沒有其他可匯入的檔案 → `Error` 同一句，warnings 保留上面的說明。
- 原本「兩者並存時忽略舊格式並加一則 warning『忽略舊版 KiCad 格式檔案』」：只在有 `.mod`、或有未被轉換的 `.lib`／`.dcm` 時才加。

### 9.2 以名稱配對封裝（補 3.1）

在 3.1 的規則 2 之後、規則 3 之前加一條：

- V 為空字串，包內有**多個**封裝，且恰好一個封裝的原檔名（去副檔名）或其 Sanitize 結果與符號名稱相同（先區分大小寫，沒有再不分大小寫）→ 改成 `<lib>:<該封裝>`，加 warning「符號 X 未指定封裝，已依名稱配對到 Y，請確認」。

### 9.3 接線

- CLI `import` 與 app 的 `Import`：用 `kicadcli.Find` 找得到時傳入轉換器，找不到傳 nil。CLI 與 app 的轉換器都要可注入以便測試。
- GitHub：`Contents` 新增 `LegacyFiles []File`（`.lib`、`.dcm`、`.mod`，依路徑排序；`Legacy` 維持為其數量）。`Download` 也下載 `.lib` 與 `.dcm`（不下載 `.mod`），計入 200 檔與大小上限。`ErrNoKiCadFiles` 的判斷改為：三類新格式為 0 **且**沒有 `.lib`。GitHub 的加入路徑同樣傳入轉換器。
- GitHub 預覽：有 `.lib` 且找得到 kicad-cli 時，先轉換再產生符號預覽（與新格式符號合計仍受「前 5 個符號檔」上限）；`Files` 也列出 `.lib`。

### 9.4 測試（期望值寫死；假的轉換器／runner，不連外）

- `kicadcli`：`UpgradeSymbolLibrary` 的引數陣列、有無 `.dcm`、逾時、失敗時暫存目錄已刪。實際執行 kicad-cli 的測試在找不到時 `t.Skip`。
- `importer`：有轉換器（轉出的符號被寫入、warning 文字、Footprint 依 9.2 配對）；無轉換器（warning、其餘檔案照常）；轉換失敗；同名 `.kicad_sym` 已存在時不轉換；只有 `.lib` 且無轉換器 → Error；只有 `.mod` → Error；`.dcm` 有傳給轉換器；9.2 的命中（區分大小寫、不分大小寫）、兩個封裝都符合時不配對、V 非空時不套用。`Import` 的既有測試不得修改且全過。
- `github`：`LegacyFiles` 的分類；`Download` 含 `.lib`／`.dcm` 不含 `.mod`；只有 `.lib` 的 repo 不回 `ErrNoKiCadFiles`。
- `cli`／`app`：轉換器有傳入 importer（找得到 kicad-cli）與傳 nil（找不到）各一。
