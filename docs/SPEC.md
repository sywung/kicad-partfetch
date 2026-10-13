# kicad-partfetch 規格

桌面（macOS / Windows / Linux）工具：輸入零件編號，從線上來源抓符號、封裝、3D 模型，寫進 KiCad 元件庫。
第一個來源是 EasyEDA／LCSC（C 編號）。Go 核心 + CLI，之後加 Wails GUI。

本專案為獨立實作，**不得**複製 LCSC3D、easyeda2kicad（皆 AGPL-3.0）的原始碼。

## 1. 套件結構

```
cmd/partfetch/            CLI 入口（標準庫 flag，子命令 add / info）
internal/model/           中立資料模型（單位 mm，KiCad 座標慣例）
internal/source/          Source 介面
internal/source/easyeda/  HTTP client（節流、鏡像、快取）＋解析＋轉成 model
internal/kicad/           寫檔：sexpr 工具、符號庫讀／併／寫、封裝 .kicad_mod
internal/library/         元件庫目標（目錄佈局、覆寫策略、原子寫入、備份）
```

Go module：`github.com/sywung/kicad-partfetch`，Go 1.27，核心只用標準庫。

```go
// internal/source
type Source interface {
    ID() string                                             // "easyeda"
    Fetch(ctx context.Context, partID string) (*model.Component, error)
    FetchModel(ctx context.Context, ref model.Model3DRef, format string) ([]byte, error) // "step" | "obj"
}
```

## 2. 元件庫佈局

`--out DIR --lib NAME` 產生：

```
DIR/NAME.kicad_sym
DIR/NAME.pretty/<footprint>.kicad_mod
DIR/NAME.3dshapes/<model title>.step
```

- 符號的 `Footprint` 屬性為 `NAME:<footprint>`。
- 封裝的 `(model ...)` 路徑為 `<model-base>/NAME.3dshapes/<title>.step`；`--model-base` 預設為 DIR 的絕對路徑，
  可給 `${KIPRJMOD}/libs` 這類字串（原樣寫入）。
- 名稱消毒：`/ \ : * ? " < > |` 與空白一律換成 `_`（例：`19-217/BHC-ZL1M2RY/3T` → `19-217_BHC-ZL1M2RY_3T`）。
- 既有項目：預設略過並回報 `exists`；`--overwrite` 才取代。
- 併入既有 `.kicad_sym`：以括號配對（須正確處理字串與跳脫）切出頂層 `(symbol "X" ...)` 區段，
  **未被取代的符號必須逐位元組不變**。修改前把舊檔複製成 `NAME.kicad_sym.bak`。
- 一律原子寫入（同目錄暫存檔 → rename）。

## 3. 輸出格式

寫 KiCad 8 格式（KiCad 8／9／10 皆可讀）：

- 符號庫：`(kicad_symbol_lib (version 20231120) (generator "kicad-partfetch") (generator_version "0.1") ...)`
- 封裝：`(footprint "NAME" (version 20240108) (generator "kicad-partfetch") (generator_version "0.1") (layer "F.Cu") ...)`

數值最多 4 位小數、去尾零、不得輸出 `-0`。字串一律雙引號並跳脫 `\` 與 `"`。

## 4. EasyEDA 來源

### 4.1 HTTP

- 元件：`GET https://easyeda.com/api/products/{C編號}/components?version=6.4.19.5`，鏡像 `https://lceda.cn/...`（相同路徑）。
  回應 `{"success":true,"result":{...}}`；`success` 非 true 或 `result` 為空 → `ErrNotFound`。
- STEP：`GET https://modules.easyeda.com/qAxj6KHrDKw4blvCG8QJPs7Y/{uuid}`
- OBJ：`GET https://modules.easyeda.com/3dmodel/{uuid}`
- **實測**：easyeda.com 前面是 CloudFront WAF，短時間約 25 個請求後整個 IP 被 403（body 含 `Request blocked`），
  鏡像 lceda.cn 不受影響。因此：
  - 每個 host 兩次請求至少間隔 1.2 秒（可注入時鐘，測試不得真的睡）。
  - 遇到 403／429／5xx／連線錯誤 → 換下一個鏡像；全部失敗且其中有 403／429 → `ErrRateLimited`。
  - 磁碟快取：`os.UserCacheDir()/kicad-partfetch/components/<ID>.json`（7 天）、`models/<uuid>.<ext>`（永久）。`--no-cache` 關閉。
  - Client 的 `http.RoundTripper`、時鐘、快取目錄都要能注入；**測試不得連網、不得開 listener**。
