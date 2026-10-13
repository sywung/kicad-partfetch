# kicad-partfetch 元件庫管理規格（M2b）

> 2026-10-12。在既有「加入」之外，補上「讀」（列出元件庫內容）與「刪」（移除已加入的零件）。
> CLI 與 GUI 都要有。沿用 `docs/SPEC.md` 第 2 節的元件庫佈局，不改既有 `add` 的行為與輸出。

## 1. 名詞

- 元件庫目錄 `<out>`、元件庫名稱 `<lib>`（經 `kicad.Sanitize`）
- 符號庫 `<out>/<lib>.kicad_sym`
- 封裝 `<out>/<lib>.pretty/<fp>.kicad_mod`
- 3D 模型 `<out>/<lib>.3dshapes/<file>.step`（`add --obj` 時另有同主檔名的 `.obj`）

## 2. internal/kicad

### 2.1 `RemoveSymbols(existing []byte, names []string) (out []byte, removed []string, err error)`

- 用既有的 `topSymbols` 找出最上層 `(symbol "NAME" ...)` 的範圍。
- 對 `names` 中存在的每個符號：刪掉 `[start, end)`，並一併刪掉該範圍**之前**同一行的空白（空格、tab）與**之後**的一個換行（`\n` 或 `\r\n`）。
- **其餘位元組一字不動**（檔頭、其他符號、非符號的最上層運算式、註解、換行風格都保留）。這點與 `MergeSymbols` 會重寫檔頭不同，刻意如此。
- `removed` 依檔案內出現順序回傳實際刪掉的名稱；`names` 中不存在的名稱不算錯，只是不會出現在 `removed`。
- 名稱比對區分大小寫、完全相符。
- `existing` 不是合法符號庫時回傳 error（沿用 `topSymbols` 的錯誤）。

### 2.2 `FootprintModelPaths(data []byte) []string`

- 回傳 `.kicad_mod` 內所有 `(model "PATH"` 的 PATH（已還原跳脫），依出現順序。沒有就回空 slice（非 nil）。
- 字串內的 `(model "` 不可誤判（要用與 `topSymbols` 同等級的括號／字串狀態掃描，不可用單純的 `bytes.Index`）。

## 3. internal/library

```go
type Entry struct {
    Name         string // 符號名稱
    Footprint    string // 封裝名稱，不含 "<lib>:" 前綴
    LCSC         string
    HasFootprint bool   // <lib>.pretty/<Footprint>.kicad_mod 存在
    HasModel     bool   // 該封裝檔引用的 3D 檔存在於 <lib>.3dshapes/
}
func List(opts Options) ([]Entry, error)

type RemoveResult struct {
    Name      string
    Symbol    string // removed | missing
    Footprint string // removed | kept | missing | none | failed
    Model     string // removed | kept | missing | none | failed
    Error     string // 僅在有 failed 時非空
}
func Remove(opts Options, names []string) ([]RemoveResult, error)
```

### 3.1 List

