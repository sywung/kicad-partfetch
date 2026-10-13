# kicad-partfetch 線上來源規格（M4）

> 2026-10-12。LCSC 沒有的東西（開發板、冷門模組）要能在線上搜尋、預覽確認後加入。
> 新增兩個來源：**EasyEDA 使用者貢獻元件庫**（M4a）與 **GitHub**（M4b）。
> M4b 依賴 M3 的 `internal/importer`。驗收基準是 KiCad 10。兩者都不需要登入。

## 1. 零件識別碼（PartID）

整個程式用字串 PartID 指一個可預覽、可加入的項目。現有 `C` 加數字的 LCSC 編號不變，新增：

| 來源 | 形式 | 例 |
|---|---|---|
| `lcsc` | `C\d+`（不分大小寫，正規化成大寫） | `C2040` |
| `easyeda` | `easyeda:<32 位十六進位 uuid>`（小寫） | `easyeda:f2fc22956d5f4a41aa18d1a4337fd37a` |
| `github` | `github:<owner>/<repo>`（owner／repo 僅允許 `[A-Za-z0-9._-]`，各 1～100 字元，不得為 `.` 或 `..`） | `github:ccadic/T-Display-S3-Kicad` |

新增 `internal/partid`：`Parse(s string) (source, value string, err error)`，不符合任一形式回 error。所有入口（CLI、app 綁定）先經它驗證，**不得**把未驗證的字串拼進 URL 或檔案路徑。

## 2. M4a：EasyEDA 使用者貢獻元件庫

實測（2026-10-12）：

- 搜尋：`POST https://easyeda.com/api/components/search`，`Content-Type: application/x-www-form-urlencoded`，欄位
  `type=3`、`doctype[]=2`、`returnListStyle=classifyarr`、`uid=user`、`wd=<關鍵字>`、`page=<頁碼，1 起>`、`pageSize=25`。
  回應 `result.lists.user` 是項目陣列，`result.facets.user` 是總數。**不帶 `uid=user` 時 `lists.user` 是空的**（只回 LCSC 那一組）。
- 項目欄位：`uuid`、`title`、`description`、`owner.nickname`（可能沒有，退回 `owner.username`）、`dataStr.head.c_para.package`、`thumb`（`//image.easyeda.com/...`，要補 `https:`）。
- 取單一元件：`GET https://easyeda.com/api/components/<uuid>?version=6.4.19.5`，回應的 `result` 與 LCSC 零件同形（`dataStr`、`packageDetail`），但沒有 `lcsc`／`szlcsc`。已實測兩個使用者元件經現有 `easyeda.Parse` → `library.Write` → `kicad-cli` 10 可載入。
- lceda.cn 的使用者元件庫是**另一份資料**，uuid 不通用：這個來源只打 easyeda.com，**不做鏡像切換**。403／429 → `ErrRateLimited`。
- 節流、快取與 LCSC 零件共用同一個 client（每 host 1.2 秒；快取檔 `components/easyeda-<uuid>.json`，7 天）。搜尋不快取，但同樣要過節流。

實作：

- `easyeda.Client.Fetch` 接受 `easyeda:<uuid>`：走上面的單一元件端點。解析後 `Component.ID` 設為該 PartID。
- 符號屬性：`LCSC` 屬性留空；新增屬性 `EasyEDA`，值為 uuid（隱藏，樣式同 `LCSC`）。LCSC 零件不寫 `EasyEDA` 屬性，其輸出**逐位元組不變**。
- `kicad.SymbolLibraryInfo`／`library.Entry`／`app.LibraryEntry` 加欄位 `PartID`：有 `LCSC` 用它；否則有 `EasyEDA` 用 `easyeda:<uuid>`；否則空。前端已安裝清單改用 `PartID` 判斷可否點選預覽（顯示的編號：LCSC 顯示 C 編號，EasyEDA 顯示「社群」）。
- `library.Remove` 的名稱解析除了符號名稱、LCSC 編號，也接受 `easyeda:<uuid>`。
- 搜尋：`internal/search` 新增 `Community(ctx, query, page) (Result, error)`，`Item` 欄位對應：`PartID=easyeda:<uuid>`、`MPN=title`、`Manufacturer=作者`、`Package=package`、`Description=description`、`ImageURL=thumb`；`Stock`／`Price` 為零值。`Total=facets.user`。