- 編號驗證：`^C\d+$`（大小寫不拘，正規化成大寫）。

### 4.2 單位與座標

EasyEDA 1 單位 = 10 mil = **0.254 mm**。

- 符號：原點 `dataStr.head.x/y`；`X = (x-ox)*0.254`，`Y = -(y-oy)*0.254`（Y 反向）。
- 封裝：原點 `packageDetail.dataStr.head.x/y`；`X = (x-ox)*0.254`，`Y = (y-oy)*0.254`（Y 不反向）。

### 4.3 符號

`result.subparts` 非空 → 每個 subpart 是一個 unit（依序 1..N，各自用自己的 `dataStr.head` 原點），忽略頂層 `dataStr.shape`。
否則頂層 `dataStr.shape` 為唯一 unit。

KiCad 輸出：單一 unit 全部放 `NAME_0_1`；多 unit 放 `NAME_<n>_1`。

形狀字串以 `~` 分欄（接腳先以 `^^` 分段）：

**接腳 `P`**（7 段）

```
P~show~0~B1~360~250~180~gge5~0 ^^ 360~250 ^^ M 360 250 h 10~#880000
  ^^ 1~373.7~254~0~GND~start~~~#0000FF ^^ 1~369.5~249~0~B1~end~~~#0000FF ^^ 0~367~250 ^^ 0~M 370 253 L ...
```

| 段 | 欄位 |
|---|---|
| 0 | `P`、顯示、**電氣型別**[2]、SPICE 腳號[3]、x[4]、y[5]、旋轉[6]、id |
| 2 | 路徑 `M x y h L` 或 `M x y v L`（已是最終座標）、顏色 |
| 3 | 名稱：顯示旗標[0]、…、文字[4] |
| 4 | **顯示腳號**：顯示旗標[0]、…、文字[4] |
| 5 | 反相圓點：顯示旗標[0] |
| 6 | 時脈：顯示旗標[0] |

- 連接點 = 段 0 的 (x,y)。
- **腳號用段 4 的文字**，為空才退回段 0 的 SPICE 腳號。⚠️ 兩者不一定相同，而對應封裝焊盤的是段 4
  （C5446：SPICE 2 = Vin，顯示腳號 3；焊盤 3 才是 Vin）。
- 長度 = `|L| * 0.254`。
- **方向只從路徑推，不要用旋轉欄位**（旋轉欄位可能為空字串）：
  `h` 且 L>0 → KiCad 角度 0；`h` 且 L<0 → 180；`v` 且 L>0（螢幕向下）→ 270；`v` 且 L<0 → 90。
- 電氣型別：`0` 或空 → `unspecified`、`1` → `input`、`2` → `output`、`3` → `bidirectional`、`4` → `power_in`。
- 圖形樣式：圓點＋時脈 → `inverted_clock`、只有圓點 → `inverted`、只有時脈 → `clock`、否則 `line`。
- 全部接腳的名稱顯示旗標都是 0 → 符號加 `(pin_names (offset 1.016) hide)`；腳號同理 → `(pin_numbers hide)`。

**圖形**

| 類型 | 欄位 | KiCad |
|---|---|---|
| `R` | x,y,rx,ry,w,h,stroke,strokeW,style,fill | `rectangle`，`fill background` |
| `E` | cx,cy,rx,ry,stroke,strokeW,style,fill | rx==ry → `circle`；否則 32 段 `polyline` |
| `C` | cx,cy,r,stroke,strokeW,style,fill | `circle` |
| `PL` | 點列（空白分隔的 x y…）,stroke,strokeW,style,fill | `polyline` |
| `PG` | 同 PL | 封閉 `polyline`（補回起點）；fill 非 `none` → `fill outline` |
| `A` | SVG path（`M … A rx ry rot large sweep x y`） | `arc`（start／mid／end） |
| `PT` | SVG path（M／L／H／V／Z；A、C 以線段逼近） | `polyline` |
| `T` | `T~L~x~y~rot~color~font~size~…~comment~<text>~<visible>~…` | 可見者輸出 `text`，字高 1.27 |

