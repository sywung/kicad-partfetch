// Package settings reads and writes the desktop application's user settings.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Settings struct {
	OutDir    string `json:"outDir"`
	LibName   string `json:"libName"`
	ModelBase string `json:"modelBase"`
	With3D    bool   `json:"with3D"`
	Overwrite bool   `json:"overwrite"`
	KicadCLI  string `json:"kicadCLI"`
}

// DamagedError reports a settings file that exists but cannot be parsed.
type DamagedError struct{ Err error }

func (e *DamagedError) Error() string { return "settings file is damaged: " + e.Err.Error() }
func (e *DamagedError) Unwrap() error { return e.Err }

func Defaults() Settings {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return Settings{OutDir: filepath.Join(home, "Documents", "KiCad", "partfetch"), LibName: "partfetch", ModelBase: "absolute", With3D: true}
}
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kicad-partfetch", "settings.json"), nil
}
func Load(path string) (Settings, error) {
	defaults := Defaults()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaults, nil
	}
	if err != nil {
		return defaults, err
	}
	var value Settings
	if err = json.Unmarshal(data, &value); err != nil {
		return defaults, &DamagedError{Err: err}
	}
	if value.OutDir == "" {
		value.OutDir = defaults.OutDir
	}
	if value.LibName == "" {
		value.LibName = defaults.LibName
	}
	if value.ModelBase == "" {
		value.ModelBase = defaults.ModelBase
	}
	return value, nil
}
func Save(path string, value Settings) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
