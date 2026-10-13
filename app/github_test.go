package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/importer"
	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/settings"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type appFakeGitHub struct {
	repos         []gh.Repo
	total         int
	contents      gh.Contents
	contentsErr   error
	files         map[string]string
	downloadErr   error
	searchCalls   int
	contentCalls  int
	downloadCalls int
	downloadDir   string
}

func (f *appFakeGitHub) Search(_ context.Context, _ string, _ int) ([]gh.Repo, int, error) {
	f.searchCalls++
	return f.repos, f.total, nil
}
func (f *appFakeGitHub) Contents(_ context.Context, name string) (gh.Contents, error) {
	f.contentCalls++
	if name != f.contents.Repo.FullName {
		return gh.Contents{}, errors.New("unexpected repository")
	}
	if f.contentsErr != nil {
		return gh.Contents{}, f.contentsErr
	}
	return f.contents, nil
}
func (f *appFakeGitHub) Download(_ context.Context, _ gh.Contents, dir string) error {
	f.downloadCalls++
	f.downloadDir = dir
	if f.downloadErr != nil {
		return f.downloadErr
	}
	for name, value := range f.files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			return err
		}
	}
	return nil
}

type appFakeKiCadPreview struct {
	findErr     error
	symbols     []kicadcli.NamedSVG
	footprints  []kicadcli.NamedSVG
	symbolPaths []string
	foundPath   string
}

func (f *appFakeKiCadPreview) Find(configured string) (string, error) {
	f.foundPath = configured
	if f.findErr != nil {
		return "", f.findErr
	}
	return "fake-cli", nil
}
func (f *appFakeKiCadPreview) SymbolSVGs(_ context.Context, _ string, file string) ([]kicadcli.NamedSVG, error) {
	f.symbolPaths = append(f.symbolPaths, file)
	return f.symbols, nil
}
func (f *appFakeKiCadPreview) FootprintSVGs(_ context.Context, _ string, _ string) ([]kicadcli.NamedSVG, error) {
	return f.footprints, nil
}

func githubAppContents() gh.Contents {
	return gh.Contents{Repo: gh.Repo{FullName: "owner/repo", Description: "board source", License: "MIT", URL: "https://github.com/owner/repo", SHA: "commit"}, Symbols: []gh.File{{Path: "lib/board.kicad_sym", Size: 4}}, Footprints: []gh.File{{Path: "footprints/board.kicad_mod", Size: 3}}, Models: []gh.File{{Path: "3d/board.step", Size: 4}}}
}

func TestGitHubSearchAndPreviewCases(t *testing.T) {
	t.Run("GitHub search dispatch binds repository fields", testAppGitHubSearchDispatch)
	t.Run("GitHub preview downloads and exports both drawings", testAppGitHubPreviewExports)
	t.Run("GitHub preview caps symbols, SVGs and footprints", testAppGitHubPreviewCaps)
	t.Run("missing kicad-cli returns preview with warning", testAppGitHubPreviewWithoutKiCadCLI)
	t.Run("invalid PartID is rejected before GitHub calls", testAppRejectsInvalidGitHubPartID)
	t.Run("GitHub Model returns specified explanation", testAppGitHubModelExplanation)
}

func testAppGitHubPreviewCaps(t *testing.T) {
	contents := gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}}
	files := make(map[string]string)
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("symbols/%02d.kicad_sym", i)
		contents.Symbols = append(contents.Symbols, gh.File{Path: name, Size: 1})
		files[name] = "s"
	}
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("footprints/%02d.kicad_mod", i)
		contents.Footprints = append(contents.Footprints, gh.File{Path: name, Size: 1})
		files[name] = "f"
	}
	svgs := make([]kicadcli.NamedSVG, 13)
	for i := range svgs {
		svgs[i] = kicadcli.NamedSVG{Name: fmt.Sprintf("%02d.svg", i), SVG: []byte("<svg/>")}
	}
	github := &appFakeGitHub{contents: contents, files: files}
	cli := &appFakeKiCadPreview{symbols: svgs, footprints: append(svgs, svgs...)}
	a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	got, err := a.Preview("github:owner/repo")
	if err != nil || len(cli.symbolPaths) != 5 || len(got.SymbolSVGs) != 60 || len(got.FootprintSVGs) != 24 || !containsWarning(got.Warnings, "僅預覽前 5 個符號檔") || !containsWarning(got.Warnings, "僅預覽前 24 個封裝") || !containsWarning(got.Warnings, "僅預覽前 12 個 SVG") {
		t.Fatalf("preview SVGs=%d/%d warnings=%v err=%v calls=%d", len(got.SymbolSVGs), len(got.FootprintSVGs), got.Warnings, err, len(cli.symbolPaths))
	}
}

func containsWarning(warnings []string, value string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, value) {
			return true
		}
	}
	return false
}