未知類型：計數後放進該零件的 warnings，不得讓轉換失敗。

**屬性**

- `Reference`：`head.c_para.pre` 去掉 `?` 與數字（`U?` → `U`），空則 `U`。
- `Value`：`c_para.name`，空則 `result.title`。符號名同 Value（消毒後）。
- `Footprint`：`NAME:<footprint>`；`Datasheet`：空字串。
- 另加隱藏屬性：`LCSC`（C 編號）、`Manufacturer`、`MPN`（`Manufacturer Part`）、`JLCPCB Class`（`JLCPCB Part Class`），缺值就不寫。
- Reference 放在圖形 bbox 上方 1.27、Value 放下方 1.27。

### 4.4 封裝

封裝名 = `packageDetail.title`（消毒後）。

圖層對照：1→F.Cu、2→B.Cu、3→F.SilkS、4→B.SilkS、5→F.Paste、6→B.Paste、7→F.Mask、8→B.Mask、
10→Edge.Cuts、12→Cmts.User、13→F.Fab、14→B.Fab、15→Dwgs.User、101→F.Fab。
99（元件外形）與 100（引腳外形）不輸出圖形（99 用於計算 courtyard）。其他未知圖層 → warning 並略過。

**`PAD`**

```
PAD~OVAL~4010.323~2998.929~13.7795~5.9055~11~~3~1.9685~<points>~270~gge229~11.811~<hole points>~Y~...
     [1]  [2]      [3]      [4]     [5]   [6] [7][8] [9]    [10]   [11] [12]   [13]    [14]      [15]
```

- [1] 形狀、[2][3] 中心、[4][5] 寬高（焊盤本地座標）、[6] 圖層、[8] 編號、[9] 孔半徑、[10] 外形點列、
  [11] 旋轉（度，空 = 0）、[13] 槽孔長度、[15] 是否電鍍（`Y`／`N`）。
- 類型：圖層 11 且孔半徑 > 0 → `thru_hole`（[15]=`N` 則 `np_thru_hole`），layers `"*.Cu" "*.Mask"`；
  圖層 1 → `smd`，`"F.Cu" "F.Paste" "F.Mask"`；圖層 2 → `smd`，`"B.Cu" "B.Paste" "B.Mask"`。
- 形狀：`RECT` → `rect`；`ELLIPSE` → 寬高相等 `circle`，否則 `oval`；`OVAL` → `oval`；
  `POLYGON` → `custom`（anchor `rect`、size 0.2×0.2、角度 0，`primitives` 一個 `gr_poly`，點為 [10] 相對焊盤中心的 mm 座標）。
- 角度：KiCad `(at x y angle)` 的 angle = [11]（0 時省略）。
- 鑽孔：直徑 = 2×[9]×0.254。[13] > 0 為槽孔：長邊 = max(直徑, [13]×0.254)、短邊 = min(…)，
  **長邊對齊焊盤本地的長軸**（寬 ≥ 高 → `(drill oval 長 短)`，否則 `(drill oval 短 長)`）。

**其他**

| 類型 | 欄位 | KiCad |
|---|---|---|
| `HOLE` | cx,cy,r | `np_thru_hole circle`，size = drill = 2r，無編號，`"*.Cu" "*.Mask"` |
| `VIA` | cx,cy,直徑,net,孔半徑 | `thru_hole circle`，無編號 |
| `TRACK` | 線寬,圖層,net,點列 | 每段一條 `fp_line` |
| `CIRCLE` | cx,cy,r,線寬,圖層 | `fp_circle` |
| `RECT` | x,y,w,h,圖層,id,locked,線寬 | `fp_rect` |
| `ARC` | 線寬,圖層,net,SVG path | `fp_arc`（start／mid／end） |
| `SOLIDREGION` | 圖層,net,SVG path,類型 | `solid` → 填滿 `fp_poly`（線寬 0）；`npth` → `Edge.Cuts` 外框；`cutout` → 略過 |
| `SVGNODE` | JSON | `attrs.c_etype == "outline3D"` → 3D 模型參照 |
| `TEXT` | — | 略過 |

