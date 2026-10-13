# kicad-partfetch GUI 規格（M2）

Wails v2（2.16）桌面 app：macOS／Windows／Linux。前端 Vue 3 + TypeScript + Vite，3D 預覽用 three.js。
核心邏輯全部重用 `internal/` 既有套件；GUI 不得重寫轉換邏輯。

## 1. 專案佈局

```
main.go            Wails 入口（repo 根目錄，package main）
wails.json
app/               Go：綁定給前端的 App（package app）
internal/preview/  model → SVG（符號、封裝）
internal/search/   LCSC 搜尋
internal/settings/ 設定檔讀寫
frontend/          Vue 3 + TS
cmd/partfetch/     既有 CLI，不動
```

`wails build` 產出 `build/bin/`。`go build ./...` 與 `go test ./...` 必須在沒有 `frontend/dist` 的情況下也能通過
（`//go:embed all:frontend/dist` 需要目錄存在：在 `frontend/dist/.gitkeep` 放佔位檔並調整 .gitignore）。

## 2. Go 綁定（app.App 的公開方法）

所有方法回傳 `(T, error)`；錯誤訊息是給使用者看的一句話（中文），不得洩漏堆疊。

```go
type SearchItem struct {
    PartID       string   // "C8734"
    MPN          string   // productModel
    Manufacturer string   // brandNameEn
    Package      string   // encapStandard
    Description  string   // productIntroEn
    Stock        int      // stockNumber
    Price        string   // 最低階梯的 usdPrice，格式 "$1.5012"；沒有則 ""
    ImageURL     string   // productImageUrl
    DatasheetURL string   // pdfUrl
}
type SearchResult struct { Items []SearchItem; Total int; Page int; PageSize int }

type Preview struct {
    PartID, Name, Package, Manufacturer, MPN string
    SymbolSVGs   []string   // 每個 unit 一張
    FootprintSVG string
    PinCount, PadCount int
    HasModel     bool
    ModelTitle   string
    Warnings     []string
}
type ModelData struct { OBJ string; Offset, Rotate [3]float64 }   // OBJ 已解壓的文字

type Settings struct {
    OutDir    string   // 元件庫目錄
    LibName   string   // 預設 "partfetch"
    ModelBase string   // "absolute"（預設）| "kiprjmod" | 自訂字串
    With3D    bool     // 預設 true
    Overwrite bool
}
type AddResult struct { PartID, Symbol, Footprint, Model string; Warnings []string; Error, ErrorKind string }
type LibraryEntry struct { Name, Footprint, LCSC string }

func (a *App) Search(query string, page int) (SearchResult, error)
func (a *App) Preview(partID string) (Preview, error)
func (a *App) Model(partID string) (ModelData, error)
func (a *App) GetSettings() (Settings, error)
func (a *App) SaveSettings(s Settings) error
func (a *App) PickDirectory() (string, error)          // 原生目錄對話框；取消回 ""
func (a *App) Add(partIDs []string) ([]AddResult, error)
func (a *App) ListLibrary() ([]LibraryEntry, error)     // 目前元件庫內的符號
func (a *App) OpenInFileManager(path string) error      // 只允許 OutDir 之下的路徑
func (a *App) LogPath() string
func (a *App) OpenLog() error
func (a *App) OpenURL(url string) error                  // 只允許 https://
```

- `Add` 逐一處理，每完成一個零件發事件 `add:progress`（payload：`{index, total, result: AddResult}`）。
- `ModelBase`：`absolute` → OutDir 絕對路徑；`kiprjmod` → `${KIPRJMOD}/<OutDir 最後一層目錄名>`。
- `Search` 的 `query` 若符合 `^[Cc]\d+$`（可多個，以空白／逗號／換行分隔）→ 不打搜尋 API，直接回傳這些編號的佔位項目
  （只有 PartID，其他欄位空）。
- 設定檔：`os.UserConfigDir()/kicad-partfetch/settings.json`。`OutDir` 預設 `~/Documents/KiCad/partfetch`。
- `App` 的依賴（source、搜尋 client、設定路徑）都要能注入，以便不連網測試。

## 3. LCSC 搜尋（internal/search）

`POST https://wmsc.lcsc.com/ftps/wm/search/v3/global`，JSON body，標頭需帶
`Origin: https://www.lcsc.com`、`Referer: https://www.lcsc.com/`、`Accept-Language: en-US,en;q=0.9`、瀏覽器 UA。
回應 `{code:200, result:{...}}`（實測）：

