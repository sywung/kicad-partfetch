package library

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func TestExistingArtifactsSkipAndOverwriteWithBackup(t *testing.T) {
	raw, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	component, err := easyeda.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Out: t.TempDir(), Name: "parts"}
	model := map[string][]byte{component.Footprint.Models[0].UUID: []byte("STEP one")}
	first, err := Write(opts, component, model)
	if err != nil {
		t.Fatal(err)
	}
	if first.Symbol != "added" || first.Footprint != "added" || first.Model != "added" {
		t.Fatalf("initial statuses=%+v", first)
	}
	oldSymbol, err := os.ReadFile(first.SymbolPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Write(opts, component, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Symbol != "exists" || second.Footprint != "exists" || second.Model != "exists" {
		t.Fatalf("skip statuses=%+v", second)
	}
	opts.Overwrite = true
	model[component.Footprint.Models[0].UUID] = []byte("STEP two")
	third, err := Write(opts, component, model)
	if err != nil {
		t.Fatal(err)
	}
	if third.Symbol != "updated" || third.Footprint != "updated" || third.Model != "updated" {
		t.Fatalf("overwrite statuses=%+v", third)
	}
	backup, err := os.ReadFile(filepath.Join(opts.Out, "parts.kicad_sym.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, oldSymbol) {
		t.Fatal("symbol backup did not preserve old file bytes")
	}
}

func TestWriteReadonlyDirectoryPreservesPermissionError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only permission behavior is unavailable as root or on Windows")
	}
	raw, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	component, err := easyeda.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.Chmod(out, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(out, 0700)
	_, err = Write(Options{Out: out, Name: "parts"}, component, nil)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Write() error=%v, want fs.ErrPermission", err)
	}
}

func TestListLibraryEntries(t *testing.T) {
	t.Run("missing symbol library returns non-nil empty slice", func(t *testing.T) {
		entries, err := List(Options{Out: t.TempDir(), Name: "demo"})
		if err != nil || entries == nil || len(entries) != 0 {
			t.Fatalf("List()=(%#v, %v), want non-nil empty slice and nil", entries, err)
		}
	})
	t.Run("footprint and model presence flags include true and false", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("WithFiles", "demo:Present", "C10"), testSymbol("WithoutFiles", "demo:Absent", "C11"))
		writeTestFile(t, filepath.Join(out, "demo.pretty", "Present.kicad_mod"), `(footprint "Present" (model "${KIPRJMOD}/case.step"))`)
		writeTestFile(t, filepath.Join(out, "demo.3dshapes", "case.step"), "step")
		entries, err := List(Options{Out: out, Name: "demo"})
		if err != nil {
			t.Fatal(err)
		}
		want := []Entry{{Name: "WithFiles", Footprint: "Present", LCSC: "C10", PartID: "C10", HasFootprint: true, HasModel: true}, {Name: "WithoutFiles", Footprint: "Absent", LCSC: "C11", PartID: "C11"}}
		if !reflect.DeepEqual(entries, want) {
			t.Fatalf("entries=%+v, want %+v", entries, want)
		}
	})
}

func TestEntryPartIDCases(t *testing.T) {
	t.Run("LCSC property is the PartID", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("LCSC", "", "C2040"))
		entries, err := List(Options{Out: out, Name: "demo"})
		if err != nil || !reflect.DeepEqual(entries, []Entry{{Name: "LCSC", LCSC: "C2040", PartID: "C2040"}}) {
			t.Fatalf("entries=%+v error=%v", entries, err)
		}
	})
	t.Run("EasyEDA property is prefixed as a PartID", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testCommunitySymbol("Community", "f2fc22956d5f4a41aa18d1a4337fd37a"))
		entries, err := List(Options{Out: out, Name: "demo"})
		want := []Entry{{Name: "Community", PartID: "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a"}}
		if err != nil || !reflect.DeepEqual(entries, want) {
			t.Fatalf("entries=%+v error=%v, want %+v", entries, err, want)
		}
	})
	t.Run("symbol without a source identifier has no PartID", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("Local", "", ""))
		entries, err := List(Options{Out: out, Name: "demo"})
		if err != nil || !reflect.DeepEqual(entries, []Entry{{Name: "Local"}}) {
			t.Fatalf("entries=%+v error=%v", entries, err)
		}
	})
}