SVG path：指令與數字之間可能沒有空白、可能用逗號（`M3990.1,3002.8 A2.86,2.86 0 0 0 3990.1,2997.1`、`L3980.6,3016.8 Z`）。
圓弧依 SVG 端點參數化算出圓心，mid 取弧的中點；用在多邊形內時以 ≤ 10° 的線段逼近。

**固定項目**

- `Reference`（`REF**`）在 F.SilkS、所有圖形與焊盤 bbox 上方 1 mm；`Value` 在 F.Fab、bbox 下方 1 mm。
- `attr`：有任何 thru_hole 焊盤 → `through_hole`，否則 `smd`。
- Courtyard：F.CrtYd 上的 `fp_rect`，範圍 = bbox(所有焊盤 ∪ 圖層 99 的 SOLIDREGION) 外擴 0.25 mm，
  四捨五入到 0.01 mm，線寬 0.05。

**3D 模型**

`SVGNODE` 的 `attrs`：`uuid`、`title`、`c_origin`（`"x,y"`）、`z`、`c_rotation`（`"rx,ry,rz"`）。
解析成 `model.Model3DRef{UUID, Title, Origin(mm，封裝座標), Z(mm), Rotation[3]}`。
`(model ...)` 的 offset／rotate **只由單一函式** `easyeda.StepPlacement` 決定，公式見第 7 節。

## 5. CLI

```
partfetch add [flags] C2040 C25804 ...
  --out DIR          輸出目錄（預設 .）
  --lib NAME         元件庫名（預設 partfetch）
  --overwrite        取代既有的符號／封裝／模型
  --no-3d            不下載 3D 模型
  --model-base STR   寫進封裝的 3D 路徑前綴（預設 DIR 絕對路徑）
  --no-cache
  --json             每個零件輸出一行 JSON 結果
partfetch info C2040   印出名稱、封裝、接腳數、焊盤數、是否有 3D
```

- 每個零件獨立處理，單一零件失敗不影響其他；有任何失敗則 exit code 1。
- 結果需列出：symbol／footprint／model 各自是 `added`／`updated`／`exists`／`skipped`／`failed`，以及 warnings。
- 3D 下載失敗只算 warning（符號與封裝照寫，封裝內不寫 `(model)`）。

## 6. 測試與驗收

測試資料：`internal/source/easyeda/testdata/C*.json`（15 個真實 API 回應）。
**獨立期望值**：`testdata/expect/C*.json`，由另一套程式直接從原始資料算出，是事實來源——
測試必須逐一比對，**若實作與期望值不符，改實作，不要改期望值、不要加別名或特例遷就**；
若認為期望值本身有錯，停下來在回報中說明，不要自行修改。

必要測試（表格驅動、Arrange-Act-Assert）：

1. 15 個 fixture 全部：unit 數、每個 unit 的接腳（number／name／x／y／angle／length，容差 1e-3）、
   焊盤（number／x／y／w／h／drill／slot，容差 1e-3）、`npth_holes` 數量、3D uuid 與 title。
2. `RECT` 焊盤：以 (中心, 寬高, 角度) 算出的四角，與欄位 [10] 的點集合一致（容差 0.01 mm）。
3. 槽孔方向：C16214 焊盤 `3`、C134092 焊盤 `0`。
4. SVG path 解析：無空白、逗號、相對／絕對 H／V、圓弧的 start／mid／end。
5. 符號庫合併：未取代的符號逐位元組不變；取代只換目標；字串內含括號與 `\"` 時不誤判。
6. 名稱消毒、數值格式（無 `-0`、無尾零）。
7. HTTP：鏡像切換、403 → `ErrRateLimited`、節流間隔（假時鐘）、快取命中不發請求。
8. 端到端：以 fixture 餵假的 RoundTripper 跑 `add`，檢查三類檔案的路徑與 `Footprint` 屬性。

KiCad 實機驗證（macOS，測試在找不到執行檔時 `t.Skip`）：
`/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli`

- `kicad-cli sym export svg <lib>.kicad_sym -o <dir>` exit 0，且每個符號都有 SVG。
- `kicad-cli fp export svg <lib>.pretty -o <dir>` exit 0，且每個封裝都有 SVG。

完成條件：`go build ./...`、`go vet ./...`、`go test ./...` 全過；函式 < 50 行、檔案 < 800 行；錯誤不得被吞掉。

