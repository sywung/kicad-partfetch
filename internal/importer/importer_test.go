package importer

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/model"
)

func TestImportKnownZipLayouts(t *testing.T) {
	tests := []struct {
		name                                 string
		files                                map[string]string
		symbolName, footprintName, modelName string
	}{
		{name: "SnapEDA 根目錄", files: map[string]string{"PART.kicad_sym": symbolFile("Snap", "PART", "20231120"), "PART.kicad_mod": footprintFile("PART", "models/PART.step"), "PART.step": "snap model"}, symbolName: "Snap", footprintName: "PART", modelName: "PART.step"},
		{name: "Ultra Librarian 遞迴目錄", files: map[string]string{"KiCAD/2026/UL.kicad_sym": symbolFile("Ultra", "Package", "20231120"), "KiCAD/2026/footprints.pretty/Package.kicad_mod": footprintFile("Package", "3D/Package.step"), "KiCAD/3D/Package.step": "ul model"}, symbolName: "Ultra", footprintName: "Package", modelName: "Package.step"},
		{name: "SamacSys 遞迴目錄與舊格式共存", files: map[string]string{"LIB_PART/PART/KiCad/PART.kicad_sym": symbolFile("Samac", "PART", "20231120"), "LIB_PART/PART/KiCad/PART.kicad_mod": footprintFile("PART", "../3D/PART.stp"), "LIB_PART/PART/3D/PART.stp": "sam model", "LIB_PART/PART/KiCad/PART.lib": "legacy", "LIB_PART/PART/KiCad/PART.dcm": "legacy", "LIB_PART/PART/KiCad/PART.mod": "legacy"}, symbolName: "Samac", footprintName: "PART", modelName: "PART.stp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { testLayoutImport(t, tt.files, tt.symbolName, tt.footprintName, tt.modelName) })
	}
}

func testLayoutImport(t *testing.T, files map[string]string, symbol, footprint, modelFile string) {
	t.Helper()
	zipPath := writeZip(t, t.TempDir(), files, false)
	out := t.TempDir()
	results := Import(library.Options{Out: out, Name: "demo", ModelBase: "${KIPRJMOD}/libs"}, []string{zipPath})
	if len(results) != 1 {
		t.Fatalf("results=%d, want 1", len(results))
	}
	result := results[0]
	if result.Error != "" {
		t.Fatalf("error=%s", result.Error)
	}
	if result.Symbols[0] != (Item{Name: symbol, Status: "added"}) {
		t.Fatalf("symbols=%+v", result.Symbols)
	}
	if result.Footprints[0] != (Item{Name: footprint, Status: "added"}) {
		t.Fatalf("footprints=%+v", result.Footprints)
	}
	if result.Models[0] != (Item{Name: modelFile, Status: "added"}) {
		t.Fatalf("models=%+v", result.Models)
	}
	symbolData, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(symbolData, []byte(`"demo:`+footprint+`"`)) {
		t.Fatalf("symbol Footprint was not rewritten: %s", symbolData)
	}
	footprintData, err := os.ReadFile(filepath.Join(out, "demo.pretty", footprint+".kicad_mod"))
	if err != nil {
		t.Fatal(err)
	}
	wantModelPath := `${KIPRJMOD}/libs/demo.3dshapes/` + modelFile
	if !bytes.Contains(footprintData, []byte(kicad.Quote(wantModelPath))) {
		t.Fatalf("model path not rewritten: %s", footprintData)
	}
	modelData, err := os.ReadFile(filepath.Join(out, "demo.3dshapes", modelFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(modelData) == 0 {
		t.Fatal("model file is empty")
	}
}

func TestDirectoryAndZipInputsProduceSameArtifacts(t *testing.T) {
	t.Run("目錄輸入與 zip 輸入結果一致", func(t *testing.T) {
		files := simpleBundle("Board", "Board")
		root := t.TempDir()
		writeTree(t, root, files)
		zipPath := writeZip(t, t.TempDir(), files, false)
		leftOut, rightOut := t.TempDir(), t.TempDir()
		left := Import(library.Options{Out: leftOut, Name: "demo", ModelBase: "/models"}, []string{root})[0]
		right := Import(library.Options{Out: rightOut, Name: "demo", ModelBase: "/models"}, []string{zipPath})[0]
		left.Source, right.Source = "", ""
		if !reflect.DeepEqual(left, right) {
			t.Fatalf("directory result=%+v zip result=%+v", left, right)
		}
		if !reflect.DeepEqual(snapshot(t, leftOut), snapshot(t, rightOut)) {
			t.Fatal("directory and zip artifacts differ")
		}
	})
}

func TestImportSingleFootprintFile(t *testing.T) {
	t.Run("單一 kicad_mod 檔案", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "Solo.kicad_mod")
		writeFileForTest(t, path, `(footprint "Solo" (property "Value" "Solo"))`)
		out := t.TempDir()
		result := Import(library.Options{Out: out, Name: "demo"}, []string{path})[0]
		if result.Error != "" {
			t.Fatalf("error=%s", result.Error)
		}
		if !reflect.DeepEqual(result.Footprints, []Item{{Name: "Solo", Status: "added"}}) {
			t.Fatalf("footprints=%+v", result.Footprints)
		}
		if _, err := os.Stat(filepath.Join(out, "demo.pretty", "Solo.kicad_mod")); err != nil {
			t.Fatal(err)
		}
	})
}

func TestImportIgnoresMacOSAndDotFiles(t *testing.T) {
	t.Run("__MACOSX 與點開頭項目忽略", func(t *testing.T) {
		files := map[string]string{"visible.step": "visible", "__MACOSX/hidden.kicad_sym": symbolFile("Hidden", "x", "20231120"), ".hidden.kicad_mod": footprintFile("Hidden", "hidden.step"), "folder/.secret.step": "secret"}
		out := t.TempDir()
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
		if result.Error != "" {
			t.Fatalf("error=%s", result.Error)
		}
		if !reflect.DeepEqual(result.Models, []Item{{Name: "visible.step", Status: "added"}}) {
			t.Fatalf("models=%+v", result.Models)
		}
		if _, err := os.Stat(filepath.Join(out, "demo.3dshapes", "hidden.step")); !os.IsNotExist(err) {
			t.Fatalf("hidden model stat error=%v", err)
		}
	})
}

func TestImportZipSlipPathsStayInsideOutput(t *testing.T) {
	t.Run("zip slip 與絕對路徑只產生輸出目錄內檔案", func(t *testing.T) {
		parent := t.TempDir()
		zipPath := writeZip(t, parent, map[string]string{"../../evil.kicad_mod": `(footprint "evil")`, "/abs/evil.step": "evil model"}, false)
		out := filepath.Join(parent, "out")
		relativeEscape := filepath.Clean(filepath.Join(out, "../../evil.kicad_mod"))
		absoluteEscape := filepath.Join(string(filepath.Separator), "abs", "evil.step")
		_, relativeBefore := os.Lstat(relativeEscape)
		_, absoluteBefore := os.Lstat(absoluteEscape)
		result := Import(library.Options{Out: out, Name: "demo"}, []string{zipPath})[0]
		if result.Error != "" {
			t.Fatalf("error=%s", result.Error)
		}
		if _, err := os.Stat(filepath.Join(out, "demo.pretty", "evil.kicad_mod")); err != nil {
			t.Fatalf("footprint missing; result=%+v output=%#v: %v", result, snapshot(t, out), err)
		}
		if _, err := os.Stat(filepath.Join(out, "demo.3dshapes", "evil.step")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(relativeEscape); os.IsNotExist(relativeBefore) && !os.IsNotExist(err) {
			t.Fatalf("zip slip created outside file: %s", relativeEscape)
		}
		if _, err := os.Lstat(absoluteEscape); os.IsNotExist(absoluteBefore) && !os.IsNotExist(err) {
			t.Fatalf("absolute zip path created outside file: %s", absoluteEscape)
		}
		if len(snapshot(t, out)) != 2 {
			t.Fatalf("output has unexpected files: %#v", snapshot(t, out))
		}
	})
}

func TestImportActualZipEntrySizeLimit(t *testing.T) {
	t.Run("宣告大小較小但實際解壓超限", func(t *testing.T) {
		zipPath := writeZipWithSmallDeclaredSize(t, t.TempDir(), "large.step", []byte(strings.Repeat("x", 128)))
		out := t.TempDir()
		result := importWithLimits(library.Options{Out: out, Name: "demo"}, []string{zipPath}, limits{file: 32, bundle: 64, items: 10})[0]
		if result.Error != "單一檔案超過大小上限" {
			t.Fatalf("error=%q", result.Error)
		}
		if len(snapshot(t, out)) != 0 {
			t.Fatalf("size failure wrote files: %#v", snapshot(t, out))
		}
	})
}

func TestImportEmptyAndLegacyBundles(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "只有舊版 lib 與 mod", files: map[string]string{"old.lib": "legacy", "old.mod": "legacy"}, want: "只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式"},
		{name: "空包", files: map[string]string{}, want: "找不到可匯入的 KiCad 檔案"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := t.TempDir()
			result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), tt.files, false)})[0]
			if result.Error != tt.want {
				t.Fatalf("error=%q, want %q", result.Error, tt.want)
			}
			if len(snapshot(t, out)) != 0 {
				t.Fatalf("error bundle wrote files: %#v", snapshot(t, out))
			}
		})
	}
}

