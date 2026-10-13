package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestImportCommandTextOutput(t *testing.T) {
	t.Run("文字輸出列出三類項目且成功回傳零", func(t *testing.T) {
		out := t.TempDir()
		input := writeImportZip(t, map[string]string{"lib.kicad_sym": `(kicad_symbol_lib (version 20231120) (symbol "Chip" (property "Footprint" "Board")))`, "Board.kicad_mod": `(footprint "Board" (model "Board.step"))`, "Board.step": "step"})
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"import", "--out", out, "--lib", "demo", "--model-base", "/models", input}, nil, &stdout, &stderr)
		want := input + ":\n  symbol Chip: added\n  footprint Board: added\n  model Board.step: added\n"
		if code != 0 || stdout.String() != want || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func TestImportCommandJSONOutput(t *testing.T) {
	t.Run("JSON 每包一行且 item 陣列為空陣列或具名物件", func(t *testing.T) {
		out := t.TempDir()
		input := writeImportZip(t, map[string]string{"Board.kicad_mod": `(footprint "Board")`})
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"import", "--out", out, "--lib", "demo", "--json", input}, nil, &stdout, &stderr)
		var got map[string]json.RawMessage
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
			t.Fatal(err)
		}
		if code != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stderr=%q", code, stderr.String())
		}
		if string(got["source"]) != `"`+input+`"` {
			t.Fatalf("source=%s", got["source"])
		}
		if string(got["symbols"]) != `[]` || string(got["models"]) != `[]` {
			t.Fatalf("empty arrays: %s", stdout.String())
		}
		if string(got["footprints"]) != `[{"name":"Board","status":"added"}]` {
			t.Fatalf("footprints=%s", got["footprints"])
		}
		if _, ok := got["warnings"]; ok {
			t.Fatalf("unexpected warnings field: %s", stdout.String())
		}
		if _, ok := got["error"]; ok {
			t.Fatalf("unexpected error field: %s", stdout.String())
		}
	})
}

func TestImportCommandExitCodes(t *testing.T) {
	t.Run("包錯誤回傳一", func(t *testing.T) {
		input := writeImportZip(t, map[string]string{})
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"import", "--out", t.TempDir(), "--lib", "demo", input}, nil, &stdout, &stderr)
		if code != 1 || stdout.String() != input+":\n  error: 找不到可匯入的 KiCad 檔案\n" || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("failed 項目回傳一", func(t *testing.T) {
		input := writeImportZip(t, map[string]string{"broken.kicad_mod": "not a footprint"})
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"import", "--out", t.TempDir(), "--lib", "demo", input}, nil, &stdout, &stderr)
		if code != 1 || !bytes.Contains(stdout.Bytes(), []byte("footprint broken: failed")) || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("沒有 PATH 時印指定錯誤並回傳二", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"import", "--out", t.TempDir(), "--lib", "demo"}, nil, &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || stderr.String() != "import requires one or more files or directories\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func writeImportZip(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "import.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(data)); err != nil {
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
