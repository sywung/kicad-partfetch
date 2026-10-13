package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type recordingCLI struct {
	findErr       error
	symbolLibrary string
	footprints    []string
	leftoverDir   string
}

func (c *recordingCLI) Find(string) (string, error) { return "kicad-cli", c.findErr }
func (c *recordingCLI) SymbolSVGs(_ context.Context, _ string, file string) ([]kicadcli.NamedSVG, error) {
	data, err := os.ReadFile(file)
	c.symbolLibrary = string(data)
	c.leftoverDir = filepath.Dir(file)
	return []kicadcli.NamedSVG{{Name: "s.svg", SVG: []byte("<svg id=\"sym\"/>")}}, err
}
func (c *recordingCLI) FootprintSVGs(_ context.Context, _ string, dir string) ([]kicadcli.NamedSVG, error) {
	entries, err := os.ReadDir(dir)
	for _, entry := range entries {
		c.footprints = append(c.footprints, entry.Name())
	}
	return []kicadcli.NamedSVG{{Name: "f.svg", SVG: []byte("<svg id=\"fp\"/>")}}, err
}

func installedTestApp(t *testing.T, cli *recordingCLI) (*App, string) {
	t.Helper()
	out := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	a := NewWithOptions(Options{SettingsPath: settingsPath, KiCadCLI: cli})
	if err := a.SaveSettings(Settings{OutDir: out, LibName: "demo", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	library := "(kicad_symbol_lib (version 20251024) (generator \"x\")\n" +
		"(symbol \"Parent\" (property \"Footprint\" \"demo:Pad\"))\n" +
		"(symbol \"Child\" (extends \"Parent\") (property \"Footprint\" \"demo:Pad\"))\n" +
		"(symbol \"Other\" (property \"Footprint\" \"demo:Missing\"))\n)\n"
	if err := os.WriteFile(filepath.Join(out, "demo.kicad_sym"), []byte(library), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "demo.pretty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "demo.pretty", "Pad.kicad_mod"), []byte("(footprint \"Pad\")"), 0644); err != nil {
		t.Fatal(err)
	}
	return a, out
}

func TestPreviewInstalledCases(t *testing.T) {
	t.Run("符號與封裝取自元件庫檔案且只送出該符號", func(t *testing.T) {
		cli := &recordingCLI{}
		a, _ := installedTestApp(t, cli)
		got, err := a.PreviewInstalled("Parent")
		if err != nil {
			t.Fatal(err)
		}
		want := "(kicad_symbol_lib (version 20251024) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n(symbol \"Parent\" (property \"Footprint\" \"demo:Pad\"))\n)\n"
		if cli.symbolLibrary != want {
			t.Errorf("symbol library sent to kicad-cli=%q, want %q", cli.symbolLibrary, want)
		}
		if len(cli.footprints) != 1 || cli.footprints[0] != "Pad.kicad_mod" {
			t.Errorf("footprints=%v", cli.footprints)
		}
		if !got.External || got.Source != "library" || got.Name != "Parent" || got.Package != "Pad" || len(got.SymbolSVGs) != 1 || got.SymbolSVGs[0] != "<svg id=\"sym\"/>" || len(got.FootprintSVGs) != 1 || got.FootprintSVGs[0] != "<svg id=\"fp\"/>" {
			t.Errorf("preview=%+v", got)
		}
		if _, statErr := os.Stat(cli.leftoverDir); !os.IsNotExist(statErr) {
			t.Errorf("temporary directory %s remains: %v", cli.leftoverDir, statErr)
		}
	})
	t.Run("衍生符號連同父符號一起送出且父在前", func(t *testing.T) {
		cli := &recordingCLI{}
		a, _ := installedTestApp(t, cli)
		if _, err := a.PreviewInstalled("Child"); err != nil {
			t.Fatal(err)
		}
		parent, child := strings.Index(cli.symbolLibrary, "(symbol \"Parent\""), strings.Index(cli.symbolLibrary, "(symbol \"Child\"")
		if parent < 0 || child < 0 || parent > child || strings.Contains(cli.symbolLibrary, "Other") {
			t.Errorf("symbol library=%q", cli.symbolLibrary)
		}
	})
	t.Run("封裝檔不存在時仍顯示符號並警告", func(t *testing.T) {
		cli := &recordingCLI{}
		a, _ := installedTestApp(t, cli)
		got, err := a.PreviewInstalled("Other")
		if err != nil || len(got.SymbolSVGs) != 1 || len(got.FootprintSVGs) != 0 || len(got.Warnings) != 1 || got.Warnings[0] != "元件庫裡找不到封裝檔 Missing" {
			t.Fatalf("preview=%+v err=%v", got, err)
		}
		if len(cli.footprints) != 0 {
			t.Errorf("kicad-cli was asked for a footprint: %v", cli.footprints)
		}
	})
	t.Run("不存在的符號回錯誤", func(t *testing.T) {
		a, _ := installedTestApp(t, &recordingCLI{})
		if _, err := a.PreviewInstalled("Nope"); err == nil || err.Error() != "元件庫裡找不到這個符號" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("找不到 kicad-cli 回指定錯誤", func(t *testing.T) {
		a, _ := installedTestApp(t, &recordingCLI{findErr: kicadcli.ErrNotFound})
		if _, err := a.PreviewInstalled("Parent"); err == nil || err.Error() != "找不到 kicad-cli，無法顯示元件庫內的圖形" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("結果經 JSON 不含 null", func(t *testing.T) {
		a, _ := installedTestApp(t, &recordingCLI{})
		got, err := a.PreviewInstalled("Other")
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(got)
		if strings.Contains(string(encoded), "null") {
			t.Errorf("json=%s", encoded)
		}
	})
}

type glbCLI struct {
	recordingCLI
	board string
	glb   []byte
}

func (c *glbCLI) BoardGLB(_ context.Context, _ string, board []byte) ([]byte, error) {
	c.board = string(board)
	return c.glb, nil
}

func testGLB(document string) []byte {
	for len(document)%4 != 0 {
		document += " "
	}
	out := []byte("glTF")
	out = binary.LittleEndian.AppendUint32(out, 2)
	out = binary.LittleEndian.AppendUint32(out, uint32(20+len(document)))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(document)))
	out = append(out, "JSON"...)
	return append(out, document...)
}

func TestModelInstalledCases(t *testing.T) {
	setup := func(t *testing.T, cli KiCadCLI, modelLine string, withFile bool) (*App, string) {
		t.Helper()
		out := t.TempDir()
		a := NewWithOptions(Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), KiCadCLI: cli})
		if err := a.SaveSettings(Settings{OutDir: out, LibName: "demo", ModelBase: "kiprjmod"}); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(out, "demo.kicad_sym"), []byte("(kicad_symbol_lib (version 20231120)\n(symbol \"Part\" (property \"Footprint\" \"demo:Pad\"))\n)\n"), 0644)
		os.MkdirAll(filepath.Join(out, "demo.pretty"), 0755)
		os.MkdirAll(filepath.Join(out, "demo.3dshapes"), 0755)
		os.WriteFile(filepath.Join(out, "demo.pretty", "Pad.kicad_mod"), []byte("(footprint \"Pad\" (layer \"F.Cu\")"+modelLine+")"), 0644)
		if withFile {
			os.WriteFile(filepath.Join(out, "demo.3dshapes", "body.step"), []byte("step"), 0644)
		}
		return a, out
	}
	t.Run("模型路徑改指向元件庫的 3dshapes 並回傳 base64 GLB", func(t *testing.T) {
		glb := testGLB(`{"meshes":[{}]}`)
		cli := &glbCLI{glb: glb}
		a, out := setup(t, cli, ` (model "${KIPRJMOD}/libs/demo.3dshapes/body.step" (offset (xyz 1 2 3)))`, true)
		got, err := a.ModelInstalled("Part")
		if err != nil {
			t.Fatal(err)
		}
		if got.GLB != base64.StdEncoding.EncodeToString(glb) {
			t.Errorf("GLB payload differs")
		}
		wantModel := `(model "` + filepath.ToSlash(filepath.Join(out, "demo.3dshapes", "body.step")) + `" (offset (xyz 1 2 3)))`
		if !strings.HasPrefix(cli.board, "(kicad_pcb (version 20240108)") || !strings.Contains(cli.board, wantModel) || !strings.HasSuffix(cli.board, "\n)\n") {
			t.Errorf("board=%q, want it to contain %q", cli.board, wantModel)
		}
	})
	t.Run("模型檔不在元件庫時路徑不改", func(t *testing.T) {
		cli := &glbCLI{glb: testGLB(`{"meshes":[{}]}`)}
		a, _ := setup(t, cli, ` (model "C:/elsewhere/body.step")`, false)
		if _, err := a.ModelInstalled("Part"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(cli.board, `(model "C:/elsewhere/body.step")`) {
			t.Errorf("board=%q", cli.board)
		}
	})
	t.Run("KiCad 匯出空的 GLB 時回指定錯誤", func(t *testing.T) {
		a, _ := setup(t, &glbCLI{glb: testGLB(`{"meshes":[]}`)}, "", false)
		if _, err := a.ModelInstalled("Part"); err == nil || !strings.HasPrefix(err.Error(), "這個封裝沒有可顯示的 3D 模型") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("不是 GLB 的內容視為沒有模型", func(t *testing.T) {
		a, _ := setup(t, &glbCLI{glb: []byte("not a glb")}, "", false)
		if _, err := a.ModelInstalled("Part"); err == nil {
			t.Fatal("expected an error")
		}
	})
	t.Run("kicad-cli 介面不支援匯出時回錯誤", func(t *testing.T) {
		a, _ := setup(t, &recordingCLI{}, "", false)
		if _, err := a.ModelInstalled("Part"); err == nil || err.Error() != "目前的 kicad-cli 介面不支援 3D 匯出" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("不存在的符號回錯誤", func(t *testing.T) {
		a, _ := setup(t, &glbCLI{}, "", false)
		if _, err := a.ModelInstalled("Nope"); err == nil || err.Error() != "元件庫裡找不到這個符號" {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestModelExternalCases(t *testing.T) {
	run := func(t *testing.T, contents gh.Contents, files map[string]string, glb []byte) (InstalledModel, error, *glbCLI, *appFakeGitHub) {
		t.Helper()
		contents.Repo.FullName = "owner/repo"
		github := &appFakeGitHub{contents: contents, files: files}
		cli := &glbCLI{glb: glb}
		a := NewWithOptions(Options{GitHub: github, KiCadCLI: cli, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
		got, err := a.ModelExternal("github:owner/repo")
		return got, err, cli, github
	}
	meshes := testGLB(`{"meshes":[{}]}`)
	t.Run("封裝有引用該模型時依封裝擺放", func(t *testing.T) {
		contents := gh.Contents{Footprints: []gh.File{{Path: "fp/Board.kicad_mod"}}, Models: []gh.File{{Path: "3d/Body.STEP"}}}
		files := map[string]string{"fp/Board.kicad_mod": `(footprint "Board" (layer "F.Cu") (model "C:/Users/x/body.step" (offset (xyz 1 2 3))))`, "3d/Body.STEP": "step"}
		got, err, cli, github := run(t, contents, files, meshes)
		if err != nil {
			t.Fatal(err)
		}
		local := filepath.ToSlash(filepath.Join(github.downloadDir, "3d", "Body.STEP"))
		if !strings.Contains(cli.board, `(footprint "Board" (layer "F.Cu") (model "`+local+`" (offset (xyz 1 2 3))))`) {
			t.Errorf("board=%q", cli.board)
		}
		if got.File != "3d/Body.STEP" || got.Note != "依封裝 Board 的設定擺放" || got.GLB != base64.StdEncoding.EncodeToString(meshes) {
			t.Errorf("result=%+v", got)
		}
		if _, statErr := os.Stat(github.downloadDir); !os.IsNotExist(statErr) {
			t.Errorf("temporary directory remains: %v", statErr)
		}
	})
	t.Run("沒有封裝引用時以替身封裝顯示模型本體", func(t *testing.T) {
		contents := gh.Contents{Footprints: []gh.File{{Path: "A.kicad_mod"}}, Models: []gh.File{{Path: "ttgo1.wrl"}, {Path: "z.wrl"}}}
		files := map[string]string{"A.kicad_mod": `(module A (layer F.Cu))`, "ttgo1.wrl": "wrl", "z.wrl": "wrl"}
		got, err, cli, github := run(t, contents, files, meshes)
		if err != nil {
			t.Fatal(err)
		}
		local := filepath.ToSlash(filepath.Join(github.downloadDir, "ttgo1.wrl"))
		if !strings.Contains(cli.board, `(footprint "model" (layer "F.Cu") (model "`+local+`" (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0))))`) || strings.Contains(cli.board, "module A") {
			t.Errorf("board=%q", cli.board)
		}
		want := "這個模型沒有掛在任何封裝上，只顯示模型本體；加入後需在 KiCad 的封裝屬性手動指定（共 2 個 3D 檔，顯示第一個）"
		if got.File != "ttgo1.wrl" || got.Note != want {
			t.Errorf("result=%+v", got)
		}
	})
	t.Run("repo 沒有 3D 檔回錯誤且不下載", func(t *testing.T) {
		_, err, _, github := run(t, gh.Contents{}, nil, meshes)
		if err == nil || err.Error() != "這個 repo 沒有 3D 模型檔" || github.downloadCalls != 0 {
			t.Fatalf("err=%v downloads=%d", err, github.downloadCalls)
		}
	})
	t.Run("KiCad 產不出幾何時回錯誤", func(t *testing.T) {
		contents := gh.Contents{Models: []gh.File{{Path: "m.wrl"}}}
		_, err, _, _ := run(t, contents, map[string]string{"m.wrl": "wrl"}, testGLB(`{"meshes":[]}`))
		if err == nil || err.Error() != "KiCad 無法從這個 3D 檔產生可顯示的模型" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("非 GitHub 的 PartID 被拒絕", func(t *testing.T) {
		a := NewWithOptions(Options{GitHub: &appFakeGitHub{}, KiCadCLI: &glbCLI{}, SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
		if _, err := a.ModelExternal("C2040"); err == nil {
			t.Fatal("expected an error")
		}
	})
}