func TestImportExistingAndOverwriteArtifacts(t *testing.T) {
	t.Run("不覆寫時三類 exists 且原 bytes 不變沒有 bak", func(t *testing.T) {
		out := t.TempDir()
		files := simpleBundle("Chip", "Board")
		zipPath := writeZip(t, t.TempDir(), files, false)
		if got := Import(library.Options{Out: out, Name: "demo"}, []string{zipPath})[0]; got.Error != "" {
			t.Fatal(got.Error)
		}
		paths := artifactPaths(out)
		before := setArtifactBytes(t, paths, []byte(`(kicad_symbol_lib (version 20231120) (symbol "Chip"))`), []byte("old footprint"), []byte("old model"))
		result := Import(library.Options{Out: out, Name: "demo"}, []string{zipPath})[0]
		if !allStatus(result, "exists") {
			t.Fatalf("result=%+v", result)
		}
		for i, path := range paths {
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, before[i]) {
				t.Fatalf("artifact changed: %s", path)
			}
		}
		for _, path := range paths {
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Fatalf("unexpected backup for %s: %v", path, err)
			}
		}
	})
	t.Run("覆寫回報 updated 並備份原符號庫", func(t *testing.T) {
		out := t.TempDir()
		files := simpleBundle("Chip", "Board")
		zipPath := writeZip(t, t.TempDir(), files, false)
		paths := artifactPaths(out)
		before := setArtifactBytes(t, paths, []byte(`(kicad_symbol_lib (version 20231120) (symbol "Chip"))`), []byte("old footprint"), []byte("old model"))
		result := Import(library.Options{Out: out, Name: "demo", Overwrite: true}, []string{zipPath})[0]
		if !allStatus(result, "updated") {
			t.Fatalf("result=%+v", result)
		}
		backup, err := os.ReadFile(paths[0] + ".bak")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(backup, before[0]) {
			t.Fatalf("backup=%q want=%q", backup, before[0])
		}
		for _, path := range paths[1:] {
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Fatalf("unexpected add-semantics backup for %s: %v", path, err)
			}
		}
	})
}

