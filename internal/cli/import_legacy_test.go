package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicadcli"
)

type importFakeKiCadCLI struct {
	path string
	err  error
}

func (f importFakeKiCadCLI) Find(string) (string, error) { return f.path, f.err }
func (f importFakeKiCadCLI) SymbolSVGs(context.Context, string, string) ([]kicadcli.NamedSVG, error) {
	return []kicadcli.NamedSVG{}, nil
}
func (f importFakeKiCadCLI) FootprintSVGs(context.Context, string, string) ([]kicadcli.NamedSVG, error) {
	return []kicadcli.NamedSVG{}, nil
}

func TestImportLegacyConverterWiringCases(t *testing.T) {
	t.Run("found kicad-cli converter is passed to importer", testCLIImportPassesLegacyConverter)
	t.Run("missing kicad-cli passes nil converter", testCLIImportPassesNilLegacyConverter)
}

func testCLIImportPassesLegacyConverter(t *testing.T) {
	input := writeImportZip(t, map[string]string{"Old.lib": "legacy"})
	var stdout, stderr bytes.Buffer
	called := false
	code := RunWithOptions(context.Background(), []string{"import", "--out", t.TempDir(), "--lib", "demo", input}, nil, Options{KiCadCLI: importFakeKiCadCLI{path: "fake-cli"}, UpgradeSymbols: func(_ context.Context, cli string, lib, dcm []byte) ([]byte, error) {
		called = true
		if cli != "fake-cli" || string(lib) != "legacy" || dcm != nil {
			t.Fatalf("converter arguments cli=%q lib=%q dcm=%q", cli, lib, dcm)
		}
		return []byte(`(kicad_symbol_lib (version 20231120) (symbol "Legacy"))`), nil
	}}, &stdout, &stderr)
	if code != 0 || !called || !bytes.Contains(stdout.Bytes(), []byte("symbol Legacy: added")) || stderr.Len() != 0 {
		t.Fatalf("exit=%d called=%t stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func testCLIImportPassesNilLegacyConverter(t *testing.T) {
	input := writeImportZip(t, map[string]string{"Old.lib": "legacy", "Board.kicad_mod": `(footprint "Board")`})
	var stdout, stderr bytes.Buffer
	code := RunWithOptions(context.Background(), []string{"import", "--out", t.TempDir(), "--lib", "demo", input}, nil, Options{KiCadCLI: importFakeKiCadCLI{err: kicadcli.ErrNotFound}}, &stdout, &stderr)
	if code != 0 || !bytes.Contains(stdout.Bytes(), []byte("發現舊版符號庫 Old.lib，但找不到 kicad-cli，無法自動轉換")) || !bytes.Contains(stdout.Bytes(), []byte("footprint Board: added")) || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