func testAppGitHubSearchDispatch(t *testing.T) {
	github := &appFakeGitHub{repos: []gh.Repo{{FullName: "owner/repo", Description: "board", License: "MIT", URL: "https://github.com/owner/repo", Stars: 17}}, total: 1}
	a := NewWithOptions(Options{GitHub: github, Searcher: &fakeSearch{}})
	got, err := a.Search("github", "board", 2)
	want := []SearchItem{{PartID: "github:owner/repo", MPN: "owner/repo", Manufacturer: "owner", Description: "board", Source: "github", URL: "https://github.com/owner/repo", License: "MIT", Stars: 17}}
	if err != nil || !reflect.DeepEqual(got.Items, want) || got.Total != 1 || got.Page != 2 || github.searchCalls != 1 {
		t.Fatalf("result=%+v err=%v calls=%d", got, err, github.searchCalls)
	}
}

func testAppGitHubPreviewExports(t *testing.T) {
	github := &appFakeGitHub{contents: githubAppContents(), files: map[string]string{"lib/board.kicad_sym": "sym!", "footprints/board.kicad_mod": "fp!", "3d/board.step": "step"}}
	cli := &appFakeKiCadPreview{symbols: []kicadcli.NamedSVG{{Name: "unit.svg", SVG: []byte("<svg/>")}}, footprints: []kicadcli.NamedSVG{{Name: "board.svg", SVG: []byte("<svg/>")}}}
	a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	got, err := a.Preview("github:owner/repo")
	wantFiles := []string{"lib/board.kicad_sym", "footprints/board.kicad_mod", "3d/board.step"}
	if err != nil || !got.External || got.Name != "repo" || got.Package != "" || got.Manufacturer != "owner" || got.Source != "github" || got.URL != github.contents.Repo.URL || got.License != "MIT" || !got.HasModel || got.ModelTitle != "board.step" || !reflect.DeepEqual(got.Files, wantFiles) || !reflect.DeepEqual(got.SymbolSVGs, []string{"<svg/>"}) || !reflect.DeepEqual(got.FootprintSVGs, []string{"<svg/>"}) || github.downloadCalls != 1 || len(cli.symbolPaths) != 1 {
		t.Fatalf("preview=%+v err=%v downloads=%d", got, err, github.downloadCalls)
	}
	if _, err = os.Stat(github.downloadDir); !os.IsNotExist(err) {
		t.Fatalf("preview temporary directory remains: %v", err)
	}
}

func testAppGitHubPreviewWithoutKiCadCLI(t *testing.T) {
	github := &appFakeGitHub{contents: githubAppContents(), files: map[string]string{"lib/board.kicad_sym": "sym!"}}
	cli := &appFakeKiCadPreview{findErr: kicadcli.ErrNotFound}
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	if err := settings.Save(settingsPath, settings.Settings{OutDir: t.TempDir(), LibName: "parts", KicadCLI: "/configured/kicad-cli"}); err != nil {
		t.Fatal(err)
	}
	a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, SettingsPath: settingsPath})
	got, err := a.Preview("github:owner/repo")
	if err != nil || got.SymbolSVGs == nil || got.FootprintSVGs == nil || cli.foundPath != "/configured/kicad-cli" || len(got.Warnings) != 1 || got.Warnings[0] != "找不到 kicad-cli，無法產生預覽圖；仍可加入元件庫" {
		t.Fatalf("preview=%+v err=%v", got, err)
	}
	if _, err = os.Stat(github.downloadDir); !os.IsNotExist(err) {
		t.Fatalf("preview temporary directory remains: %v", err)
	}
}

func testAppRejectsInvalidGitHubPartID(t *testing.T) {
	github := &appFakeGitHub{contents: githubAppContents()}
	a := NewWithOptions(Options{GitHub: github})
	if _, err := a.Preview("github:../repo"); err == nil || err.Error() != `invalid part ID "github:../repo"` {
		t.Fatalf("Preview error=%v", err)
	}
	if _, err := a.Add([]string{"github:owner/repo", "not-a-part-id"}); err == nil || err.Error() != `invalid part ID "not-a-part-id"` {
		t.Fatalf("Add error=%v", err)
	}
	if github.contentCalls != 0 || github.downloadCalls != 0 {
		t.Fatalf("GitHub calls before validation: %+v", github)
	}
}

func testAppGitHubModelExplanation(t *testing.T) {
	a := NewWithOptions(Options{})
	_, err := a.Model("github:owner/repo")
	want := "GitHub 來源的 3D 模型無法在此預覽，請加入後在 KiCad 檢視"
	if err == nil || err.Error() != want {
		t.Fatalf("Model error=%v, want %q", err, want)
	}
}