func TestRemoveLibraryEntries(t *testing.T) {
	t.Run("easyeda PartID resolves and removes symbol", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testCommunitySymbol("Community", "f2fc22956d5f4a41aa18d1a4337fd37a"))
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"easyeda:f2fc22956d5f4a41aa18d1a4337fd37a"})
		want := []RemoveResult{{Name: "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", Symbol: "removed", Footprint: "none", Model: "none"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Remove()=(%+v,%v), want %+v", got, err, want)
		}
	})
	t.Run("shared footprint is kept until its last symbol is removed", func(t *testing.T) {
		out := t.TempDir()
		original := writeTestSymbols(t, out, "demo", testSymbol("A", "demo:Shared", "C1"), testSymbol("B", "demo:Shared", "C2"))
		footprint := filepath.Join(out, "demo.pretty", "Shared.kicad_mod")
		step := filepath.Join(out, "demo.3dshapes", "shared.step")
		writeTestFile(t, footprint, `(footprint "Shared" (model "${KIPRJMOD}/shared.step"))`)
		writeTestFile(t, step, "step")
		first, err := Remove(Options{Out: out, Name: "demo"}, []string{"A"})
		if err != nil || !reflect.DeepEqual(first, []RemoveResult{{Name: "A", Symbol: "removed", Footprint: "kept", Model: "kept"}}) {
			t.Fatalf("first remove=(%+v, %v)", first, err)
		}
		if _, err = os.Stat(footprint); err != nil {
			t.Fatalf("shared footprint removed too early: %v", err)
		}
		if _, err = os.Stat(step); err != nil {
			t.Fatalf("shared model removed too early: %v", err)
		}
		second, err := Remove(Options{Out: out, Name: "demo"}, []string{"B"})
		if err != nil || !reflect.DeepEqual(second, []RemoveResult{{Name: "B", Symbol: "removed", Footprint: "removed", Model: "removed"}}) {
			t.Fatalf("second remove=(%+v, %v)", second, err)
		}
		for _, path := range []string{footprint, step} {
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s still exists, stat error=%v", path, err)
			}
		}
		updated, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil || bytes.Equal(updated, original) {
			t.Fatalf("symbol library was not updated: err=%v", err)
		}
	})
	t.Run("different footprints keep a model referenced by a remaining footprint", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("A", "demo:First", "C1"), testSymbol("B", "demo:Second", "C2"))
		first := filepath.Join(out, "demo.pretty", "First.kicad_mod")
		second := filepath.Join(out, "demo.pretty", "Second.kicad_mod")
		step := filepath.Join(out, "demo.3dshapes", "common.step")
		writeTestFile(t, first, `(footprint "First" (model "common.step"))`)
		writeTestFile(t, second, `(footprint "Second" (model "subdir/common.step"))`)
		writeTestFile(t, step, "step")
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"A"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "A", Symbol: "removed", Footprint: "removed", Model: "kept"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
		if _, err = os.Stat(first); !os.IsNotExist(err) {
			t.Errorf("removed footprint remains: %v", err)
		}
		if _, err = os.Stat(step); err != nil {
			t.Errorf("shared model was deleted: %v", err)
		}
		if _, err = os.Stat(second); err != nil {
			t.Errorf("remaining footprint missing: %v", err)
		}
	})
	t.Run("case-insensitive LCSC number resolves symbol", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("Widget", "", "C2040"))
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"c2040"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "c2040", Symbol: "removed", Footprint: "none", Model: "none"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
	})
	t.Run("missing name leaves symbol bytes unchanged and does not create backup", func(t *testing.T) {
		out := t.TempDir()
		original := writeTestSymbols(t, out, "demo", testSymbol("Present", "", "C1"))
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"Absent"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "Absent", Symbol: "missing", Footprint: "none", Model: "none"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
		actual, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
		if err != nil || !bytes.Equal(actual, original) {
			t.Fatalf("symbol bytes changed: err=%v", err)
		}
		if _, err = os.Stat(filepath.Join(out, "demo.kicad_sym.bak")); !os.IsNotExist(err) {
			t.Fatalf("unexpected backup, stat error=%v", err)
		}
	})
	t.Run("external library footprint is kept even if matching local file exists", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("External", "otherlib:X", "C1"))
		local := filepath.Join(out, "demo.pretty", "X.kicad_mod")
		writeTestFile(t, local, `(footprint "X")`)
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"External"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "External", Symbol: "removed", Footprint: "kept", Model: "kept"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
		if _, err = os.Stat(local); err != nil {
			t.Fatalf("external footprint matched local file was removed: %v", err)
		}
	})
	t.Run("model paths only remove same basenames inside library directory", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("Escape", "demo:Escape", "C1"))
		writeTestFile(t, filepath.Join(out, "demo.pretty", "Escape.kicad_mod"), `(footprint "Escape" (model "../../secret.step") (model "C:\\x\\..\\evil.step"))`)
		insideSecret := filepath.Join(out, "demo.3dshapes", "secret.step")
		insideEvil := filepath.Join(out, "demo.3dshapes", "evil.step")
		outsideSecret := filepath.Join(filepath.Dir(out), "secret.step")
		outsideEvil := filepath.Join(filepath.Dir(out), "evil.step")
		for _, path := range []string{insideSecret, insideEvil, outsideSecret, outsideEvil} {
			writeTestFile(t, path, "step")
		}
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"Escape"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "Escape", Symbol: "removed", Footprint: "removed", Model: "removed"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
		for _, path := range []string{insideSecret, insideEvil} {
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("in-library model %s still exists: %v", path, err)
			}
		}
		for _, path := range []string{outsideSecret, outsideEvil} {
			if _, err = os.Stat(path); err != nil {
				t.Errorf("outside model %s was removed: %v", path, err)
			}
		}
	})
	t.Run("backup contains exact bytes from before removal", func(t *testing.T) {
		out := t.TempDir()
		original := writeTestSymbols(t, out, "demo", testSymbol("Delete", "", "C1"))
		if _, err := Remove(Options{Out: out, Name: "demo"}, []string{"Delete"}); err != nil {
			t.Fatal(err)
		}
		backup, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym.bak"))
		if err != nil || !bytes.Equal(backup, original) {
			t.Fatalf("backup=%q err=%v, want original %q", backup, err, original)
		}
	})
	t.Run("duplicate name returns identical outcomes without a second deletion", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("Delete", "", "C1"))
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"Delete", "Delete"})
		want := []RemoveResult{{Name: "Delete", Symbol: "removed", Footprint: "none", Model: "none"}, {Name: "Delete", Symbol: "removed", Footprint: "none", Model: "none"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Remove()=(%+v, %v), want %+v", got, err, want)
		}
	})
	t.Run("footprint file already gone reports missing and none", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("NoFile", "demo:NoFile", "C1"))
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"NoFile"})
		want := []RemoveResult{{Name: "NoFile", Symbol: "removed", Footprint: "missing", Model: "none"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("Remove()=(%+v, %v), want %+v", got, err, want)
		}
	})
	t.Run("matching OBJ file is removed with STEP", func(t *testing.T) {
		out := t.TempDir()
		writeTestSymbols(t, out, "demo", testSymbol("WithOBJ", "demo:WithOBJ", "C1"))
		writeTestFile(t, filepath.Join(out, "demo.pretty", "WithOBJ.kicad_mod"), `(footprint "WithOBJ" (model "C:\\models\\same.step"))`)
		step := filepath.Join(out, "demo.3dshapes", "same.step")
		obj := filepath.Join(out, "demo.3dshapes", "same.obj")
		writeTestFile(t, step, "step")
		writeTestFile(t, obj, "obj")
		got, err := Remove(Options{Out: out, Name: "demo"}, []string{"WithOBJ"})
		if err != nil || !reflect.DeepEqual(got, []RemoveResult{{Name: "WithOBJ", Symbol: "removed", Footprint: "removed", Model: "removed"}}) {
			t.Fatalf("Remove()=(%+v, %v)", got, err)
		}
		for _, path := range []string{step, obj} {
			if _, err = os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("%s remains after removal: %v", path, err)
			}
		}
	})
}

func testSymbol(name, footprint, lcsc string) string {
	return `(symbol "` + name + `" (property "Footprint" "` + footprint + `") (property "LCSC" "` + lcsc + `"))`
}

func testCommunitySymbol(name, uuid string) string {
	return `(symbol "` + name + `" (property "Footprint" "") (property "EasyEDA" "` + uuid + `"))`
}

func writeTestSymbols(t *testing.T, out, name string, symbols ...string) []byte {
	t.Helper()
	var content strings.Builder
	content.WriteString("(kicad_symbol_lib (version 20231120) (generator \"test\")\n")
	for _, symbol := range symbols {
		content.WriteString("  " + symbol + "\n")
	}
	content.WriteString(")\n")
	data := []byte(content.String())
	writeTestFile(t, filepath.Join(out, name+".kicad_sym"), string(data))
	return data
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