1. body `{"keyword":Q,"currentPage":1,"pageSize":25}`。
2. `result.exactMatchResult`（陣列）非空 → 這些是完全符合的商品，排在結果最前面。
3. `result.productSearchResultVO.productList` 非空 → 直接是商品列表，`result.totalCount` 是總數。
4. 否則若 `result.catalogVOS`／`result.topResults` 有分類（廣泛關鍵字，`isToCategory: true`）→ 取商品數最多的分類，
   再 `POST /ftps/wm/product/query/list`，body：
   `{"keyword":"","globalKeyword":Q,"catalogIdList":[id],"brandIdList":[],"encapValueList":[],"isStock":false,
     "isOtherSuppliers":false,"isAsianBrand":false,"isDeals":false,"isRohsCert":false,"paramNameValueMap":{},
     "currentPage":P,"pageSize":25}`（`result.scene == "FULL_MATCH"` 時加 `"scene":"FULL_MATCH"`）。
   回應 `result.dataList`（商品陣列）與 `result.totalRow`。
   ⚠️ 缺少上述任一欄位會回 `{"code":405,"msg":"Invalid field"}`。
5. 都沒有 → 空結果。

商品欄位：`productCode`、`productModel`、`brandNameEn`、`encapStandard`、`productIntroEn`、`stockNumber`、
`productPriceList[].{ladder, usdPrice}`、`productImageUrl`、`pdfUrl`。
每 host 節流 1 秒；HTTP client 可注入。測試用 `internal/search/testdata/` 的真實回應（`global_exact.json`、`global_category.json`）。

## 4. 預覽 SVG（internal/preview）

`SymbolSVG(sym model.Symbol, unit int) string`、`FootprintSVG(fp model.Footprint) string`。

- `viewBox` 為內容 bbox 外擴 8%；不寫死寬高（由 CSS 控制）。
- 顏色一律用 CSS class，不寫死色碼（主題由前端 CSS 變數決定）：
  符號 `sym-body`、`sym-pin`、`sym-pin-name`、`sym-pin-num`、`sym-text`；
  封裝 `fp-cu-f`、`fp-cu-b`、`fp-cu-th`、`fp-hole`、`fp-silk`、`fp-fab`、`fp-paste`、`fp-mask`、`fp-crtyd`、`fp-edge`、`fp-user`、`fp-pad-num`。
- 符號：KiCad Y 向上 → SVG 要翻轉；接腳畫線、名稱在本體內側、腳號在線上方。
- 封裝：焊盤依形狀（rect／circle／oval／custom polygon）與角度畫出，鑽孔挖空顯示，焊盤中央標編號（字高隨焊盤縮放）。
- 所有文字內容必須做 XML 跳脫。

## 5. 前端

### 版面（單一視窗，最小 1100×700）

```
┌───────────────┬──────────────────────────────────┬────────────────┐
│ 搜尋／佇列     │ 預覽                              │ 元件庫          │
│ [輸入框]       │ [符號][封裝][3D]  ← 分頁          │ 目錄 [選擇…]    │
│ 結果列表       │                                  │ 庫名            │
│  □ C8734      │        （大畫面預覽）              │ 3D 路徑模式     │
│  □ …          │                                  │ ☑ 下載 3D       │
│ ────────────  │ 零件資訊列：MPN／封裝／腳數／警告   │ ☐ 覆寫既有      │
│ 已選 N 個      │                                  │ [加入元件庫]    │
│               │                                  │ 結果紀錄        │
└───────────────┴──────────────────────────────────┴────────────────┘
```

- 輸入框接受關鍵字或 C 編號（可貼多個）。Enter 搜尋。
- 點結果列 → 中間預覽該零件（符號預設）；勾選框決定加入佇列。多 unit 符號可切換 unit。
- 3D 分頁：three.js 載入 `Model()` 回傳的 OBJ（OBJLoader），軌道控制，Z 向上，自動取景；
  同時把封裝焊盤畫成平面當參考，讓使用者直接看出對位。three.js 用動態 import，只有切到 3D 分頁才載入。
- 「加入元件庫」執行 `Add(已勾選)`，依 `add:progress` 即時更新每個零件的狀態（added／updated／exists／failed＋警告）。
- 完成後顯示「在檔案管理員中顯示」與一段可複製的 KiCad 設定說明（符號庫檔案路徑、封裝庫 `.pretty` 路徑）。
- 所有錯誤都要在畫面上顯示（不可只 console）；載入中要有狀態。
- 介面語言：繁體中文。

### 視覺方向：製圖紙（drafting sheet）

給硬體工程師用的工具，視覺語言取自工程圖紙與 PCB 圖層，不做通用後台樣板。