## 3. M4b：GitHub

實測（2026-10-12，未帶 token）：

- 搜 repo：`GET https://api.github.com/search/repositories?q=<關鍵字>&per_page=25&page=<n>`；限制每分鐘 10 次。`t-display kicad` 回 5 個 repo。
- 列檔：`GET https://api.github.com/repos/<owner>/<repo>/git/trees/HEAD?recursive=1`；限制每小時 60 次（與其他 core API 共用）。`truncated: true` 表示樹不完整。
- 下載：`https://raw.githubusercontent.com/<owner>/<repo>/<sha>/<path>`（用 tree 回應的 commit sha，不要用 `HEAD`，確保預覽與加入拿到同一版）。
- 標頭：`Accept: application/vnd.github+json`、`User-Agent: kicad-partfetch`。環境變數 `GITHUB_TOKEN` 有值時加 `Authorization: Bearer`（提高額度；不寫進設定檔、不記錄到任何輸出）。
- 403／429 且 `X-RateLimit-Remaining: 0` → `ErrGitHubRateLimited`，錯誤訊息帶 `X-RateLimit-Reset` 換算的本地時間。

`internal/source/github`：

```go
type Repo struct {
    FullName, Description, License, URL, SHA string
    Stars                                   int
}
type File struct {
    Path string
    Size int64
}
type Contents struct {
    Repo                         Repo
    Symbols, Footprints, Models  []File // .kicad_sym / .kicad_mod / .step .stp .wrl（副檔名不分大小寫），依路徑排序
    Legacy                       int    // .lib .mod .dcm 的數量
    Truncated                    bool
}
func (c *Client) Search(ctx, query string, page int) ([]Repo, int, error)
func (c *Client) Contents(ctx, fullName string) (Contents, error)
func (c *Client) Download(ctx, contents Contents, dir string) error
```

- `Search`：關鍵字不含 `kicad`（不分大小寫）時自動加上 ` kicad`。
- `Contents`：結果以 `<owner>/<repo>` 為鍵在記憶體快取 10 分鐘（省額度：預覽與加入共用）。
- `Download`：把 Symbols／Footprints／Models 下載到 `dir`，**保留 repo 內的相對路徑**（讓 importer 在同一包內比對）。路徑含 `..` 片段、絕對路徑、或清理後不在 `dir` 內 → 跳過該檔。
- 上限：一個 repo 的 Symbols+Footprints+Models 超過 **200 個檔案**、或 `Truncated`、或任一檔 `Size` 超過 64 MiB、或合計超過 256 MiB → `ErrRepoTooLarge`，訊息「此 repo 的 KiCad 檔案過多（N 個），請自行下載後用『匯入資料夾…』挑選」。不做部分下載。
- 三類都是 0 個 → `ErrNoKiCadFiles`（訊息依 `Legacy` 是否 >0 區分「只有舊格式」）。
- 下載要驗證實際讀到的位元組數不超過上限（不信任 `Size`）。

加入（`add` 的 GitHub 路徑）：`Contents` → `Download` 到 `os.MkdirTemp` → `importer.Import(opts, []string{dir})` → 刪掉暫存目錄（成功失敗都刪）。`Result.Source` 改寫成 `github:<owner>/<repo>`。

### 3.1 用 kicad-cli 產生預覽

`internal/kicadcli`：

