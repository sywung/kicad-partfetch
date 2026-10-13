package easyeda_test

import (
	"os"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func TestDatasheetUsesLCSCLandingPage(t *testing.T) {
	raw, err := os.ReadFile("testdata/C5446.json")
	if err != nil {
		t.Fatal(err)
	}
	component, err := easyeda.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	const url = "https://lcsc.com/product-detail/Low-Dropout-Regulators-LDO_XC6206P332MR_C5446.html"
	got := string(kicad.Symbol(component, "demo", component.Footprint.Name))
	if !strings.Contains(got, `(property "Datasheet" "`+url+`"`) {
		t.Fatalf("symbol Datasheet property does not contain LCSC URL: %s", got)
	}
}
