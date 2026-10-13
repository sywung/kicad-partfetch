package app

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

func TestImportLegacyConverterWiringCases(t *testing.T) {
	t.Run("found kicad-cli converter is passed to importer", testAppImportPassesLegacyConverter)
	t.Run("missing kicad-cli passes nil converter", testAppImportPassesNilLegacyConverter)
}

func TestGitHubLegacyPreviewConversion(t *testing.T) {
	t.Run("legacy symbol library is converted before SVG preview", func(t *testing.T) {
		contents := gh.Contents{Repo: gh.Repo{FullName: "owner/repo", URL: "https://github.com/owner/repo"}, Legacy: 2, LegacyFiles: []gh.File{{Path: "legacy/Old.dcm", Size: 4}, {Path: "legacy/Old.lib", Size: 7}}}
		github := &appFakeGitHub{contents: contents, files: map[string]string{"legacy/Old.lib": "legacy", "legacy/Old.dcm": "docs"}}
		cli := &appFakeKiCadPreview{symbols: []kicadcli.NamedSVG{{Name: "Old.svg", SVG: []byte("<svg/>")}}}
		called := false
		a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, UpgradeSymbols: func(_ context.Context, executable string, lib, dcm []byte) ([]byte, error) {
			called = true
			if executable != "fake-cli" || string(lib) != "legacy" || string(dcm) != "docs" {
				t.Fatalf("upgrade arguments cli=%q lib=%q dcm=%q", executable, lib, dcm)
			}
			return []byte(`(kicad_symbol_lib (version 20231120) (symbol "Old"))`), nil
		}})
		got, err := a.Preview("github:owner/repo")
		wantFiles := []string{"legacy/Old.dcm", "legacy/Old.lib"}
		if err != nil || !called || !reflect.DeepEqual(got.Files, wantFiles) || !reflect.DeepEqual(got.SymbolSVGs, []string{"<svg/>"}) || len(cli.symbolPaths) != 1 || !strings.HasSuffix(cli.symbolPaths[0], "legacy/Old.kicad_sym") {
			t.Fatalf("preview=%+v paths=%q called=%t err=%v", got, cli.symbolPaths, called, err)
		}
	})
}

func testAppImportPassesLegacyConverter(t *testing.T) {
	a := configuredImporter(t, t.TempDir(), false)
	cli := &appFakeKiCadPreview{}
	a.kicadCLI = cli
	called := false
	a.upgradeSymbols = func(_ context.Context, executable string, lib, dcm []byte) ([]byte, error) {
		called = true
		if executable != "fake-cli" || string(lib) != "legacy" || dcm != nil {
			t.Fatalf("converter arguments cli=%q lib=%q dcm=%q", executable, lib, dcm)
		}
		return []byte(`(kicad_symbol_lib (version 20231120) (symbol "Legacy"))`), nil
	}
	results, err := a.Import([]string{writeAppImportZip(t, map[string]string{"Old.lib": "legacy"})})
	if err != nil || !called || len(results) != 1 || len(results[0].Symbols) != 1 || results[0].Symbols[0] != (ImportItem{Name: "Legacy", Status: "added"}) {
		t.Fatalf("results=%+v called=%t err=%v", results, called, err)
	}
}

func testAppImportPassesNilLegacyConverter(t *testing.T) {
	a := configuredImporter(t, t.TempDir(), false)
	a.kicadCLI = &appFakeKiCadPreview{findErr: kicadcli.ErrNotFound}
	results, err := a.Import([]string{writeAppImportZip(t, map[string]string{"Old.lib": "legacy", "Board.kicad_mod": `(footprint "Board")`})})
	if err != nil || len(results) != 1 || results[0].Error != "" || len(results[0].Footprints) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	want := "發現舊版符號庫 Old.lib，但找不到 kicad-cli，無法自動轉換"
	if !containsAppString(results[0].Warnings, want) || !strings.Contains(results[0].Footprints[0].Status, "added") {
		t.Fatalf("result=%+v", results[0])
	}
}

func containsAppString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