## 7. 3D 模型對位（M1b，取代 4.4 的「初版」placement）

以下公式已用 `kicad-cli pcb render` 對 SOT-23、SOT-223、USB-C、DC 插座實際算圖驗證。

**事實**

- OBJ 與 STEP 使用同一個座標系（同原點、單位 mm、Z 向上），所以用 OBJ 的頂點算包圍盒，結果套用到 STEP。
- 模型原點**不一定**在模型中心（C2337 排針的 X 範圍是 -100.33..1.27）。
- `attrs.c_origin` **不可靠**（C6186 的值偏離實際位置約 4 mm）。可靠的是 `SVGNODE.childNodes[]` 裡
  `polyline` 的 `attrs.points`——那是模型在封裝平面上的投影外框（EasyEDA 封裝座標）。
- OBJ 下載內容可能是 gzip（開頭 `1f 8b`）且**沒有** `Content-Encoding` 標頭 → 以 magic 判斷後解壓。STEP 同樣處理。

**解析**

`model.Model3DRef` 增加 `OutlineCenter`（mm，封裝座標，Y 向下）＝所有 childNodes 點的包圍盒中心，轉成 mm（減原點、乘 0.254）。
沒有任何外框點時退回 `c_origin`。

**計算**（`easyeda.StepPlacement(ref, objVertices)`）

1. 每個 OBJ 頂點（`v x y z` 行）依序繞 X、Y、Z 軸以數學正向（右手、逆時針）旋轉 `rx, ry, rz` 度。
2. 取旋轉後的 XY 包圍盒中心 `(bx, by)` 與 `zmin`。
3. `offset = (OutlineCenter.X - bx,  -OutlineCenter.Y - by,  Z - zmin)`，其中 `Z = attrs.z * 0.254`。
4. `rotate = ((360-rx) mod 360, (360-ry) mod 360, (360-rz) mod 360)`。

沒有 OBJ（下載失敗）時：offset = (OutlineCenter.X, -OutlineCenter.Y, 0)、rotate 同上，並加 warning「3D 位置未經校正」。

**流程變更**

- `add` 下載 STEP 時一併下載 OBJ（進快取），用於計算 placement；`--obj` 旗標另存 `NAME.3dshapes/<title>.obj`。
- 期望值（容差 0.01）：

| 零件 | offset | rotate |
|---|---|---|
| C2337 | (49.53, 0, 0) | (0, 0, 0) |
| C134092 | (0, -0.93, 0) | (0, 0, 180) |
| C16214 | (-3.65, 2.275, 0) | (0, 0, 90) |
| C6186 | (-0.1, 0, 0.0494) | (0, 0, 180) |

測試用的精簡 OBJ：`testdata/obj/<零件>.obj`（真實模型包圍盒的 8 個角點）。C5446 期望 offset (0, 0, 0)、rotate (0, 0, 270)。

## 8. M1 驗收後的修正（2026-10-11）

實機檢視 M1 輸出後發現、須修正：

1. 符號屬性（Reference／Value／其餘）的 `(at x y angle)` 角度寫成 90，文字變直的 → 一律 0。
2. 封裝的 Reference 與 Value 上下顛倒：封裝座標 Y 向下，「上方」是較小的 Y。
   Reference 在 bbox 上方（`minY - 1`）、Value 在下方（`maxY + 1`）。
3. 封裝文字改用 KiCad 8 語法：`(property "Reference" "REF**" (at x y 0) (layer "F.SilkS") (effects (font (size 1 1) (thickness 0.15))))`、
   `(property "Value" "<封裝名>" ... (layer "F.Fab") ...)`；Value 的內容是**封裝名**，不是零件名。
4. 依規格**刻意略過**的東西不得產生 warning：圖層 99／100 的圖形、`cutout` 類型的 SOLIDREGION、`TEXT`。
   warning 只留給真正未知的形狀類型或未知圖層。現況每個零件都印 `unknown footprint "shape SOLIDREGION" (N)`，是雜訊。
5. 槽孔短邊 = `min(鑽孔直徑, 槽長)`（現況寫死成鑽孔直徑）。
6. 符號 `Datasheet` 屬性：`result.lcsc.url` 有值就填它（LCSC 商品頁），否則空字串。
