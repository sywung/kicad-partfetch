package kicadcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFindPriorityCases(t *testing.T) {
	t.Run("configured path has first priority", testFindConfiguredPriority)
	t.Run("macOS application path precedes PATH", testFindMacApplicationPriority)
	t.Run("PATH precedes Windows versions", testFindLookPathPriority)
	t.Run("Windows picks last sorted version", testFindLatestWindowsVersion)
	t.Run("missing executable returns ErrNotFound", testFindNotFound)
}

func testFindConfiguredPriority(t *testing.T) {
	got, err := FindWith("configured", FindOptions{GOOS: "darwin", Stat: func(path string) error {
		if path != "configured" {
			t.Fatalf("Stat(%q)", path)
		}
		return nil
	}, LookPath: func(string) (string, error) {
		t.Fatal("LookPath should not run")
		return "", nil
	}})
	if err != nil || got != "configured" {
		t.Fatalf("FindWith()=(%q,%v)", got, err)
	}
}

func testFindMacApplicationPriority(t *testing.T) {
	app := "/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli"
	got, err := FindWith("missing", FindOptions{GOOS: "darwin", Stat: func(path string) error {
		if path == app {
			return nil
		}
		return os.ErrNotExist
	}, LookPath: func(string) (string, error) {
		t.Fatal("LookPath must follow the app path")
		return "", nil
	}})
	if err != nil || got != app {
		t.Fatalf("FindWith()=(%q,%v)", got, err)
	}
}

func testFindLookPathPriority(t *testing.T) {
	got, err := FindWith("", FindOptions{GOOS: "windows", Stat: func(string) error { return os.ErrNotExist }, LookPath: func(name string) (string, error) {
		if name != "kicad-cli" {
			t.Fatalf("LookPath(%q)", name)
		}
		return "from-path", nil
	}, Glob: func(string) ([]string, error) {
		t.Fatal("Windows glob must follow PATH")
		return nil, nil
	}})
	if err != nil || got != "from-path" {
		t.Fatalf("FindWith()=(%q,%v)", got, err)
	}
}

func testFindLatestWindowsVersion(t *testing.T) {
	paths := []string{`C:\Program Files\KiCad\8.0\bin\kicad-cli.exe`, `C:\Program Files\KiCad\10.0\bin\kicad-cli.exe`, `C:\Program Files\KiCad\9.0\bin\kicad-cli.exe`}
	want := `C:\Program Files\KiCad\10.0\bin\kicad-cli.exe`
	got, err := FindWith("", FindOptions{GOOS: "windows", Stat: func(string) error { return nil }, LookPath: func(string) (string, error) { return "", os.ErrNotExist }, Glob: func(pattern string) ([]string, error) {
		if pattern != `C:\Program Files\KiCad\*\bin\kicad-cli.exe` {
			t.Fatalf("Glob(%q)", pattern)
		}
		return paths, nil
	}})
	if err != nil || got != want {
		t.Fatalf("FindWith()=(%q,%v), want %q", got, err, want)
	}
}

func testFindNotFound(t *testing.T) {
	_, err := FindWith("", FindOptions{GOOS: "linux", Stat: func(string) error { return os.ErrNotExist }, LookPath: func(string) (string, error) { return "", os.ErrNotExist }})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestSymbolSVGExportArgumentAndTimeoutCases(t *testing.T) {
	t.Run("uses argument array and returns sorted SVGs", testSymbolExportArguments)
	t.Run("honors caller cancellation and removes temporary output", testSymbolExportCancellation)
}

func testSymbolExportArguments(t *testing.T) {
	var captured []string
	var outputDir string
	result, err := SymbolSVGsWithRunner(context.Background(), "fake kicad-cli", "fixture.kicad_sym", func(ctx context.Context, executable string, args ...string) error {
		captured = append([]string{executable}, args...)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("command has no timeout")
		}
		outputDir = args[4]
		if err := os.WriteFile(filepath.Join(outputDir, "z.svg"), []byte("z"), 0600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(outputDir, "a.svg"), []byte("a"), 0600)
	})
	wantPrefix := []string{"fake kicad-cli", "sym", "export", "svg", "-o"}
	if err != nil || !reflect.DeepEqual(captured[:len(wantPrefix)], wantPrefix) || captured[len(captured)-1] != "fixture.kicad_sym" {
		t.Fatalf("argv=%q err=%v", captured, err)
	}
	// The colours must not depend on the theme chosen in the user's own KiCad.
	if theme := captured[len(captured)-3 : len(captured)-1]; !reflect.DeepEqual(theme, []string{"--theme", "_builtin_default"}) {
		t.Fatalf("argv=%q, want the built-in theme pinned", captured)
	}
	if len(result) != 2 || result[0].Name != "a.svg" || result[1].Name != "z.svg" {
		t.Fatalf("SVGs=%+v", result)
	}
	if _, err = os.Stat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains: %v", err)
	}
}

func testSymbolExportCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var outputDir string
	_, err := SymbolSVGsWithRunner(ctx, "fake-cli", "part.kicad_sym", func(ctx context.Context, executable string, args ...string) error {
		outputDir = args[4]
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Fatalf("temporary directory remains: %v", statErr)
	}
}

func TestActualKiCadCLIExportWhenAvailable(t *testing.T) {
	t.Run("actual invocation rejects invalid symbol library", func(t *testing.T) {
		executable, err := Find("")
		if errors.Is(err, ErrNotFound) {
			t.Skip("kicad-cli is not installed")
		}
		if err != nil {
			t.Fatal(err)
		}
		input := filepath.Join(t.TempDir(), "invalid.kicad_sym")
		if err = os.WriteFile(input, []byte("invalid"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err = SymbolSVGs(context.Background(), executable, input)
		if err == nil {
			t.Fatal("invalid symbol library unexpectedly exported")
		}
	})
}

func TestUpgradeSymbolLibraryRunnerCases(t *testing.T) {
	t.Run("writes fixed legacy filenames and passes arguments with a DCM", testUpgradeSymbolLibraryWithDCM)
	t.Run("omits DCM when none is supplied", testUpgradeSymbolLibraryWithoutDCM)
	t.Run("sets a 30 second timeout", testUpgradeSymbolLibraryTimeout)
	t.Run("removes temporary directory after runner failure", testUpgradeSymbolLibraryFailureCleanup)
	t.Run("removes temporary directory after successful conversion", testUpgradeSymbolLibrarySuccessCleanup)
}

func testUpgradeSymbolLibraryWithDCM(t *testing.T) {
	var captured []string
	var temporary string
	got, err := UpgradeSymbolLibraryWithRunner(context.Background(), "fake kicad-cli", []byte("legacy lib"), []byte("legacy docs"), func(ctx context.Context, executable string, args ...string) error {
		captured = append([]string{executable}, args...)
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("runner has no deadline")
		}
		if args[4] != filepath.Join(filepath.Dir(args[4]), "in.lib") {
			t.Fatalf("input filename=%q", args[4])
		}
		temporary = filepath.Dir(args[4])
		if data, readErr := os.ReadFile(filepath.Join(temporary, "in.dcm")); readErr != nil || string(data) != "legacy docs" {
			t.Fatalf("DCM data=%q err=%v", data, readErr)
		}
		if err := os.WriteFile(args[3], []byte("converted"), 0600); err != nil {
			return err
		}
		return nil
	})
	want := []string{"fake kicad-cli", "sym", "upgrade", "-o", filepath.Join(temporary, "out.kicad_sym"), filepath.Join(temporary, "in.lib")}
	if err != nil || string(got) != "converted" || !reflect.DeepEqual(captured, want) {
		t.Fatalf("output=%q argv=%q err=%v", got, captured, err)
	}
}

func testUpgradeSymbolLibraryWithoutDCM(t *testing.T) {
	var arguments []string
	_, err := UpgradeSymbolLibraryWithRunner(context.Background(), "cli", []byte("lib"), nil, func(_ context.Context, _ string, args ...string) error {
		arguments = append([]string(nil), args...)
		return os.WriteFile(args[3], []byte("ok"), 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(arguments) != 5 || filepath.Base(arguments[4]) != "in.lib" {
		t.Fatalf("arguments=%q", arguments)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(arguments[4]), "in.dcm")); !os.IsNotExist(err) {
		t.Fatalf("unexpected DCM file: %v", err)
	}
}

func testUpgradeSymbolLibraryTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var temporary string
	_, err := UpgradeSymbolLibraryWithRunner(ctx, "cli", []byte("lib"), nil, func(ctx context.Context, _ string, args ...string) error {
		temporary = filepath.Dir(args[4])
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(temporary); !os.IsNotExist(statErr) {
		t.Fatalf("temporary directory remains: %v", statErr)
	}
}

func testUpgradeSymbolLibraryFailureCleanup(t *testing.T) {
	var temporary string
	_, err := UpgradeSymbolLibraryWithRunner(context.Background(), "cli", []byte("lib"), nil, func(_ context.Context, _ string, args ...string) error {
		temporary = filepath.Dir(args[4])
		return errors.New("runner failed")
	})
	if err == nil || !strings.Contains(err.Error(), "runner failed") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(temporary); !os.IsNotExist(statErr) {
		t.Fatalf("temporary directory remains: %v", statErr)
	}
}

func testUpgradeSymbolLibrarySuccessCleanup(t *testing.T) {
	var temporary string
	if _, err := UpgradeSymbolLibraryWithRunner(context.Background(), "cli", []byte("lib"), nil, func(_ context.Context, _ string, args ...string) error {
		temporary = filepath.Dir(args[4])
		return os.WriteFile(args[3], []byte("converted"), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary directory remains: %v", err)
	}
}

func TestActualKiCadCLISymbolUpgradeWhenAvailable(t *testing.T) {
	t.Run("legacy symbol library upgrades to a modern library", func(t *testing.T) {
		executable, err := Find("")
		if errors.Is(err, ErrNotFound) {
			t.Skip("kicad-cli is not installed")
		}
		if err != nil {
			t.Fatal(err)
		}
		legacy := []byte("EESchema-LIBRARY Version 2.4\n#encoding utf-8\n#\n# TestPart\n#\nDEF TestPart U 0 40 Y Y 1 F N\nF0 \"U\" 0 100 50 H V C CNN\nF1 \"TestPart\" 0 -100 50 H V C CNN\nDRAW\nC 0 0 50 0 1 10 N\nENDDRAW\nENDDEF\n#\n#End Library\n")
		got, err := UpgradeSymbolLibrary(context.Background(), executable, legacy, nil)
		if err != nil || !strings.Contains(string(got), "(kicad_symbol_lib") {
			t.Fatalf("converted library=%q err=%v", got, err)
		}
	})
}

func TestBoardGLBCases(t *testing.T) {
	t.Run("引數陣列固定且輸入為暫存目錄內的 in.kicad_pcb", func(t *testing.T) {
		var gotArgs []string
		var gotBoard string
		runner := func(_ context.Context, name string, args ...string) error {
			gotArgs = append([]string{name}, args...)
			data, err := os.ReadFile(args[len(args)-1])
			gotBoard = string(data)
			if err != nil {
				return err
			}
			return os.WriteFile(args[len(args)-2], []byte("glTF-bytes"), 0600)
		}
		got, err := BoardGLBWithRunner(context.Background(), "kicad-cli", []byte("(kicad_pcb)"), runner)
		if err != nil || string(got) != "glTF-bytes" || gotBoard != "(kicad_pcb)" {
			t.Fatalf("got=%q board=%q err=%v", got, gotBoard, err)
		}
		dir := filepath.Dir(gotArgs[len(gotArgs)-1])
		want := []string{"kicad-cli", "pcb", "export", "glb", "--subst-models", "--no-board-body", "--force", "-o", filepath.Join(dir, "out.glb"), filepath.Join(dir, "in.kicad_pcb")}
		if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("args=%q, want %q", gotArgs, want)
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("temporary directory remains: %v", statErr)
		}
	})
	t.Run("失敗時回錯誤且暫存目錄已刪", func(t *testing.T) {
		var dir string
		runner := func(_ context.Context, _ string, args ...string) error {
			dir = filepath.Dir(args[len(args)-1])
			return errors.New("boom")
		}
		if _, err := BoardGLBWithRunner(context.Background(), "kicad-cli", []byte("x"), runner); err == nil {
			t.Fatal("expected an error")
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("temporary directory remains: %v", statErr)
		}
	})
	t.Run("沒有 kicad-cli 回 ErrNotFound", func(t *testing.T) {
		if _, err := BoardGLBWithRunner(context.Background(), "", []byte("x"), nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err=%v", err)
		}
	})
}

// Without a layer list kicad-cli plots every layer and through-hole pads take
// the colour of an arbitrary inner copper layer, which the GUI cannot map.
func TestFootprintExportLimitsLayersAndPinsTheme(t *testing.T) {
	var captured []string
	_, err := FootprintSVGsWithRunner(context.Background(), "fake kicad-cli", "parts.pretty", func(_ context.Context, executable string, args ...string) error {
		captured = append([]string{executable}, args...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(captured, " ")
	for _, want := range []string{"fp export svg -o ", " --layers B.Cu,F.Cu,", "F.SilkS", "Edge.Cuts", " --theme _builtin_default parts.pretty"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv=%q, missing %q", captured, want)
		}
	}
	if strings.Contains(joined, "In1.Cu") {
		t.Errorf("argv=%q, inner copper layers must not be plotted", captured)
	}
	if captured[len(captured)-1] != "parts.pretty" {
		t.Errorf("argv=%q, input must be the last argument", captured)
	}
}
