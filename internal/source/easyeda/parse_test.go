package easyeda

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/model"
)

type expectedComponent struct {
	LCSC    string `json:"lcsc"`
	Title   string `json:"title"`
	Package string `json:"package"`
	Units   []struct {
		Pins []struct {
			Number, Name        string
			X, Y, Angle, Length float64
		}
	} `json:"units"`
	Pads []struct {
		Number                            string
		X, Y, W, H, Drill, Slot, Rotation float64
	} `json:"pads"`
	NPTH  int                           `json:"npth_holes"`
	Model *struct{ UUID, Title string } `json:"model3d"`
}

func TestAllFixturesMatchIndependentExpectations(t *testing.T) {
	files, e := filepath.Glob("testdata/C*.json")
	if e != nil {
		t.Fatal(e)
	}
	if len(files) != 15 {
		t.Fatalf("want all 15 fixtures, found %d", len(files))
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
			got, want := loadExpectedFixture(t, file)
			assertExpectedComponent(t, got, want)
		})
	}
}

func TestIntentionalFootprintSkipsDoNotWarn(t *testing.T) {
	shapes := []any{
		"TEXT~ignored",
		"SOLIDREGION~3~~M0 0 L1 1 Z~cutout",
		"SOLIDREGION~100~~M0 0 L1 1 Z~solid",
		"SOLIDREGION~100~~M0 0 L1 1 Z~npth",
		"SOLIDREGION~99~~M0 0 L1 1 Z~npth",
		"SOLIDREGION~99~~M0 0 L1 1 Z~solid",
	}
	var footprint model.Footprint
	warnings, err := parseFootprintShapes(shapes, 0, 0, &footprint)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("intentionally skipped shapes produced warnings: %v", warnings)
	}
	if !footprint.HasCourtyard || len(footprint.Graphics) != 0 {
		t.Fatalf("layer 99 courtyard or skipped graphics are wrong: %+v", footprint)
	}
}

func loadExpectedFixture(t *testing.T, file string) (*model.Component, expectedComponent) {
	t.Helper()
	input, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	wantBytes, e := os.ReadFile(filepath.Join("testdata", "expect", filepath.Base(file)))
	if e != nil {
		t.Fatal(e)
	}
	var want expectedComponent
	if e = json.Unmarshal(wantBytes, &want); e != nil {
		t.Fatal(e)
	}
	got, e := Parse(input)
	if e != nil {
		t.Fatal(e)
	}
	return got, want
}

func assertExpectedComponent(t *testing.T, got *model.Component, want expectedComponent) {
	t.Helper()
	assertComponentIdentity(t, got, want)
	assertExpectedPins(t, got, want)
	assertExpectedPads(t, got, want)
	if len(got.Footprint.NPTHoles) != want.NPTH {
		t.Errorf("npth holes=%d, want %d", len(got.Footprint.NPTHoles), want.NPTH)
	}
	assertExpectedModel(t, got, want.Model)
}

func assertComponentIdentity(t *testing.T, got *model.Component, want expectedComponent) {
	t.Helper()
	if got.ID != want.LCSC || got.Title != want.Title || got.Footprint.Name != want.Package {
		t.Fatalf("component got (%q,%q,%q), want (%q,%q,%q)", got.ID, got.Title, got.Footprint.Name, want.LCSC, want.Title, want.Package)
	}
	if len(got.Units) != len(want.Units) {
		t.Fatalf("units=%d, want %d", len(got.Units), len(want.Units))
	}
}

func assertExpectedPins(t *testing.T, got *model.Component, want expectedComponent) {
	t.Helper()
	for ui, unit := range want.Units {
		if len(got.Units[ui].Pins) != len(unit.Pins) {
			t.Fatalf("unit %d pins=%d, want %d", ui+1, len(got.Units[ui].Pins), len(unit.Pins))
		}
		for pi, expected := range unit.Pins {
			actual := got.Units[ui].Pins[pi]
			if actual.Number != expected.Number || actual.Name != expected.Name {
				t.Errorf("unit %d pin %d identity=(%q,%q), want (%q,%q)", ui+1, pi, actual.Number, actual.Name, expected.Number, expected.Name)
			}
			closeField(t, "pin x", actual.X, expected.X)
			closeField(t, "pin y", actual.Y, expected.Y)
			closeField(t, "pin angle", actual.Angle, expected.Angle)
			closeField(t, "pin length", actual.Length, expected.Length)
		}
	}
}

