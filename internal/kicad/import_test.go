package kicad

import (
	"bytes"
	"testing"
)

func TestLibraryVersion(t *testing.T) {
	tests := []struct {
		name string
		data string
		want int
	}{
		{name: "有版本", data: `(kicad_symbol_lib (version 20251024) (symbol "A"))`, want: 20251024},
		{name: "沒有版本", data: `(kicad_symbol_lib (generator "test") (symbol "A"))`, want: 0},
		{name: "版本只出現在字串陷阱", data: `(kicad_symbol_lib (property "Description" "(version 20991231)"))`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LibraryVersion([]byte(tt.data)); got != tt.want {
				t.Fatalf("LibraryVersion=%d, want %d", got, tt.want)
			}
		})
	}
}

func TestMergeSymbolsLibraryVersion(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		want     string
	}{
		{name: "既有檔頭保留 20251024", existing: "(kicad_symbol_lib (version 20251024) (generator \"old\")\n(symbol \"A\"))\n", want: "(kicad_symbol_lib (version 20251024) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n(symbol \"A\" (property \"Value\" \"new\"))\n)\n"},
		{name: "既有檔頭 20231120 輸出逐位元組相同", existing: "(kicad_symbol_lib (version 20231120)\n(symbol \"A\"))\n", want: "(kicad_symbol_lib (version 20231120) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n(symbol \"A\" (property \"Value\" \"new\"))\n)\n"},
		{name: "沒有既有檔輸出逐位元組相同", existing: "", want: "(kicad_symbol_lib (version 20231120) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n(symbol \"A\" (property \"Value\" \"new\"))\n)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MergeSymbols([]byte(tt.existing), map[string][]byte{"A": []byte(`(symbol "A" (property "Value" "new"))`)})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tt.want)) {
				t.Fatalf("MergeSymbols bytes differ\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestRewriteSymbolFootprints(t *testing.T) {
	tests := []struct{ name, input, value, want string }{
		{name: "一般情況且其餘位元組不變", input: "(symbol \"A\"\n  (property \"Footprint\" \"Old\" (at 1 2 3))\n  (property \"Value\" \"keep\" (at 0 0 0))\n)\n", value: "demo:New", want: "(symbol \"A\"\n  (property \"Footprint\" \"demo:New\" (at 1 2 3))\n  (property \"Value\" \"keep\" (at 0 0 0))\n)\n"},
		{name: "值含跳脫引號與反斜線", input: "(symbol \"A\" (property \"Footprint\" \"Old\" (at 0 0 0)))", value: `demo:New "Q"\P`, want: `(symbol "A" (property "Footprint" "demo:New \"Q\"\\P" (at 0 0 0)))`},
		{name: "子符號同名屬性字樣字串陷阱", input: "(symbol \"A\" (property \"Footprint\" \"Old\" (at 0 0 0)) (symbol \"A_0_1\" (property \"Footprint\" \"Nested\" (at 0 0 0)) (property \"Value\" \"(property \\\"Footprint\\\" \\\"trap\\\")\")))", value: "demo:Top", want: "(symbol \"A\" (property \"Footprint\" \"demo:Top\" (at 0 0 0)) (symbol \"A_0_1\" (property \"Footprint\" \"Nested\" (at 0 0 0)) (property \"Value\" \"(property \\\"Footprint\\\" \\\"trap\\\")\")))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RewriteSymbolFootprints([]byte(tt.input), map[string]string{"A": tt.value})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tt.want)) {
				t.Fatalf("rewrite bytes differ\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestRewriteFootprintModelPaths(t *testing.T) {
	tests := []struct {
		name, input  string
		replacements map[string]string
		want         string
	}{
		{name: "一個模型路徑", input: `(footprint "FP" (model "old/part.step" (offset (xyz 0 0 0))))`, replacements: map[string]string{"old/part.step": "${KIPRJMOD}/demo.3dshapes/part.step"}, want: `(footprint "FP" (model "${KIPRJMOD}/demo.3dshapes/part.step" (offset (xyz 0 0 0))))`},
		{name: "兩個模型路徑", input: `(footprint "FP" (model "a.step") (model "b.wrl"))`, replacements: map[string]string{"a.step": "/lib/a.step", "b.wrl": "/lib/b.wrl"}, want: `(footprint "FP" (model "/lib/a.step") (model "/lib/b.wrl"))`},
		{name: "路徑含反斜線", input: `(footprint "FP" (model "old\\folder\\part.step"))`, replacements: map[string]string{`old\folder\part.step`: `C:\libs\part.step`}, want: `(footprint "FP" (model "C:\\libs\\part.step"))`},
		{name: "字串內含 model 字樣陷阱", input: `(footprint "FP" (property "Value" "(model \"trap.step\")") (model "old.step"))`, replacements: map[string]string{"old.step": "new.step", "trap.step": "bad.step"}, want: `(footprint "FP" (property "Value" "(model \"trap.step\")") (model "new.step"))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := RewriteFootprintModels([]byte(tt.input), tt.replacements)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tt.want)) {
				t.Fatalf("rewrite bytes differ\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestInsertFootprintModel(t *testing.T) {
	t.Run("最外層結尾之前插入完整 model 區塊", func(t *testing.T) {
		got, ok := InsertFootprintModel([]byte("(footprint \"FP\" (property \"Value\" \"keep\"))\n"), "/demo.3dshapes/part.step")
		want := "(footprint \"FP\" (property \"Value\" \"keep\")\n  (model \"/demo.3dshapes/part.step\" (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0)))\n)\n"
		if !ok {
			t.Fatal("expected model insertion")
		}
		if !bytes.Equal(got, []byte(want)) {
			t.Fatalf("insert bytes differ\ngot:  %q\nwant: %q", got, want)
		}
	})
}
