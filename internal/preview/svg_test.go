package preview

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func TestSVGFixturesAreWellFormed(t *testing.T) {
	files, err := filepath.Glob("../source/easyeda/testdata/C*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 15 {
		t.Fatalf("fixtures=%d, want 15", len(files))
	}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		component, err := easyeda.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		for i, unit := range component.Units {
			assertXML(t, SymbolSVG(unit, i+1), file+" symbol")
		}
		assertXML(t, FootprintSVG(component.Footprint), file+" footprint")
	}
}
func assertXML(t *testing.T, data, label string) {
	t.Helper()
	decoder := xml.NewDecoder(strings.NewReader(data))
	for {
		_, err := decoder.Token()
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("%s: invalid XML: %v", label, err)
		}
	}
}

func TestSymbolSVGViewBoxAndEscaping(t *testing.T) {
	svg := SymbolSVG(model.SymbolUnit{Graphics: []model.Graphic{{Kind: "text", Center: model.Point{X: 2, Y: 3}, Text: `<&"`}}, Pins: []model.Pin{{Number: "<1>", Name: "A&B", NameVisible: true, NumberVisible: true, X: -5, Y: 1, Angle: 0, Length: 2}}}, 1)
	assertXML(t, svg, "symbol")
	if !strings.Contains(svg, "viewBox=") || strings.Contains(svg, "width=") || strings.Contains(svg, "height=") {
		t.Fatalf("unexpected viewbox sizing: %s", svg)
	}
	if !strings.Contains(svg, "&lt;") || !strings.Contains(svg, "&amp;") {
		t.Fatalf("text was not escaped: %s", svg)
	}
}

func TestFootprintSVGPadShapesAndThemeClasses(t *testing.T) {
	fp := model.Footprint{Name: "P<&", Pads: []model.Pad{{Number: "1", Shape: "rect", Width: 2, Height: 1}, {Number: "2", Shape: "circle", Width: 1, Height: 1}, {Number: "3", Shape: "oval", Width: 2, Height: 1}, {Number: "4", Shape: "custom", Width: .2, Height: .2, Points: []model.Point{{X: -1, Y: -1}, {X: 1, Y: -1}, {X: 0, Y: 1}}}, {Number: "5", Shape: "rect", Type: "thru_hole", Width: 2, Height: 2, Drill: .8}}, Graphics: []model.FootprintGraphic{{Kind: "polyline", Layer: "F.SilkS", Points: []model.Point{{X: -2, Y: -2}, {X: 2, Y: 2}}}}}
	svg := FootprintSVG(fp)
	assertXML(t, svg, "footprint shapes")
	for _, needle := range []string{"fp-cu-f", "fp-cu-th", "fp-hole", "fp-silk", "fp-pad-num", "<polygon"} {
		if !strings.Contains(svg, needle) {
			t.Errorf("missing %s", needle)
		}
	}
}