- 符號庫檔不存在 → 回空 slice（非 nil）、nil error。
- 順序同檔案內順序。
- `Footprint` 屬性值形如 `lib:fp` 時取冒號後；沒有冒號就整串。
- `HasModel`：封裝檔存在、且 `FootprintModelPaths` 至少一個路徑的**檔名（最後一段，`/` 與 `\` 都當分隔）**在 `<out>/<lib>.3dshapes/` 下存在。

### 3.2 Remove

`names` 的每一項可以是符號名稱，或該符號的 LCSC 編號（`C` 加數字，不分大小寫）；先比符號名稱，沒有再比 LCSC。同一個符號被重複指定時只處理一次，但每個輸入項都要有一筆結果（重複項回報與第一次相同的結果）。

步驟：

1. 讀符號庫。檔案不存在 → 每一項回 `Symbol: "missing", Footprint: "none", Model: "none"`，nil error。
2. 解析出要刪的符號集合 R。找不到的項 → `Symbol: "missing", Footprint: "none", Model: "none"`。
3. R 非空時：先把原檔寫成 `<lib>.kicad_sym.bak`（同 `add` 的作法），再用 `RemoveSymbols` 的結果 `atomicWrite` 回去。這一步失敗 → 整個函式回 error，不動任何封裝／3D 檔。
4. 對 R 中每個符號決定封裝：
   - `Footprint` 屬性空白 → `none`
   - 屬性有 `lib:` 前綴且前綴**不等於** `<lib>` → `kept`（不是這個元件庫的封裝，不碰）
   - **刪除後仍留在符號庫裡**的任一符號引用同一個封裝名稱 → `kept`
   - 封裝檔不存在 → `missing`
   - 否則刪檔 → `removed`；刪檔失敗 → `failed`
   - 同一批 R 內多個符號共用同一封裝、且沒有留存的符號引用它：只刪一次，這幾筆都回 `removed`
5. 對 R 中每個符號決定 3D：
   - 封裝狀態不是 `removed`（包含 `kept`／`missing`／`none`／`failed`）→ 封裝是 `kept` 時回 `kept`，其餘回 `none`
   - 刪封裝檔**之前**先讀出它的 `FootprintModelPaths`，取檔名；沒有任何路徑 → `none`
   - 該檔名仍被 `<lib>.pretty/` 下**其餘留存的** `.kicad_mod` 引用（同樣以檔名比對）→ `kept`
   - `<lib>.3dshapes/<檔名>` 不存在 → `missing`
   - 否則刪檔 → `removed`；若有同主檔名的 `.obj` 一併刪；失敗 → `failed`
   - 只用檔名（`filepath.Base` 的效果，且要自行處理 `\`），**絕不**依封裝檔裡寫的目錄去刪檔；檔名為空、`.`、`..` 一律視為 `none`
6. 不刪目錄（即使變空）。

## 4. CLI

```
partfetch list   --out DIR --lib NAME [--json]
partfetch remove --out DIR --lib NAME [--json] NAME_OR_CNUMBER...
```

- usage 字串改為 `usage: partfetch <add|info|list|remove> ...`
- `list` 文字輸出：每個符號一行 `NAME<TAB>FOOTPRINT<TAB>LCSC<TAB>footprint=yes|no<TAB>model=yes|no`；空元件庫不輸出任何東西、exit 0。`--json` 輸出一個 JSON 陣列（空時是 `[]` 不是 `null`），欄位 `name`、`footprint`、`lcsc`、`hasFootprint`、`hasModel`。
- `remove` 文字輸出：每個輸入項一行 `NAME: symbol=<s> footprint=<s> model=<s>`，有 Error 時行尾加 ` error=<訊息>`。`--json` 每行一個物件（同 `add --json` 的風格），欄位 `name`、`symbol`、`footprint`、`model`、`error`（空則省略）。
- `remove` 沒給名稱 → stderr 印 `remove requires one or more symbol names or part numbers`、exit 2。
- exit code：全部 `symbol=removed` 且沒有 `failed` → 0；有任何 `symbol=missing` 或任何 `failed` → 1（封裝／3D 的 `missing` 不算失敗，符號已移除）；函式回 error → stderr 印訊息、exit 1。
- `list`／`remove` 不需要網路，不得呼叫 `source.Source`。

## 5. GUI 綁定（app.App）

```go
type LibraryEntry struct {
    Name, Footprint, LCSC  string
    HasFootprint, HasModel bool
}
type RemoveResult struct{ Name, Symbol, Footprint, Model, Error string }

