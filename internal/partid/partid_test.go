package partid

import "testing"

func TestParsePartIDFormsAndNormalization(t *testing.T) {
	cases := []struct{ name, input, source, value string }{
		{"LCSC identifier", "C2040", "lcsc", "C2040"},
		{"EasyEDA identifier", "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", "easyeda", "f2fc22956d5f4a41aa18d1a4337fd37a"},
		{"GitHub repository", "github:ccadic/T-Display-S3-Kicad", "github", "ccadic/T-Display-S3-Kicad"},
		{"LCSC lowercase normalized", "c2040", "lcsc", "C2040"},
		{"EasyEDA UUID uppercase normalized", "easyeda:F2FC22956D5F4A41AA18D1A4337FD37A", "easyeda", "f2fc22956d5f4a41aa18d1a4337fd37a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source, value, err := Parse(tc.input)
			if err != nil || source != tc.source || value != tc.value {
				t.Fatalf("Parse(%q)=(%q,%q,%v), want (%q,%q,nil)", tc.input, source, value, err, tc.source, tc.value)
			}
		})
	}
}

func TestParseRejectsInvalidPartIDs(t *testing.T) {
	invalid := []struct{ name, input string }{
		{"empty string", ""},
		{"GitHub owner path traversal", "github:../x"},
		{"GitHub extra path segment", "github:a/b/c"},
		{"EasyEDA UUID wrong length", "easyeda:1234"},
		{"EasyEDA UUID contains non-hex digit", "easyeda:0123456789abcdef0123456789abcdeg"},
		{"whitespace", "C 2040"},
		{"question mark", "github:a/b?x"},
		{"fragment marker", "github:a/b#x"},
		{"percent escape", "github:a/b%2F"},
		{"GitHub dot owner", "github:./repo"},
		{"GitHub dot dot repository", "github:owner/.."},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if source, value, err := Parse(tc.input); err == nil || source != "" || value != "" {
				t.Fatalf("Parse(%q)=(%q,%q,%v), want empty values and an error", tc.input, source, value, err)
			}
		})
	}
}