- 主題提供「自動／淺色／深色」三段選擇；自動跟隨 `prefers-color-scheme`，選擇儲存在 `localStorage` 的 `partfetch-theme`。淺色為米白圖紙底＋極淡方格紋理；深色主題為深藍圖紙。兩個主題都要刻意設計。
- `kicad-cli` 匯出的圖（GitHub 來源與已安裝零件）以 `<img>` 顯示，頁面 CSS 進不去。匯出時固定帶 `--theme _builtin_default`（不受使用者 KiCad 裡的配色設定影響），前端再把這套固定配色逐色換成對應的設計 token（`frontend/src/lib/kicadColors.ts`），所以同一個零件加入元件庫前後看起來一致，也會跟著主題切換。不在對照表裡的顏色維持原色。
- 符號與封裝預覽可平移縮放（`frontend/src/lib/panzoom.ts`）：滾輪以游標為中心縮放（100%–1200%）、拖曳平移、雙擊或「重設」鈕回到原狀；鍵盤 `+`／`-` 縮放、方向鍵平移、`0` 重設。切換零件、分頁或 unit 時自動重設。縮放是改變圖層尺寸而不是 CSS transform，放大後文字才不會糊。
- 3D 預覽填滿整個圖框（不留方格邊），標籤避開圖框上的圖號列與標題欄。
- 顏色有語意：強調色即 PCB 圖層色（銅紅＝F.Cu、絲印黃、Fab 灰藍、courtyard 洋紅），預覽圖與 UI 狀態共用同一組 CSS 變數。
- 字體：`@fontsource/ibm-plex-sans`（介面）＋ `@fontsource/ibm-plex-mono`（零件編號、封裝名、數值），本地打包，不連外部 CDN。
- 階層靠尺度對比：預覽區最大、零件編號用等寬大字、輔助資訊小而淡。間距有節奏，不是每處同一個 padding。
- 預覽區像一張圖紙：有圖框與右下角標題欄（零件名／封裝／腳數）。
- hover／focus／active 狀態要明確；鍵盤可操作（Tab 順序、Enter 搜尋、上下鍵移動結果列、空白鍵勾選）；`focus-visible` 外框。
- 動效只用 `transform`／`opacity`，尊重 `prefers-reduced-motion`。
- 設計 token 集中在 `frontend/src/styles/tokens.css`；元件依功能分資料夾。
- 不得用 `v-html` 塞未經處理的外部字串；SVG 預覽來自本機 Go（已跳脫）可以用 `v-html`。商品圖片用 `<img>`，`referrerpolicy="no-referrer"`。

## 6. 錯誤分類與紀錄檔

GUI 錯誤以 `ErrorKind` 分類，並顯示繁體中文訊息。加入結果包含 `ErrorKind`；其他操作將安全訊息作為錯誤回傳。

| Kind | 使用者訊息 |
| --- | --- |
| `not_found` | 找不到零件編號或 GitHub repo，請確認編號／repo 名稱 |
| `rate_limited` | 來源暫時限制請求，請稍後再試 |
| `network` | 無法連線到對應來源，請確認網路連線 |
| `permission` | 無法寫入元件庫目錄，請確認目錄權限 |
| `disk_full` | 磁碟空間不足，無法寫入元件庫目錄 |
| `bad_data` | 零件資料無法轉換，這個零件可能不受支援 |
| `tool_missing` | 顯示找不到 `kicad-cli` 的安裝／路徑提示 |
| `unknown` | 操作失敗，詳情請看紀錄檔 |

紀錄檔為 `<os.UserConfigDir()>/kicad-partfetch/partfetch.log`，目錄權限 0700、檔案權限 0600。使用標準庫 `slog` 文字格式並以 append 寫入；開啟時若超過 1 MiB，舊檔輪替為 `partfetch.log.1`。每筆錯誤記錄 `op`、可用時的 `part`、`kind` 與原始 `err` 錯誤鏈；另在啟動時記錄 Info。紀錄檔不得包含憑證或 Authorization 標頭。

## 7. 測試與驗收

- Go：`internal/search`（三種回應路徑、405 錯誤、節流）、`internal/preview`（XML 跳脫、viewBox、每種焊盤形狀、15 個 fixture 都能產生可被 XML parser 解析的 SVG）、
  `internal/settings`（預設值、讀寫、損毀檔案回預設並回報）、`app`（以假 source 測 Search 的 C 編號分流、Preview、Add 事件順序、OpenInFileManager／OpenURL 的路徑與協定限制）。
- 前端：`npm run build`（含 `vue-tsc` 型別檢查）通過。
- 整體：`gofmt -l .`、`go vet ./...`、`go test ./...`、`~/go/bin/wails build` 全部成功。
