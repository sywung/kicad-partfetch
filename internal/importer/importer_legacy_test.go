package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/library"
)

func TestLegacyConversionCases(t *testing.T) {
	t.Run("converter output is imported and empty footprint pairs by symbol name", testLegacyConversionAndNamePairing)
	t.Run("missing converter warns while importing other files", testLegacyMissingConverterKeepsOtherFiles)
	t.Run("conversion error warns while importing other files", testLegacyConversionFailureKeepsOtherFiles)
	t.Run("same name modern symbol file skips conversion without warning", testLegacyModernSymbolWins)
	t.Run("legacy library without converter returns old format error", testLegacyOnlyWithoutConverter)
	t.Run("legacy module without converter returns old format error", testLegacyOnlyModule)
	t.Run("matching DCM data is passed to converter", testLegacyConverterReceivesDCM)
}

func testLegacyConversionAndNamePairing(t *testing.T) {
	converted := []byte(`(kicad_symbol_lib (version 20231120) (symbol "Chip" (property "Footprint" "" (at 0 0 0))))`)
	input := writeZip(t, t.TempDir(), map[string]string{"Part.lib": "legacy", "Chip.kicad_mod": `(footprint "Chip")`, "Other.kicad_mod": `(footprint "Other")`}, false)
	out := t.TempDir()
	result := ImportWith(library.Options{Out: out, Name: "demo"}, []string{input}, func(lib, dcm []byte) ([]byte, error) {
		if string(lib) != "legacy" || dcm != nil {
			t.Fatalf("converter input lib=%q dcm=%q", lib, dcm)
		}
		return converted, nil
	})[0]
	if result.Error != "" || len(result.Symbols) != 1 || result.Symbols[0] != (Item{Name: "Chip", Status: "added"}) {
		t.Fatalf("result=%+v", result)
	}
	wantWarnings := []string{"已用 kicad-cli 將舊版符號庫 Part.lib 轉為新格式", "符號 Chip 未指定封裝，已依名稱配對到 Chip，請確認"}
	if strings.Join(result.Warnings, "\n") != strings.Join(wantWarnings, "\n") {
		t.Fatalf("warnings=%q", result.Warnings)
	}
	want := "(kicad_symbol_lib (version 20231120) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n(symbol \"Chip\" (property \"Footprint\" \"demo:Chip\" (at 0 0 0)))\n)\n"
	got, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
	if err != nil || string(got) != want {
		t.Fatalf("symbol library=%q err=%v", got, err)
	}
}

func testLegacyMissingConverterKeepsOtherFiles(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"Old.lib": "legacy", "Board.kicad_mod": `(footprint "Board")`}, false)
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, nil)[0]
	if result.Error != "" || len(result.Footprints) != 1 || result.Footprints[0].Status != "added" {
		t.Fatalf("result=%+v", result)
	}
	want := []string{"發現舊版符號庫 Old.lib，但找不到 kicad-cli，無法自動轉換", "忽略舊版 KiCad 格式檔案"}
	if strings.Join(result.Warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings=%q", result.Warnings)
	}
}

func testLegacyConversionFailureKeepsOtherFiles(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"Old.lib": "legacy", "Board.kicad_mod": `(footprint "Board")`}, false)
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, func([]byte, []byte) ([]byte, error) {
		return nil, os.ErrInvalid
	})[0]
	if result.Error != "" || len(result.Footprints) != 1 || result.Footprints[0].Status != "added" {
		t.Fatalf("result=%+v", result)
	}
	want := []string{"舊版符號庫 Old.lib 轉換失敗：invalid argument", "忽略舊版 KiCad 格式檔案"}
	if strings.Join(result.Warnings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("warnings=%q", result.Warnings)
	}
}

func testLegacyModernSymbolWins(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"parts/Board.lib": "legacy", "parts/BOARD.KICAD_SYM": symbolFile("Board", "", "20231120")}, false)
	called := false
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, func([]byte, []byte) ([]byte, error) {
		called = true
		return nil, os.ErrInvalid
	})[0]
	if called || result.Error != "" || len(result.Symbols) != 1 || result.Symbols[0].Name != "Board" || len(result.Warnings) != 0 {
		t.Fatalf("called=%t result=%+v", called, result)
	}
}