func TestImportAppendsSymbolsInBundleOrder(t *testing.T) {
	t.Run("既有符號原樣保留且新符號依包內路徑順序附加", func(t *testing.T) {
		out := t.TempDir()
		old := []byte("(kicad_symbol_lib (version 20231120)\n(symbol \"Existing\" (property \"Value\" \"old\"))\n)\n")
		writeFileForTest(t, filepath.Join(out, "demo.kicad_sym"), string(old))
		files := map[string]string{"a/A.kicad_sym": symbolFile("First", "", "20231120"), "z/Z.kicad_sym": symbolFile("Second", "", "20231120")}
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
		if result.Error != "" {
			t.Fatalf("error=%s", result.Error)
		}
		data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(`(symbol "Existing" (property "Value" "old"))`)) {
			t.Fatal("existing symbol bytes were not preserved")
		}
		first, second := bytes.Index(data, []byte(`(symbol "First"`)), bytes.Index(data, []byte(`(symbol "Second"`))
		if first < 0 || second < 0 || first > second {
			t.Fatalf("new symbol order incorrect: %s", data)
		}
	})
}

func TestImportSymbolVersionAndLibraryWrite(t *testing.T) {
	t.Run("匯入高版本警告且 library.Write 後仍保留", func(t *testing.T) {
		out := t.TempDir()
		writeFileForTest(t, filepath.Join(out, "demo.kicad_sym"), `(kicad_symbol_lib (version 20231120) (symbol "Old"))`)
		files := map[string]string{"new.kicad_sym": symbolFile("New", "", "20251024")}
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
		data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil {
			t.Fatal(err)
		}
		if kicad.LibraryVersion(data) != 20251024 {
			t.Fatalf("version=%d", kicad.LibraryVersion(data))
		}
		if !containsString(result.Warnings, "符號格式版本 20251024 較新，KiCad 8／9 可能無法開啟此元件庫") {
			t.Fatalf("warnings=%v", result.Warnings)
		}
		component := &model.Component{Value: "Added By Add"}
		if _, err := library.Write(library.Options{Out: out, Name: "demo"}, component, nil); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil {
			t.Fatal(err)
		}
		if kicad.LibraryVersion(after) != 20251024 {
			t.Fatalf("version after Write=%d", kicad.LibraryVersion(after))
		}
	})
}

