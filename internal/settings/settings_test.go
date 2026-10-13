package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaults(t *testing.T) {
	got := Defaults()
	if got.OutDir == "" || got.LibName != "partfetch" || got.ModelBase != "absolute" || !got.With3D {
		t.Fatalf("defaults=%+v", got)
	}
}
func TestSettingsReadWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "settings.json")
	want := Settings{OutDir: "/tmp/lib", LibName: "parts", ModelBase: "kiprjmod", With3D: false, Overwrite: true}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestKiCadCLISettingsField(t *testing.T) {
	t.Run("configured executable round trips", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		want := Settings{OutDir: "/tmp/lib", LibName: "parts", ModelBase: "absolute", KicadCLI: "/opt/kicad-cli"}
		if err := Save(path, want); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.KicadCLI != want.KicadCLI {
			t.Fatalf("settings=%+v err=%v", got, err)
		}
	})
	t.Run("omitted executable defaults to automatic lookup", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(path, []byte(`{"outDir":"/tmp/lib","libName":"parts"}`), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.KicadCLI != "" {
			t.Fatalf("settings=%+v err=%v", got, err)
		}
	})
}
func TestCorruptSettingsReturnDefaultsAndError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("expected corruption error")
	}
	if !reflect.DeepEqual(got, Defaults()) {
		t.Fatalf("got=%+v", got)
	}
}
