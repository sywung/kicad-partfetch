package kicad

import (
	"bytes"
	"testing"
)

func TestRemoveSymbols(t *testing.T) {
	t.Run("delete middle symbol preserves other bytes", func(t *testing.T) {
		input := []byte("(kicad_symbol_lib (version 1)\n  (symbol \"A\")\n  (symbol \"B\")\n  (symbol \"C\")\n)\n")
		want := []byte("(kicad_symbol_lib (version 1)\n  (symbol \"A\")\n  (symbol \"C\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"B"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"B"}) {
			t.Fatalf("RemoveSymbols()=(%q, %v, %v), want (%q, [B], nil)", got, removed, err, want)
		}
	})
	t.Run("delete first symbol", func(t *testing.T) {
		input := []byte("(lib\n  (symbol \"A\")\n  (symbol \"B\")\n)\n")
		want := []byte("(lib\n  (symbol \"B\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"A"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"A"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [A], nil)", got, removed, err, want)
		}
	})
	t.Run("delete last symbol", func(t *testing.T) {
		input := []byte("(lib\n  (symbol \"A\")\n  (symbol \"B\")\n)\n")
		want := []byte("(lib\n  (symbol \"A\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"B"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"B"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [B], nil)", got, removed, err, want)
		}
	})
	t.Run("delete two symbols", func(t *testing.T) {
		input := []byte("(lib\n  (symbol \"A\")\n  (symbol \"B\")\n  (symbol \"C\")\n)\n")
		want := []byte("(lib\n  (symbol \"B\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"A", "C"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"A", "C"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [A C], nil)", got, removed, err, want)
		}
	})
	t.Run("missing symbol leaves input unchanged", func(t *testing.T) {
		input := []byte("(lib (version 1)\n  (symbol \"A\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"missing"})
		if err != nil || !bytes.Equal(got, input) || len(removed) != 0 {
			t.Fatalf("got=(%q, %v, %v), want unchanged input, empty names, nil", got, removed, err)
		}
	})
	t.Run("symbol strings with symbol text and escaped quotes are not mistaken", func(t *testing.T) {
		input := []byte("(lib\n  (symbol \"A\" (property \"Value\" \"(symbol \\\"X\\\" and \\\\\\\"quote\\\\\\\")\"))\n  (symbol \"B\")\n)\n")
		want := []byte("(lib\n  (symbol \"A\" (property \"Value\" \"(symbol \\\"X\\\" and \\\\\\\"quote\\\\\\\")\"))\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"B"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"B"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [B], nil)", got, removed, err, want)
		}
	})
	t.Run("CRLF library preserves remaining bytes", func(t *testing.T) {
		input := []byte("(lib (version 1)\r\n  (symbol \"A\")\r\n  (symbol \"B\")\r\n)\r\n")
		want := []byte("(lib (version 1)\r\n  (symbol \"B\")\r\n)\r\n")
		got, removed, err := RemoveSymbols(input, []string{"A"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"A"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [A], nil)", got, removed, err, want)
		}
	})
	t.Run("nonstandard header and non-symbol expression are preserved", func(t *testing.T) {
		input := []byte("; top comment (symbol \"FAKE\")\n(kicad_symbol_lib (version 20240101) (generator \"other\")\n  ; inside comment (symbol \"ALSO_FAKE\")\n  (custom_header \"keep (text)\")\n  (symbol \"A\")\n)\n")
		want := []byte("; top comment (symbol \"FAKE\")\n(kicad_symbol_lib (version 20240101) (generator \"other\")\n  ; inside comment (symbol \"ALSO_FAKE\")\n  (custom_header \"keep (text)\")\n)\n")
		got, removed, err := RemoveSymbols(input, []string{"A"})
		if err != nil || !bytes.Equal(got, want) || !equalStrings(removed, []string{"A"}) {
			t.Fatalf("got=(%q, %v, %v), want=(%q, [A], nil)", got, removed, err, want)
		}
	})
}

func TestFootprintModelPaths(t *testing.T) {
	t.Run("zero paths returns non-nil empty slice", func(t *testing.T) {
		got := FootprintModelPaths([]byte("(footprint \"x\" (descr \"none\"))"))
		if got == nil || len(got) != 0 {
			t.Fatalf("paths=%#v, want non-nil empty slice", got)
		}
	})
	t.Run("one path", func(t *testing.T) {
		got := FootprintModelPaths([]byte("(footprint \"x\" (model \"${KIPRJMOD}/x.step\"))"))
		if !equalStrings(got, []string{"${KIPRJMOD}/x.step"}) {
			t.Fatalf("paths=%#v", got)
		}
	})
	t.Run("two paths keep appearance order", func(t *testing.T) {
		got := FootprintModelPaths([]byte("(footprint \"x\" (model \"first.step\") (model \"second.step\"))"))
		if !equalStrings(got, []string{"first.step", "second.step"}) {
			t.Fatalf("paths=%#v", got)
		}
	})
	t.Run("escaped backslash is restored", func(t *testing.T) {
		got := FootprintModelPaths([]byte(`(footprint "x" (model "C:\\parts\\x.step"))`))
		if !equalStrings(got, []string{`C:\parts\x.step`}) {
			t.Fatalf("paths=%#v", got)
		}
	})
	t.Run("model-like text inside string is ignored", func(t *testing.T) {
		got := FootprintModelPaths([]byte(`(footprint "x" (descr "text (model \"fake.step\")") (model "real.step"))`))
		if !equalStrings(got, []string{"real.step"}) {
			t.Fatalf("paths=%#v", got)
		}
	})
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