func assertExpectedPads(t *testing.T, got *model.Component, want expectedComponent) {
	t.Helper()
	if len(got.Footprint.Pads) != len(want.Pads) {
		t.Fatalf("pads=%d, want %d", len(got.Footprint.Pads), len(want.Pads))
	}
	for i, expected := range want.Pads {
		actual := got.Footprint.Pads[i]
		if actual.Number != expected.Number {
			t.Errorf("pad %d number=%q, want %q", i, actual.Number, expected.Number)
		}
		closeField(t, "pad x", actual.X, expected.X)
		closeField(t, "pad y", actual.Y, expected.Y)
		closeField(t, "pad width", actual.Width, expected.W)
		closeField(t, "pad height", actual.Height, expected.H)
		closeField(t, "pad drill", actual.Drill, expected.Drill)
		closeField(t, "pad slot", actual.Slot, expected.Slot)
	}
}

func assertExpectedModel(t *testing.T, got *model.Component, want *struct{ UUID, Title string }) {
	t.Helper()
	if want == nil {
		if len(got.Footprint.Models) != 0 {
			t.Errorf("unexpected model %+v", got.Footprint.Models)
		}
		return
	}
	if len(got.Footprint.Models) != 1 || got.Footprint.Models[0].UUID != want.UUID || got.Footprint.Models[0].Title != want.Title {
		t.Errorf("model mismatch: got %+v, want %+v", got.Footprint.Models, want)
	}
}

func TestRECTPadPointSetsMatchRotatedDimensions(t *testing.T) {
	files, _ := filepath.Glob("testdata/C*.json")
	for _, file := range files {
		testFixtureRECTPads(t, file)
	}
}

func testFixtureRECTPads(t *testing.T, file string) {
	t.Helper()
	raw, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	root, e := decode(raw)
	if e != nil {
		t.Fatal(e)
	}
	pkg := obj(obj(obj(root["result"])["packageDetail"])["dataStr"])
	head := obj(pkg["head"])
	ox, _ := field(head, "x")
	oy, _ := field(head, "y")
	component, e := Parse(raw)
	if e != nil {
		t.Fatalf("%s: %v", file, e)
	}
	padIndex := 0
	for _, shape := range list(pkg["shape"]) {
		record := strings.Split(str(shape), "~")
		if record[0] != "PAD" {
			continue
		}
		pad := component.Footprint.Pads[padIndex]
		padIndex++
		if record[1] != "RECT" {
			continue
		}
		assertRECTPadCorners(t, file, record, pad, ox, oy)
	}
}

func assertRECTPadCorners(t *testing.T, file string, record []string, pad model.Pad, ox, oy float64) {
	t.Helper()
	coords := strings.Fields(strings.ReplaceAll(record[10], ",", " "))
	if len(coords) != 8 {
		t.Fatalf("%s pad %q has %d point fields", file, pad.Number, len(coords))
	}
	want := rotatedPadCorners(pad)
	used := make([]bool, len(want))
	for i := 0; i < 4; i++ {
		x, err := numberAt(coords, i*2)
		if err != nil {
			t.Fatal(err)
		}
		y, err := numberAt(coords, i*2+1)
		if err != nil {
			t.Fatal(err)
		}
		actual := model.Point{X: (x - ox) * unitMM, Y: (y - oy) * unitMM}
		if !matchPoint(actual, want, used) {
			t.Errorf("%s pad %q corner (%f,%f) unmatched", file, pad.Number, actual.X, actual.Y)
		}
	}
}

func rotatedPadCorners(pad model.Pad) []model.Point {
	angle := pad.Rotation * math.Pi / 180
	corners := []model.Point{{X: -pad.Width / 2, Y: -pad.Height / 2}, {X: pad.Width / 2, Y: -pad.Height / 2}, {X: pad.Width / 2, Y: pad.Height / 2}, {X: -pad.Width / 2, Y: pad.Height / 2}}
	for i, p := range corners {
		corners[i] = model.Point{X: pad.X + p.X*math.Cos(angle) - p.Y*math.Sin(angle), Y: pad.Y + p.X*math.Sin(angle) + p.Y*math.Cos(angle)}
	}
	return corners
}

func matchPoint(actual model.Point, want []model.Point, used []bool) bool {
	for i, p := range want {
		if !used[i] && math.Hypot(actual.X-p.X, actual.Y-p.Y) <= .01 {
			used[i] = true
			return true
		}
	}
	return false
}

func closeField(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-3 {
		t.Errorf("%s=%0.6f, want %0.6f", name, got, want)
	}
}