func TestImportDerivedSymbolDependencies(t *testing.T) {
	t.Run("父子同包且包內順序保留", func(t *testing.T) {
		out := t.TempDir()
		content := `(kicad_symbol_lib (version 20231120) (symbol "Parent") (symbol "Child" (extends "Parent")))`
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), map[string]string{"lib.kicad_sym": content}, false)})[0]
		if !reflect.DeepEqual(result.Symbols, []Item{{Name: "Parent", Status: "added"}, {Name: "Child", Status: "added"}}) {
			t.Fatalf("symbols=%+v", result.Symbols)
		}
		data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Index(data, []byte(`(symbol "Parent"`)) > bytes.Index(data, []byte(`(symbol "Child"`)) {
			t.Fatalf("parent/child order=%s", data)
		}
	})
	t.Run("父符號不存在時該項 failed 其餘照常寫入", func(t *testing.T) {
		out := t.TempDir()
		content := `(kicad_symbol_lib (version 20231120) (symbol "Orphan" (extends "Missing")) (symbol "Okay"))`
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), map[string]string{"lib.kicad_sym": content}, false)})[0]
		if !reflect.DeepEqual(result.Symbols, []Item{{Name: "Orphan", Status: "failed"}, {Name: "Okay", Status: "added"}}) {
			t.Fatalf("symbols=%+v", result.Symbols)
		}
		if !containsString(result.Warnings, "符號 Orphan 的父符號 Missing 不存在") {
			t.Fatalf("warnings=%v", result.Warnings)
		}
		data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(`(symbol "Orphan"`)) || !bytes.Contains(data, []byte(`(symbol "Okay"`)) {
			t.Fatalf("output symbols=%s", data)
		}
	})
}

func TestImportFootprintPropertyCases(t *testing.T) {
	tests := []struct {
		name, symbol, fp, want string
		warning                string
	}{
		{name: "Footprint ref 命中原檔名", symbol: `(symbol "Chip" (property "Footprint" "pkg" (at 0 0 0)))`, fp: "pkg", want: `(property "Footprint" "demo:pkg"`},
		{name: "Footprint 空值且僅一個封裝", symbol: `(symbol "Chip" (property "Footprint" "" (at 0 0 0)))`, fp: "pkg", want: `(property "Footprint" "demo:pkg"`},
		{name: "非空 Footprint 對不到保留原值", symbol: `(symbol "Chip" (property "Footprint" "missing" (at 0 0 0)))`, fp: "pkg", want: `(property "Footprint" "missing"`, warning: "符號 Chip 的封裝 missing 不在匯入內容中，保留原值"},
		{name: "沒有 Footprint 屬性時不新增", symbol: `(symbol "Chip" (property "Value" "Chip" (at 0 0 0)))`, fp: "pkg", want: `(property "Value" "Chip"`, warning: "符號 Chip 沒有 Footprint 屬性，未自動指定封裝"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := map[string]string{"lib.kicad_sym": `(kicad_symbol_lib (version 20231120) ` + tt.symbol + `)`, tt.fp + ".kicad_mod": `(footprint "` + tt.fp + `" (property "Value" "keep"))`}
			out := t.TempDir()
			result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte(tt.want)) {
				t.Fatalf("symbol bytes lack %q: %s", tt.want, data)
			}
			if strings.Contains(tt.symbol, `(property "Footprint"`) != strings.Contains(string(data), `(property "Footprint"`) {
				t.Fatalf("Footprint property presence changed: %s", data)
			}
			if tt.warning != "" && !containsString(result.Warnings, tt.warning) {
				t.Fatalf("warnings=%v", result.Warnings)
			}
			if tt.warning == "" && len(result.Warnings) > 0 {
				t.Fatalf("unexpected warnings=%v", result.Warnings)
			}
		})
	}
}