- `Find(configured string) (string, error)`：依序試 設定值 → macOS `/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli` → `exec.LookPath("kicad-cli")` → Windows `C:\Program Files\KiCad\*\bin\kicad-cli.exe`（取版本目錄排序最後一個）。都沒有 → `ErrNotFound`。
- `SymbolSVGs(ctx, cli, symFile string) ([]NamedSVG, error)`：`kicad-cli sym export svg -o <tmp> --theme _builtin_default <file>`，讀回所有 `.svg`，依檔名排序。
- `FootprintSVGs(ctx, cli, prettyDir string) ([]NamedSVG, error)`：`kicad-cli fp export svg -o <tmp> --layers B.Cu,F.Cu,B.SilkS,F.SilkS,B.Fab,F.Fab,B.CrtYd,F.CrtYd,Edge.Cuts,Dwgs.User,Cmts.User --theme _builtin_default <dir>`（不限制圖層的話會連內層銅箔一起畫，通孔焊盤會變成某個內層的顏色）。
- 每次呼叫逾時 30 秒（`exec.CommandContext`）；參數一律用引數陣列傳，**不得**經 shell；暫存目錄用完即刪。
- 設定檔新增 `kicadCLI`（字串，預設空＝自動尋找）。GUI 設定區不必加欄位（這次只讀設定檔）。

預覽上限：符號檔取前 5 個、每檔 SVG 取前 12 個；封裝取前 24 個。超過時在 `Preview.Warnings` 說明「僅預覽前 N 個」。

## 4. CLI

- `partfetch search [--source lcsc|easyeda|github] [--page N] [--json] QUERY...`（預設 `lcsc`；多個引數以空白接起來）
  文字輸出每筆一行：`PARTID<TAB>名稱<TAB>作者或廠牌<TAB>封裝或星數<TAB>說明`，最後一行 `total: N`。`--json` 輸出 `{"total":N,"items":[...]}`。
- `partfetch add` 的引數接受三種 PartID。GitHub 項目的輸出格式與 `import` 相同（每包一段）；LCSC／EasyEDA 項目維持 `add` 原格式。
- `partfetch info` 接受 `easyeda:<uuid>`；對 `github:` 印出 repo 資訊與三類檔案清單。
- usage：`usage: partfetch <add|info|list|remove|import|search> ...`
- 無效的 PartID → 該項 error、exit 1，不發任何網路請求。

## 5. GUI

綁定變更：

```go
func (a *App) Search(source, query string, page int) (SearchResult, error) // source: lcsc | easyeda | github
type SearchItem struct { ...既有欄位...; Source, URL, License string; Stars int }
type Preview struct { ...既有欄位...; Source, URL, License string; FootprintSVGs []string; Files []string; External bool }
```

- `Search`：`source` 不是三者之一 → error。貼上的內容全部是合法 PartID（三種形式皆可、可混用）時直接列出這些項目，不查詢（延伸現有「全部是 C 編號」的行為）。
- `Preview(partID)`：
  - `lcsc`／`easyeda`：現有流程。`FootprintSVGs` 為只含 `FootprintSVG` 的單元素陣列，`External=false`。
  - `github`：`Contents` → 下載符號與封裝檔到暫存目錄 → `kicadcli` 轉 SVG。`External=true`、`Name=repo 名稱`、`Package=""`、`Manufacturer=owner`、`Files`=三類檔案的 repo 內路徑、`HasModel=len(Models)>0`、`ModelTitle`=第一個 3D 檔名。找不到 kicad-cli → 仍回傳成功，`SymbolSVGs`／`FootprintSVGs` 為空陣列，Warnings 加「找不到 kicad-cli，無法產生預覽圖；仍可加入元件庫」。
- `Model(partID)`：`github` → error「GitHub 來源的 3D 模型無法在此預覽，請加入後在 KiCad 檢視」。
- `Add(partIDs)`：依來源分派。GitHub 項目的 `AddResult`：`Symbol`／`Footprint`／`Model` 各取該類項目的彙總狀態（有 `failed` → `failed`；否則有 `added` → `added`；否則有 `updated` → `updated`；否則有 `exists` → `exists`；該類沒有任何項目 → 符號與封裝回 `skipped`、3D 回 `skipped`），`Warnings` 第一則是「符號 N · 封裝 N · 3D N」，其後接 importer 的 warnings；`Error` 取 importer 的 Error。
- 所有 slice 經 JSON 不得是 `null`。

前端：

