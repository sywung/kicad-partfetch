package kicad

import (
	"bytes"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func TestSymbolMergePreservesUntouchedBytesAndQuotedParentheses(t *testing.T) {
	old := []byte("(kicad_symbol_lib (version 20231120)\n (symbol \"A\" (property \"Value\" \"part (with \\\"quote\\\")\" (at 0 0 0)))\n (symbol \"B\" (property \"Value\" \"old\" (at 0 0 0)))\n)\n")
	oldSpans, e := topSymbols(old)
	if e != nil {
		t.Fatal(e)
	}
	preserved := append([]byte(nil), old[oldSpans[0].start:oldSpans[0].end]...)
	merged, e := MergeSymbols(old, map[string][]byte{"B": []byte("(symbol \"B\" (property \"Value\" \"new\" (at 0 0 0)))")})
	if e != nil {
		t.Fatal(e)
	}
	spans, e := topSymbols(merged)
	if e != nil {
		t.Fatal(e)
	}
	if len(spans) != 2 {
		t.Fatalf("symbols=%d, want 2", len(spans))
	}
	if !bytes.Equal(merged[spans[0].start:spans[0].end], preserved) {
		t.Fatal("untouched symbol bytes changed")
	}
	if !bytes.Contains(merged[spans[1].start:spans[1].end], []byte("new")) {
		t.Fatal("target symbol was not replaced")
	}
}

func TestSanitizeAndNumberFormatting(t *testing.T) {
	if got := Sanitize("19-217/BHC-ZL1M2RY/3T"); got != "19-217_BHC-ZL1M2RY_3T" {
		t.Fatalf("Sanitize=%q", got)
	}
	if got := Sanitize("a b\\c:d"); got != "a_b_c_d" {
		t.Fatalf("Sanitize spaces/path=%q", got)
	}
	for value, want := range map[float64]string{0: "0", -0.00001: "0", 1.2: "1.2", 12.34001: "12.34", -1.25: "-1.25"} {
		if got := Number(value); got != want {
			t.Errorf("Number(%v)=%q, want %q", value, got, want)
		}
	}
}

func TestSymbolPropertyAnglesAreHorizontal(t *testing.T) {
	component := &model.Component{ID: "C1234", Reference: "U", Value: "chip", Datasheet: "https://example.test", Manufacturer: "maker", MPN: "part", JLCPCBClass: "basic"}
	component.Units = []model.SymbolUnit{{Pins: []model.Pin{{X: 1, Y: 2, Length: 1}}}}
	data := Symbol(component, "demo", "pkg")
	for _, property := range []string{"Reference", "Value", "Footprint", "Datasheet", "LCSC", "Manufacturer", "MPN", "JLCPCB Class"} {
		start := strings.Index(string(data), "(property \""+property+"\"")
		if start < 0 {
			t.Fatalf("property %s missing from symbol", property)
		}
		line := strings.SplitN(string(data[start:]), "\n", 2)[0]
		if !regexp.MustCompile(`\(at [^)]* 0\)`).MatchString(line) {
			t.Errorf("property %s is not horizontal: %s", property, line)
		}
	}
}

func TestFootprintReferenceAndValueUseVerticalBounds(t *testing.T) {
	component := &model.Component{Value: "part-number"}
	component.Footprint.Name = "SOT-23"
	component.Footprint.HasBounds = true
	component.Footprint.Bounds = [4]float64{-2, -3, 4, 5}
	data := string(Footprint(component, "demo", "/tmp"))
	if got := footprintTextY(t, data, "reference"); got != -4 {
		t.Errorf("Reference y=%g, want minY-1=-4", got)
	}
	if got := footprintTextY(t, data, "value"); got != 6 {
		t.Errorf("Value y=%g, want maxY+1=6", got)
	}
}

func footprintTextY(t *testing.T, data, name string) float64 {
	t.Helper()
	start := strings.Index(strings.ToLower(data), name)
	if start < 0 {
		t.Fatalf("footprint text %q missing", name)
	}
	at := strings.Index(data[start:], "(at ")
	if at < 0 {
		t.Fatalf("footprint text %q has no position", name)
	}
	fields := strings.Fields(strings.TrimPrefix(data[start+at:], "(at "))
	y, err := strconv.ParseFloat(strings.TrimSuffix(fields[1], ")"), 64)
	if err != nil {
		t.Fatalf("footprint text %q y coordinate: %v", name, err)
	}
	return y
}

func TestFootprintUsesKiCad8TextPropertiesAndFootprintValue(t *testing.T) {
	component := &model.Component{Value: "part-number"}
	component.Footprint.Name = "SOT-23"
	component.Footprint.HasBounds = true
	component.Footprint.Bounds = [4]float64{-2, -3, 4, 5}
	data := string(Footprint(component, "demo", "/tmp"))
	if !strings.Contains(data, `(property "Reference" "REF**" (at 0 -4 0) (layer "F.SilkS")`) {
		t.Fatalf("Reference property missing or misplaced:\n%s", data)
	}
	if !strings.Contains(data, `(property "Value" "SOT-23" (at 0 6 0) (layer "F.Fab")`) {
		t.Fatalf("Value property should contain the footprint name below the bounds:\n%s", data)
	}
}

func TestSlotShortEdgeUsesSmallerDimension(t *testing.T) {
	var out strings.Builder
	footprintPad(&out, model.Pad{Number: "1", Type: "thru_hole", Shape: "oval", Width: 3, Height: 1, Drill: 2, Slot: 1})
	if !strings.Contains(out.String(), "(drill oval 2 1)") {
		t.Fatalf("slot dimensions should be long=2 short=1: %s", out.String())
	}
}

func TestSlotHoleFollowsPadLongAxis(t *testing.T) {
	for _, tt := range []struct{ id, number, want string }{{"C16214", "3", "(drill oval 3 1)"}, {"C134092", "0", "(drill oval 0.6 1.6)"}} {
		t.Run(tt.id, func(t *testing.T) {
			raw, e := os.ReadFile("../source/easyeda/testdata/" + tt.id + ".json")
			if e != nil {
				t.Fatal(e)
			}
			component, e := easyeda.Parse(raw)
			if e != nil {
				t.Fatal(e)
			}
			for _, pad := range component.Footprint.Pads {
				if pad.Number == tt.number {
					var b strings.Builder
					footprintPad(&b, pad)
					if !strings.Contains(b.String(), tt.want) {
						t.Fatalf("slot serialization %q does not include %q", b.String(), tt.want)
					}
					return
				}
			}
			t.Fatalf("pad %s not found", tt.number)
		})
	}
}
