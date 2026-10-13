package easyeda_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func TestCommunitySymbolUsesEasyEDAPartIDProperty(t *testing.T) {
	data, err := os.ReadFile("testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	component, err := easyeda.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	component.ID = "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a"
	symbol := string(kicad.Symbol(component, "demo", component.Footprint.Name))
	if !strings.Contains(symbol, `(property "EasyEDA" "f2fc22956d5f4a41aa18d1a4337fd37a"`) || strings.Contains(symbol, `(property "LCSC"`) {
		t.Fatalf("symbol has unexpected source properties: %s", symbol)
	}
}

func TestLCSCFixtureSymbolBytesStayUnchanged(t *testing.T) {
	wants := map[string]string{
		"C134092.json": "b1d4251c8567bd910f65a596023e171e2c0734ea46b365ce95de4c0edd7fc65a",
		"C16214.json":  "48c14dd16cc140f48b24a64d42f3da5886fa6932f0e6d383628e41d8ad819270",
		"C165948.json": "3aa0bfa1dda1102ee1fd3ad0e56783f6618f60a0495d2a1828a0cf80e63e46d2",
		"C2040.json":   "eff22a64d576d8252991ae3a174026aa6610b588811f891399b645b5e3c93ce8",
		"C2337.json":   "6a41199fdec48bb1e2bbf89219b70c726c739b5693bd769d3dbfa08ed47e49f7",
		"C25804.json":  "bf311b8e48dc6d5e97fbefddde862181c93309411b7abdd135d2f0e645fabfd9",
		"C393939.json": "b27e4a9a9ea6a86da8f18065954557cb1e13ef7ff0640a2e3c5c5a3aaff939c4",
		"C5446.json":   "8d9f0736a969656cbc73e8b5ac0672942970fc903ce128fe3eb64a998ec3a8c0",
		"C5590.json":   "9ea7ada3272e36809a8ca999373467f27b8ad863aeca4fd6658f6c4da48a706f",
		"C6186.json":   "646039addc84a65315d23853f62898fc18984e0f54d3a2d4199ef6c47036c426",
		"C71035.json":  "a13304d4191a3fdb18b765f043188bde62192d27a9659ac3aea87dabf4920ae3",
		"C72041.json":  "eb5376fadeec2afdeaf37bb9dbc2f0df79a3364a1fc038b57cf4d59344faf03d",
		"C7426.json":   "596311e855091b56901e7b61f54790019ab8b68f8678689b71682dae2dde2d06",
		"C7950.json":   "354f80eb8a7892f8219d734a32438d142e791b86c4b46081ebe184dcb2f36874",
		"C8734.json":   "e428770996d08a2db276de49ef8d24c8547a9fd21a8b874922b244cbfbe827b1",
	}
	for file, want := range wants {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + file)
			if err != nil {
				t.Fatal(err)
			}
			component, err := easyeda.Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			got := sha256.Sum256(kicad.Symbol(component, "demo", component.Footprint.Name))
			if fmt.Sprintf("%x", got) != want {
				t.Fatalf("LCSC symbol bytes changed: SHA-256=%x, want %s", got, want)
			}
		})
	}
}