func testLegacyOnlyWithoutConverter(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"Old.lib": "legacy"}, false)
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, nil)[0]
	if result.Error != "只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式" {
		t.Fatalf("error=%q", result.Error)
	}
	if len(result.Warnings) == 0 || result.Warnings[0] != "發現舊版符號庫 Old.lib，但找不到 kicad-cli，無法自動轉換" {
		t.Fatalf("warnings=%q", result.Warnings)
	}
}

func testLegacyOnlyModule(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"Old.mod": "legacy"}, false)
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, nil)[0]
	if result.Error != "只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式" || len(result.Warnings) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func testLegacyConverterReceivesDCM(t *testing.T) {
	input := writeZip(t, t.TempDir(), map[string]string{"parts/Old.lib": "library bytes", "parts/oLD.DCM": "documentation bytes"}, false)
	called := false
	result := ImportWith(library.Options{Out: t.TempDir(), Name: "demo"}, []string{input}, func(lib, dcm []byte) ([]byte, error) {
		called = true
		if string(lib) != "library bytes" || string(dcm) != "documentation bytes" {
			t.Fatalf("converter input lib=%q dcm=%q", lib, dcm)
		}
		return []byte(`(kicad_symbol_lib (version 20231120) (symbol "Old"))`), nil
	})[0]
	if !called || result.Error != "" || len(result.Symbols) != 1 || result.Symbols[0].Name != "Old" {
		t.Fatalf("called=%t result=%+v", called, result)
	}
}

func TestEmptyFootprintNamePairingCases(t *testing.T) {
	t.Run("case-sensitive match wins", func(t *testing.T) {
		testEmptyFootprintPairing(t, "CHIP", []string{"CHIP", "chip"}, `"demo:CHIP"`, "符號 CHIP 未指定封裝，已依名稱配對到 CHIP，請確認")
	})
	t.Run("single case-insensitive match is selected", func(t *testing.T) {
		testEmptyFootprintPairing(t, "chip", []string{"CHIP", "Other"}, `"demo:CHIP"`, "符號 chip 未指定封裝，已依名稱配對到 CHIP，請確認")
	})
	t.Run("multiple case-insensitive matches are not selected", func(t *testing.T) {
		testEmptyFootprintPairing(t, "chip", []string{"CHIP", "Chip"}, `(property "Footprint" ""`, "")
	})
	t.Run("non-empty footprint value does not use name pairing", func(t *testing.T) {
		input := writeZip(t, t.TempDir(), map[string]string{"lib.kicad_sym": symbolFile("Chip", "missing", "20231120"), "Chip.kicad_mod": `(footprint "Chip")`, "Other.kicad_mod": `(footprint "Other")`}, false)
		out := t.TempDir()
		result := Import(library.Options{Out: out, Name: "demo"}, []string{input})[0]
		if !containsString(result.Warnings, "符號 Chip 的封裝 missing 不在匯入內容中，保留原值") {
			t.Fatalf("warnings=%q", result.Warnings)
		}
		assertImportedSymbolContains(t, out, result.Symbols, `(property "Footprint" "missing"`)
	})
}

func testEmptyFootprintPairing(t *testing.T, symbol string, footprintNames []string, want, warning string) {
	files := map[string]string{"lib.kicad_sym": `(kicad_symbol_lib (version 20231120) (symbol "` + symbol + `" (property "Footprint" "" (at 0 0 0))))`}
	for _, name := range footprintNames {
		files[name+".kicad_mod"] = `(footprint "` + name + `")`
	}
	out := t.TempDir()
	input := writeZip(t, t.TempDir(), files, false)
	result := Import(library.Options{Out: out, Name: "demo"}, []string{input})[0]
	data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
	if err != nil || !strings.Contains(string(data), want) {
		t.Fatalf("symbol library=%q err=%v", data, err)
	}
	if warning != "" && !containsString(result.Warnings, warning) {
		t.Fatalf("warnings=%q", result.Warnings)
	}
	if warning == "" && strings.Contains(string(data), "demo:") {
		t.Fatalf("ambiguous footprint unexpectedly paired: %s", data)
	}
}

func assertImportedSymbolContains(t *testing.T, out string, symbols []Item, want string) {
	t.Helper()
	if len(symbols) != 1 {
		t.Fatalf("symbols=%+v", symbols)
	}
	data, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
	if err == nil && strings.Contains(string(data), want) {
		return
	}
	t.Fatalf("symbol bytes=%q err=%v", data, err)
}