func TestGitHubAddAggregatedStatusCases(t *testing.T) {
	t.Run("failed takes priority", func(t *testing.T) {
		assertAggregateStatus(t, []importer.Item{{Status: "added"}, {Status: "failed"}}, "failed")
	})
	t.Run("added takes priority over updated", func(t *testing.T) {
		assertAggregateStatus(t, []importer.Item{{Status: "updated"}, {Status: "added"}}, "added")
	})
	t.Run("updated takes priority over exists", func(t *testing.T) {
		assertAggregateStatus(t, []importer.Item{{Status: "exists"}, {Status: "updated"}}, "updated")
	})
	t.Run("exists is retained", func(t *testing.T) { assertAggregateStatus(t, []importer.Item{{Status: "exists"}}, "exists") })
	t.Run("empty category is skipped", func(t *testing.T) { assertAggregateStatus(t, []importer.Item{}, "skipped") })
	t.Run("Add imports GitHub bundle and reports summary", testAppGitHubAddImport)
	t.Run("Add removes temporary directory after download failure", testAppGitHubAddDownloadFailureCleansTemp)
}

func testAppGitHubAddDownloadFailureCleansTemp(t *testing.T) {
	github := &appFakeGitHub{contents: gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}}, downloadErr: errors.New("fixture download failure")}
	a := NewWithOptions(Options{GitHub: github, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	if err := a.SaveSettings(Settings{OutDir: t.TempDir(), LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	results, err := a.Add([]string{"github:owner/repo"})
	if err != nil || len(results) != 1 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	// The underlying error goes to the log; the user sees a classified message.
	if results[0].Error != "加入失敗，詳情請看紀錄檔" || results[0].ErrorKind != "unknown" {
		t.Fatalf("results=%+v", results)
	}
	if _, err = os.Stat(github.downloadDir); !os.IsNotExist(err) {
		t.Fatalf("failed Add temporary directory remains: %v", err)
	}
}

func assertAggregateStatus(t *testing.T, items []importer.Item, want string) {
	t.Helper()
	if got := aggregateImportStatus(items); got != want {
		t.Fatalf("status=%q, want %q", got, want)
	}
}

func testAppGitHubAddImport(t *testing.T) {
	contents := gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}, Symbols: []gh.File{{Path: "lib.kicad_sym", Size: 70}}}
	github := &appFakeGitHub{contents: contents, files: map[string]string{"lib.kicad_sym": `(kicad_symbol_lib (version 20231120) (symbol "Chip"))`}}
	dir := t.TempDir()
	a := NewWithOptions(Options{GitHub: github, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	if err := a.SaveSettings(Settings{OutDir: dir, LibName: "parts", ModelBase: "absolute", With3D: false}); err != nil {
		t.Fatal(err)
	}
	got, err := a.Add([]string{"github:owner/repo"})
	if err != nil || len(got) != 1 || got[0].PartID != "github:owner/repo" || got[0].Symbol != "added" || got[0].Footprint != "skipped" || got[0].Model != "skipped" || got[0].Error != "" || len(got[0].Warnings) == 0 || got[0].Warnings[0] != "符號 1 · 封裝 0 · 3D 0" {
		t.Fatalf("Add=%+v err=%v", got, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "parts.kicad_sym")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(github.downloadDir); !os.IsNotExist(err) {
		t.Fatalf("successful Add temporary directory remains: %v", err)
	}
}

func TestGitHubBindingJSONHasNoNullSlices(t *testing.T) {
	t.Run("GitHub Preview arrays serialize as non-null", func(t *testing.T) {
		github := &appFakeGitHub{contents: gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}}, files: map[string]string{}}
		cli := &appFakeKiCadPreview{findErr: kicadcli.ErrNotFound}
		a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, Searcher: search.New(search.Options{Transport: appRoundTripper(func(request *http.Request) (*http.Response, error) {
			return appResponse(request, http.StatusInternalServerError, "unused"), nil
		})})})
		previewResult, err := a.Preview("github:owner/repo")
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(previewResult)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "null") || !strings.Contains(string(encoded), `"SymbolSVGs":[]`) || !strings.Contains(string(encoded), `"FootprintSVGs":[]`) || !strings.Contains(string(encoded), `"Files":[]`) {
			t.Fatalf("Preview JSON contains null or omits arrays: %s", encoded)
		}
	})
}

func TestConfiguredKiCadCLIIsPreservedOutsideGUISettings(t *testing.T) {
	t.Run("GUI save preserves configured executable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := settings.Save(path, settings.Settings{OutDir: t.TempDir(), LibName: "parts", KicadCLI: "/custom/kicad-cli"}); err != nil {
			t.Fatal(err)
		}
		a := NewWithOptions(Options{SettingsPath: path})
		if err := a.SaveSettings(Settings{OutDir: t.TempDir(), LibName: "parts", ModelBase: "absolute"}); err != nil {
			t.Fatal(err)
		}
		loaded, err := settings.Load(path)
		if err != nil || loaded.KicadCLI != "/custom/kicad-cli" {
			t.Fatalf("settings=%+v err=%v", loaded, err)
		}
	})
}
