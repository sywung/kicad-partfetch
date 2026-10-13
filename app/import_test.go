package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestImportRejectsEmptyInput(t *testing.T) {
	t.Run("空輸入回傳指定錯誤", func(t *testing.T) {
		app := NewWithOptions(Options{})
		results, err := app.Import(nil)
		if err == nil || err.Error() != "請先選擇要匯入的檔案" {
			t.Fatalf("Import error=%v", err)
		}
		if results == nil || len(results) != 0 {
			t.Fatalf("results=%#v, want non-nil empty slice", results)
		}
	})
}

func TestImportResultSlicesSerializeWithoutNull(t *testing.T) {
	t.Run("結果的所有 slice 經 JSON 序列化不是 null", func(t *testing.T) {
		out := t.TempDir()
		app := configuredImporter(t, out, false)
		results, err := app.Import([]string{writeAppImportZip(t, map[string]string{"Board.kicad_mod": `(footprint "Board")`})})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 {
			t.Fatalf("results=%d", len(results))
		}
		if results[0].Symbols == nil || results[0].Footprints == nil || results[0].Models == nil || results[0].Warnings == nil {
			t.Fatalf("nil result slice: %+v", results[0])
		}
		encoded, err := json.Marshal(results)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encoded, []byte("null")) {
			t.Fatalf("JSON contains null: %s", encoded)
		}
	})
}

func TestImportPickerCancellationReturnsEmptyValues(t *testing.T) {
	t.Run("檔案對話框取消回空 slice", func(t *testing.T) {
		app := NewWithOptions(Options{PickImportFiles: func(context.Context) ([]string, error) { return nil, nil }})
		paths, err := app.PickImportFiles()
		if err != nil || paths == nil || len(paths) != 0 {
			t.Fatalf("paths=%#v err=%v", paths, err)
		}
	})
	t.Run("資料夾對話框取消回空字串", func(t *testing.T) {
		app := NewWithOptions(Options{PickImportDirectory: func(context.Context) (string, error) { return "", nil }})
		path, err := app.PickImportDirectory()
		if err != nil || path != "" {
			t.Fatalf("path=%q err=%v", path, err)
		}
	})
}

func TestImportUsesSavedOverwriteAndModelPathSettings(t *testing.T) {
	t.Run("Overwrite 傳入 importer 且 With3D 不抑制本機匯入", func(t *testing.T) {
		out := t.TempDir()
		app := configuredImporter(t, out, true)
		writeAppImportFile(t, filepath.Join(out, "demo.kicad_sym"), `(kicad_symbol_lib (version 20231120) (symbol "Chip"))`)
		writeAppImportFile(t, filepath.Join(out, "demo.pretty", "Board.kicad_mod"), "old footprint")
		writeAppImportFile(t, filepath.Join(out, "demo.3dshapes", "Board.step"), "old model")
		input := writeAppImportZip(t, map[string]string{"lib.kicad_sym": `(kicad_symbol_lib (version 20231120) (symbol "Chip" (property "Footprint" "Board")))`, "Board.kicad_mod": `(footprint "Board" (model "Board.step"))`, "Board.step": "new model"})
		results, err := app.Import([]string{input})
		if err != nil {
			t.Fatal(err)
		}
		result := results[0]
		if !reflect.DeepEqual(result.Symbols, []ImportItem{{Name: "Chip", Status: "updated"}}) || !reflect.DeepEqual(result.Footprints, []ImportItem{{Name: "Board", Status: "updated"}}) || !reflect.DeepEqual(result.Models, []ImportItem{{Name: "Board.step", Status: "updated"}}) {
			t.Fatalf("result=%+v", result)
		}
		data, err := os.ReadFile(filepath.Join(out, "demo.pretty", "Board.kicad_mod"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `${KIPRJMOD}/shared/demo.3dshapes/Board.step`) {
			t.Fatalf("model base was not forwarded: %s", data)
		}
	})
}

func configuredImporter(t *testing.T, out string, overwrite bool) *App {
	t.Helper()
	app := NewWithOptions(Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	if err := app.SaveSettings(Settings{OutDir: out, LibName: "demo", ModelBase: "${KIPRJMOD}/shared", With3D: false, Overwrite: overwrite}); err != nil {
		t.Fatal(err)
	}
	return app
}

func writeAppImportZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "import.zip")
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
	return path
}

func writeAppImportFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
