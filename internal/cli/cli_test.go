package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

type testTransport func(*http.Request) (*http.Response, error)

type instantClock struct{ now time.Time }

func (c *instantClock) Now() time.Time { return c.now }
func (c *instantClock) Sleep(_ context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return nil
}

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}
}

func TestAddEndToEndWithFakeHTTP(t *testing.T) {
	fixture, e := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	client := fixtureAddClient(t, fixture, &calls)
	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"add", "--out", outDir, "--lib", "demo", "--json", "--obj", "C2040"}, client, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	component, e := easyeda.Parse(fixture)
	if e != nil {
		t.Fatal(e)
	}
	assertAddArtifacts(t, outDir, component, stdout.Bytes(), calls)
}

func fixtureAddClient(t *testing.T, fixture []byte, calls *int) *easyeda.Client {
	t.Helper()
	return easyeda.NewClient(easyeda.ClientOptions{NoCache: true, CacheDir: t.TempDir(), Clock: &instantClock{now: time.Unix(1, 0)}, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		if strings.Contains(r.URL.Path, "/api/products/C2040/") {
			return testResponse(r, string(fixture)), nil
		}
		if strings.Contains(r.URL.Path, "/3dmodel/") {
			return testResponse(r, "v 0 0 0\nv 1 1 1\n"), nil
		}
		return testResponse(r, "STEP fixture"), nil
	})})
}

func assertAddArtifacts(t *testing.T, outDir string, component *model.Component, result []byte, calls int) {
	t.Helper()
	if calls != 3 {
		t.Fatalf("HTTP calls=%d, want component, STEP, and OBJ", calls)
	}
	var got output
	if err := json.Unmarshal(bytes.TrimSpace(result), &got); err != nil {
		t.Fatal(err)
	}
	if got.Symbol != "added" || got.Footprint != "added" || got.Model != "added" {
		t.Fatalf("statuses=%+v", got)
	}
	paths := outputPaths(outDir, component)
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("output missing %s: %v", path, err)
		}
	}
	symbolBytes, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(symbolBytes, []byte("demo:"+component.Footprint.Name)) {
		t.Fatal("symbol Footprint property is missing the library-qualified footprint")
	}
}

func outputPaths(outDir string, component *model.Component) []string {
	modelBase := filepath.Join(outDir, "demo.3dshapes", component.Footprint.Models[0].Title)
	return []string{
		filepath.Join(outDir, "demo.kicad_sym"),
		filepath.Join(outDir, "demo.pretty", component.Footprint.Name+".kicad_mod"),
		modelBase + ".step",
		modelBase + ".obj",
	}
}

func TestAddFallsBackWhenOBJDownloadFails(t *testing.T) {
	fixture, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	client := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, Clock: &instantClock{now: time.Unix(1, 0)}, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/api/products/C2040/") {
			return testResponse(r, string(fixture)), nil
		}
		if strings.Contains(r.URL.Path, "/3dmodel/") {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("offline")), Header: make(http.Header), Request: r}, nil
		}
		return testResponse(r, "STEP fixture"), nil
	})})
	outDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"add", "--out", outDir, "--lib", "demo", "--json", "C2040"}, client, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "3D 位置未經校正") {
		t.Fatalf("OBJ fallback warning missing: %s", stdout.String())
	}
	component, err := easyeda.Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	footprint := filepath.Join(outDir, "demo.pretty", component.Footprint.Name+".kicad_mod")
	data, err := os.ReadFile(footprint)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("(offset (xyz ")) || !bytes.Contains(data, []byte(" 0)) (scale (xyz 1 1 1))")) {
		t.Fatalf("fallback model placement missing or has nonzero z: %s", data)
	}
}

func TestListCommandOutput(t *testing.T) {
	t.Run("text output includes artifact flags", func(t *testing.T) {
		out := t.TempDir()
		writeCLISymbolLibrary(t, out, ` (symbol "Chip" (property "Footprint" "demo:QFN") (property "LCSC" "C10"))`)
		writeCLIFile(t, filepath.Join(out, "demo.pretty", "QFN.kicad_mod"), `(footprint "QFN" (model "model.step"))`)
		writeCLIFile(t, filepath.Join(out, "demo.3dshapes", "model.step"), "step")
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"list", "--out", out, "--lib", "demo"}, nil, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 || stdout.String() != "Chip\tQFN\tC10\tfootprint=yes\tmodel=yes\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("JSON output includes fields", func(t *testing.T) {
		out := t.TempDir()
		writeCLISymbolLibrary(t, out, ` (symbol "Bare" (property "Footprint" "") (property "LCSC" "C1"))`)
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"list", "--out", out, "--lib", "demo", "--json"}, nil, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 || stdout.String() != "[{\"name\":\"Bare\",\"footprint\":\"\",\"lcsc\":\"C1\",\"hasFootprint\":false,\"hasModel\":false}]\n" {
			t.Fatalf("JSON exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("empty library text output is empty", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"list", "--out", t.TempDir(), "--lib", "empty"}, nil, &stdout, &stderr)
		if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("empty library JSON is an empty array", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"list", "--out", t.TempDir(), "--lib", "empty", "--json"}, nil, &stdout, &stderr)
		if code != 0 || stdout.String() != "[]\n" {
			t.Fatalf("empty JSON exit=%d stdout=%q", code, stdout.String())
		}
	})
}