- SearchPane：搜尋框上方加來源切換（三段式，`role="radiogroup"`）：「LCSC」「EasyEDA 社群」「GitHub」。切換來源會清空結果。預設 LCSC；選擇記在 `localStorage`（讀寫包 try/catch）。
- 結果列：LCSC 維持現狀。EasyEDA 社群顯示 名稱／作者／封裝，不顯示庫存與價格，加一個「未審核」標記。GitHub 顯示 `owner/repo`、★星數、授權（沒有則「未聲明授權」）、說明，以及「在瀏覽器開啟 ↗」連結（走既有 `open-url`）。
- PreviewPane：
  - `External` 為 true 時，SVG **一律用 `<img src="data:image/svg+xml;base64,...">` 顯示，不得用 `v-html`**（內容來自第三方檔案）。
  - 封裝分頁：`FootprintSVGs` 多於一個時提供與符號單元相同樣式的切換器。
  - `External` 時 3D 分頁顯示說明文字（同 `Model` 的錯誤訊息）與 3D 檔清單，不載入 ModelViewer。
  - `External` 時在預覽區上方顯示來源列：repo 連結、授權、`Files` 的數量摘要，以及固定警語「第三方檔案，請自行確認腳位與尺寸」。EasyEDA 社群元件顯示「使用者貢獻，未經審核，請自行確認腳位與尺寸」。
- 「加入元件庫」的行為不變（勾選的或目前預覽的 PartID）。
- 沿用既有 tokens；CSS 一條規則一行。

## 6. 測試（照表寫；期望值寫死；一律用假的 `http.RoundTripper`，**不得連外**）

`internal/partid`：三種合法形式、大小寫正規化、各種非法形式（空字串、`github:../x`、`github:a/b/c`、`easyeda:` 加非 32 位十六進位、含空白、含 `?` `#` `%`）。

`internal/search`：`Community` 送出的表單欄位（含 `uid=user`、`page`、`pageSize=25`）、項目對應、`thumb` 補 `https:`、`owner.nickname` 缺時退回 `username`、403 → rate limit 錯誤。

`internal/source/easyeda`：`easyeda:<uuid>` 打對的 URL 且**只**打 easyeda.com（403 時不切 lceda.cn）；快取檔名；`Component.ID`。符號輸出含 `EasyEDA` 屬性；LCSC 零件的輸出與修改前逐位元組相同（用現有 `testdata`）。

`internal/library`：`Entry.PartID` 三種情況；`Remove` 以 `easyeda:<uuid>` 指定。

`internal/source/github`：Search 自動補 `kicad`、已含時不重複；分頁參數；Contents 的分類與排序、`Truncated`；記憶體快取（第二次不發請求）；Download 保留相對路徑、路徑穿越被跳過、超過 200 檔／單檔過大／實際大小超過宣告 → 指定錯誤且 `dir` 內沒有殘留檔；rate limit 錯誤含重置時間；`GITHUB_TOKEN` 有值時帶 Authorization、沒值時不帶。

`internal/kicadcli`：`Find` 的優先序（用可注入的檔案存在／LookPath 函式測，不依賴本機是否裝 KiCad）；逾時；引數陣列內容。實際執行 kicad-cli 的測試在找不到 kicad-cli 時 `t.Skip`。

`internal/cli`：`search` 三個來源的文字與 JSON 輸出；`add` 混用三種 PartID；無效 PartID 不發請求。

`app`：`Search` 的來源分派與無效來源；貼上混合 PartID；`Preview` 的 GitHub 路徑（假的 github client 與假的 kicadcli）含找不到 kicad-cli 的情況；`Add` 的彙總狀態表（每個分支一個案例）；JSON 不含 `null`。

所有既有測試維持通過；不得修改 `testdata/expect/*.json`。

## 7. 不做

- SnapEDA／Ultra Librarian／SamacSys（都要登入）。
- lceda.cn 的使用者元件庫。
- GitHub code search（要登入）、GitLab 等其他平台。
- 從大型 repo 挑選個別檔案（超過上限就請使用者自行下載後匯入）。
- STEP 的 3D 預覽。
- 在 GUI 設定 kicad-cli 路徑與 GitHub token。