func (a *App) ListLibrary() ([]LibraryEntry, error)            // 既有，改用 library.List
func (a *App) RemoveFromLibrary(names []string) ([]RemoveResult, error)
```

- `RemoveFromLibrary`：`names` 為空 → error「請先選擇要移除的項目」。使用已儲存的設定（同 `Add`）。底層回 error → 回「移除失敗，請確認元件庫目錄可寫入」。
- 回傳的 slice 一律非 nil（經 JSON 後是 `[]`，不是 `null`）；要有經過 `encoding/json` 的測試。
- 更新 `frontend/src/types/wails.d.ts` 與 `frontend/src/lib/bridge.ts`（`api.removeFromLibrary`）。

## 6. 前端（LibraryPane「已安裝符號」區）

- 列出**全部**項目（拿掉只顯示前 12 個的限制），清單區自己捲動（`max-height` 約 260px）。
- 項目超過 8 個時，在清單上方顯示一個過濾輸入框（比對名稱、封裝、LCSC，不分大小寫）。
- 每列：名稱、封裝（沒有則 `—`）、LCSC 編號（有才顯示）；`HasFootprint` 為 false 時在封裝旁標「缺封裝檔」。
- 點一列（且該列有 LCSC 編號）→ 在中間預覽區載入該零件（沿用 `selectItem` 的流程，以 `{ PartID: LCSC }` 呼叫）。沒有 LCSC 的列不可點。
- 每列右側一個移除鈕（`aria-label="移除 <名稱>"`）。按下後**該列原地**換成確認狀態：文字「移除符號，以及沒有其他符號使用的封裝與 3D 檔？」加「移除」「取消」兩個按鈕。**不得使用 `window.confirm`／`alert`**。同時間只有一列處於確認狀態。
- 按「移除」→ 呼叫 `api.removeFromLibrary([name])` → 成功後重新載入清單，狀態列顯示 `已移除 <名稱>（封裝 <狀態>、3D <狀態>）`，狀態用中文：removed 已移除、kept 保留（仍被使用）、missing 檔案不存在、none 無、failed 失敗。有 `Error` 或呼叫拋錯 → 狀態列顯示錯誤（error 樣式），清單仍重新載入。
- 移除進行中，該列按鈕 disabled。
- 鍵盤：移除鈕、確認、取消都是 `<button>`，可用 Tab 到達。
- 視覺沿用既有 tokens（`--danger` 用於確認狀態的「移除」鈕），不新增色彩或字型。CSS 一條規則一行，不要把多條規則擠在同一行。
- 注意 Wails 把 Go 的 error 以**字串**reject：`App.vue` 的 `errorText` 目前只認 `Error` 物件，字串會被換成通用句。請一併修成：字串就直接用。

## 7. 測試（必須照這張表寫，不是照實作寫）

`internal/kicad`：
- RemoveSymbols 刪中間一個：結果等於「原字串去掉那一段」的**逐位元組**期望值（期望值寫死在測試裡，不可由被測函式產生）；其餘符號、檔頭原樣。
- 刪第一個、刪最後一個、一次刪兩個、刪不存在的名稱（輸出與輸入完全相同、`removed` 為空）。
- 符號內的字串含 `(symbol "X"` 與跳脫引號時不誤判。
- CRLF 換行的檔案：刪除後其餘位元組不變。
- 檔頭是別的 generator／version、含非符號的最上層運算式：原樣保留。
- FootprintModelPaths：零個、一個、兩個、路徑含跳脫的 `\\`、字串內含 `(model "` 的陷阱。

`internal/library`（用 `t.TempDir()`，檔案內容由測試自己寫，不經網路）：
- List：不存在 → 空非 nil；`HasFootprint`／`HasModel` 的 true 與 false 各一。
- Remove：兩個符號共用封裝，刪其中一個 → 封裝 `kept`、檔案還在、3D `kept`；再刪另一個 → 封裝與 3D `removed`、檔案不在。
- Remove：兩個不同封裝引用同一個 step，刪其中一個 → 封裝 `removed`、3D `kept`、step 還在。
- Remove：以 LCSC 編號（小寫 `c2040`）指定可刪。
- Remove：不存在的名稱 → `missing`／`none`／`none`，符號庫位元組不變，**不產生 `.bak`**。
- Remove：`Footprint` 是 `otherlib:X` → `kept`，即使 `<lib>.pretty/X.kicad_mod` 剛好存在也不刪。
- Remove：封裝檔的 model 路徑是 `../../secret.step`、`C:\\x\\..\\evil.step` → 只會動到 `<lib>.3dshapes/` 下同檔名的檔案；在 `<out>` 之外放一個同名檔，測完它必須還在。
- Remove：有 `.bak` 且內容等於刪除前的符號庫。
- Remove：同一名稱重複傳兩次 → 兩筆結果相同、不報錯。
- Remove：同主檔名的 `.obj` 會一併刪。
- Remove：封裝檔已不存在 → `removed`／`missing`／`none`（驗收時補上；原實作在這條路徑回空字串）。

`internal/cli`：list 文字與 `--json`（含空元件庫輸出 `[]`）、remove 的輸出格式與 exit code（0／1／2 各一）。

`app`：`RemoveFromLibrary` 空輸入的錯誤、結果經 `json.Marshal` 後不含 `null`、`ListLibrary` 新欄位。

所有既有測試必須維持通過；不得修改 `testdata/expect/*.json`。

## 8. 不做

- 不做還原（undo）、不做垃圾桶；符號庫有 `.bak`，封裝與 3D 可重新下載。
- 不做批次多選刪除（GUI 一次一個；CLI 可多個）。
- 不改 `add` 的行為。
- 不碰 `build/`。