func TestRemoveCommandOutputAndExitCodes(t *testing.T) {
	t.Run("text output and success exit code zero", func(t *testing.T) {
		out := t.TempDir()
		writeCLISymbolLibrary(t, out, ` (symbol "A" (property "Footprint" "") (property "LCSC" "C1"))`)
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"remove", "--out", out, "--lib", "demo", "A"}, nil, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 || stdout.String() != "A: symbol=removed footprint=none model=none\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("JSON output has one object per input", func(t *testing.T) {
		out := t.TempDir()
		writeCLISymbolLibrary(t, out, ` (symbol "A" (property "Footprint" "") (property "LCSC" "C1"))`)
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"remove", "--out", out, "--lib", "demo", "--json", "A"}, nil, &stdout, &stderr)
		if code != 0 || stderr.Len() != 0 || stdout.String() != "{\"name\":\"A\",\"symbol\":\"removed\",\"footprint\":\"none\",\"model\":\"none\"}\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("missing symbol returns exit code one", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"remove", "--out", t.TempDir(), "--lib", "demo", "Absent"}, nil, &stdout, &stderr)
		if code != 1 || stdout.String() != "Absent: symbol=missing footprint=none model=none\n" || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
	t.Run("no names prints exact error and exit code two", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"remove", "--out", t.TempDir(), "--lib", "demo"}, nil, &stdout, &stderr)
		if code != 2 || stdout.Len() != 0 || stderr.String() != "remove requires one or more symbol names or part numbers\n" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
	})
}

func TestCommandUsageIncludesLibraryCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), nil, nil, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || stderr.String() != "usage: partfetch <add|info|list|remove|import|search> ...\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func writeCLISymbolLibrary(t *testing.T, out string, symbols ...string) {
	t.Helper()
	content := "(kicad_symbol_lib (version 20231120) (generator \"test\")\n"
	for _, symbol := range symbols {
		content += symbol + "\n"
	}
	content += ")\n"
	writeCLIFile(t, filepath.Join(out, "demo.kicad_sym"), content)
}

func writeCLIFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestKiCadCLIExportsAllFixtureSymbolsAndFootprints(t *testing.T) {
	binary := "/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli"
	if _, e := os.Stat(binary); e != nil {
		t.Skip("kicad-cli not installed at the specified path")
	}
	out := t.TempDir()
	components := loadFixtureComponents(t)
	opts := library.Options{Out: out, Name: "fixturelib"}
	for _, c := range components {
		if _, e := library.Write(opts, c, nil); e != nil {
			t.Fatalf("write %s: %v", c.ID, e)
		}
	}
	libFile := filepath.Join(out, "fixturelib.kicad_sym")
	fpFiles, e := filepath.Glob(filepath.Join(out, "fixturelib.pretty", "*.kicad_mod"))
	if e != nil || len(fpFiles) != footprintCount(components) {
		t.Fatalf("generated footprint files=%d, err=%v", len(fpFiles), e)
	}
	symDir := filepath.Join(out, "sym-svg")
	fpDir := filepath.Join(out, "fp-svg")
	if e := os.MkdirAll(symDir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(fpDir, 0755); e != nil {
		t.Fatal(e)
	}
	runKiCad(t, binary, "sym", "export", "svg", libFile, "-o", symDir)
	runKiCad(t, binary, "fp", "export", "svg", filepath.Join(out, "fixturelib.pretty"), "-o", fpDir)
	symbolFiles := 0
	for _, component := range components {
		symbolFiles += len(component.Units)
	}
	checkSVGCount(t, symDir, symbolFiles)
	checkSVGCount(t, fpDir, footprintCount(components))
}

func footprintCount(components []*model.Component) int {
	names := map[string]bool{}
	for _, component := range components {
		names[component.Footprint.Name] = true
	}
	return len(names)
}

func loadFixtureComponents(t *testing.T) []*model.Component {
	t.Helper()
	files, e := filepath.Glob("../source/easyeda/testdata/C*.json")
	if e != nil {
		t.Fatal(e)
	}
	out := make([]*model.Component, 0, len(files))
	for _, path := range files {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		component, e := easyeda.Parse(raw)
		if e != nil {
			t.Fatalf("parse %s: %v", path, e)
		}
		out = append(out, component)
	}
	if len(out) != 15 {
		t.Fatalf("fixtures=%d, want 15", len(out))
	}
	return out
}

func runKiCad(t *testing.T, binary string, args ...string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("kicad-cli %s failed: %v\n%s", strings.Join(args, " "), e, out)
	}
}
func checkSVGCount(t *testing.T, dir string, want int) {
	t.Helper()
	count := 0
	e := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".svg") {
			count++
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if count != want {
		t.Fatalf("SVG files in %s=%d, want %d", dir, count, want)
	}
}