func TestImportFootprintModelCases(t *testing.T) {
	tests := []struct {
		name, footprint, extra string
		warning                string
		model                  string
	}{
		{name: "既有 model 路徑命中並改寫", footprint: `(footprint "Board" (model "old\\Board.step"))`, extra: "Board.step", model: "Board.step"},
		{name: "model 路徑對不到時保留並警告", footprint: `(footprint "Board" (model "missing.step"))`, extra: "Board.step", warning: "封裝 Board 的 3D 模型 missing.step 不在匯入內容中", model: "missing.step"},
		{name: "沒有 model 區塊時自動插入", footprint: `(footprint "Board" (property "Value" "Board"))`, extra: "Board.step", warning: "已自動掛上 3D 模型 Board.step，位置未經校正，請在 KiCad 檢查", model: "Board.step"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := t.TempDir()
			files := map[string]string{"lib.kicad_sym": symbolFile("Chip", "Board", "20231120"), "Board.kicad_mod": tt.footprint, tt.extra: "model"}
			result := Import(library.Options{Out: out, Name: "demo", ModelBase: "/models"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
			if result.Error != "" {
				t.Fatal(result.Error)
			}
			if tt.warning != "" && !containsString(result.Warnings, tt.warning) {
				t.Fatalf("warnings=%v", result.Warnings)
			}
			data, err := os.ReadFile(filepath.Join(out, "demo.pretty", "Board.kicad_mod"))
			if err != nil {
				t.Fatal(err)
			}
			want := "/models/demo.3dshapes/" + tt.model
			if tt.warning == "封裝 Board 的 3D 模型 missing.step 不在匯入內容中" {
				want = tt.model
			}
			if !bytes.Contains(data, []byte(kicad.Quote(want))) {
				t.Fatalf("model path output=%s", data)
			}
		})
	}
}

func TestImportDestinationCollisions(t *testing.T) {
	t.Run("同名目的地只採排序第一個並警告", func(t *testing.T) {
		out := t.TempDir()
		files := map[string]string{"a/Board.kicad_mod": `(footprint "First")`, "z/Board.kicad_mod": `(footprint "Second")`}
		result := Import(library.Options{Out: out, Name: "demo"}, []string{writeZip(t, t.TempDir(), files, false)})[0]
		if !reflect.DeepEqual(result.Footprints, []Item{{Name: "Board", Status: "added"}}) {
			t.Fatalf("footprints=%+v", result.Footprints)
		}
		if !containsString(result.Warnings, "封裝目的地檔名重複，略過 z/Board.kicad_mod") {
			t.Fatalf("warnings=%v", result.Warnings)
		}
		data, err := os.ReadFile(filepath.Join(out, "demo.pretty", "Board.kicad_mod"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("First")) {
			t.Fatalf("wrong collision winner: %s", data)
		}
	})
}

func TestImportBundlesDoNotCrossResolve(t *testing.T) {
	t.Run("兩個輸入路徑各自解析對應", func(t *testing.T) {
		out := t.TempDir()
		first := writeZip(t, t.TempDir(), map[string]string{"A.kicad_sym": symbolFile("A", "B", "20231120")}, false)
		second := writeZip(t, t.TempDir(), map[string]string{"B.kicad_mod": `(footprint "B")`}, false)
		results := Import(library.Options{Out: out, Name: "demo"}, []string{first, second})
		if len(results) != 2 {
			t.Fatalf("results=%d", len(results))
		}
		if results[0].Source != first || results[1].Source != second {
			t.Fatalf("sources=%v", []string{results[0].Source, results[1].Source})
		}
		if !containsString(results[0].Warnings, "符號 A 的封裝 B 不在匯入內容中，保留原值") {
			t.Fatalf("first warnings=%v", results[0].Warnings)
		}
		if len(results[1].Warnings) != 0 {
			t.Fatalf("second warnings=%v", results[1].Warnings)
		}
	})
}

func TestImportResultSlicesMarshalAsArrays(t *testing.T) {
	t.Run("空結果 slices JSON 為陣列", func(t *testing.T) {
		encoded, err := json.Marshal(emptyResult("input"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `null`) {
			t.Fatalf("JSON contains null: %s", encoded)
		}
	})
}

func simpleBundle(symbol, footprint string) map[string]string {
	return map[string]string{"lib.kicad_sym": symbolFile(symbol, footprint, "20231120"), footprint + ".kicad_mod": footprintFile(footprint, "models/"+footprint+".step"), footprint + ".step": "3D bytes"}
}
func symbolFile(name, footprint, version string) string {
	prop := ""
	if footprint != "" {
		prop = `(property "Footprint" "` + footprint + `" (at 0 0 0))`
	}
	return `(kicad_symbol_lib (version ` + version + `) (symbol "` + name + `" ` + prop + `))`
}
func footprintFile(name, path string) string {
	return `(footprint "` + name + `" (property "Value" "` + name + `") (model "` + path + `" (offset (xyz 0 0 0))))`
}

func writeZip(t *testing.T, root string, files map[string]string, shrinkSize bool) string {
	t.Helper()
	path := filepath.Join(root, "input.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if shrinkSize {
		shrinkZipUncompressedSize(t, path)
	}
	return path
}

func shrinkZipUncompressedSize(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	central := bytes.Index(data, []byte("PK\x01\x02"))
	if central < 0 {
		t.Fatal("zip headers not found")
	}
	binary.LittleEndian.PutUint32(data[central+24:central+28], 1)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func writeZipWithSmallDeclaredSize(t *testing.T, root, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(root, "input.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	header := &zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(content), CompressedSize64: uint64(len(content)), UncompressedSize64: 1}
	entry, err := writer.CreateRaw(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFileForTest(t, filepath.Join(root, filepath.FromSlash(name)), content)
	}
}

func writeFileForTest(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func artifactPaths(out string) []string {
	return []string{filepath.Join(out, "demo.kicad_sym"), filepath.Join(out, "demo.pretty", "Board.kicad_mod"), filepath.Join(out, "demo.3dshapes", "Board.step")}
}
func setArtifactBytes(t *testing.T, paths []string, symbol, footprint, model []byte) [][]byte {
	t.Helper()
	inputs := [][]byte{symbol, footprint, model}
	for i, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, inputs[i], 0644); err != nil {
			t.Fatal(err)
		}
	}
	return inputs
}
func allStatus(result Result, status string) bool {
	for _, items := range [][]Item{result.Symbols, result.Footprints, result.Models} {
		for _, item := range items {
			if item.Status != status {
				return false
			}
		}
	}
	return true
}
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestImportWarnsWhenExistingFileDiffers(t *testing.T) {
	out := t.TempDir()
	first := filepath.Join(t.TempDir(), "Part.kicad_mod")
	second := filepath.Join(t.TempDir(), "Part.kicad_mod")
	if err := os.WriteFile(first, []byte(`(footprint "Part" (layer "F.Cu"))`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`(footprint "Part" (layer "B.Cu"))`), 0644); err != nil {
		t.Fatal(err)
	}
	opts := library.Options{Out: out, Name: "demo"}
	t.Run("同內容再匯入不警告", func(t *testing.T) {
		Import(opts, []string{first})
		got := Import(opts, []string{first})[0]
		if got.Footprints[0].Status != "exists" || len(got.Warnings) != 0 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("內容不同且未覆寫時警告並保留原檔", func(t *testing.T) {
		got := Import(opts, []string{second})[0]
		want := "封裝 Part 已存在且內容不同，未覆寫；元件庫裡仍是原本那一份（要取代請開啟覆寫）"
		if got.Footprints[0].Status != "exists" || len(got.Warnings) != 1 || got.Warnings[0] != want {
			t.Fatalf("result=%+v", got)
		}
		kept, err := os.ReadFile(filepath.Join(out, "demo.pretty", "Part.kicad_mod"))
		if err != nil || string(kept) != `(footprint "Part" (layer "F.Cu"))` {
			t.Fatalf("kept=%q err=%v", kept, err)
		}
	})
}
