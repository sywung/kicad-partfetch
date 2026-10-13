package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

// A repo without KiCad files already has a specific message; it must not be
// flattened into the generic "see the log" sentence.
func TestAppPreviewGitHubWithoutKiCadFilesKeepsSpecificMessage(t *testing.T) {
	logger, buffer := appLogger()
	github := &appFakeGitHub{contents: gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}}, contentsErr: fmt.Errorf("此 repo 只有舊格式檔案: %w", gh.ErrNoKiCadFiles)}
	a := NewWithOptions(Options{GitHub: github, SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})

	_, err := a.Preview("github:owner/repo")

	if err == nil || !strings.Contains(err.Error(), "此 repo 沒有可用的 KiCad 檔案") || !strings.Contains(err.Error(), "舊格式") {
		t.Fatalf("Preview() err=%v", err)
	}
	if strings.Contains(err.Error(), "紀錄檔") {
		t.Fatalf("specific message was replaced by the generic one: %v", err)
	}
	if !strings.Contains(buffer.String(), "op=preview") {
		t.Fatalf("failure was not logged: %s", buffer.String())
	}
}

func TestAppGetSettingsDamagedFileExplainsRecovery(t *testing.T) {
	logger, buffer := appLogger()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewWithOptions(Options{SettingsPath: path, Logger: logger})

	_, err := a.GetSettings()

	if err == nil || err.Error() != "設定檔損毀，已載入預設值；儲存設定可重建檔案" {
		t.Fatalf("GetSettings() err=%v", err)
	}
	if !strings.Contains(buffer.String(), "op=settings") || !strings.Contains(buffer.String(), "settings file is damaged") {
		t.Fatalf("log=%s", buffer.String())
	}
}

// The importer writes its own message when a folder has nothing to import.
func TestAppImportKeepsImporterMessageAndLogsIt(t *testing.T) {
	logger, buffer := appLogger()
	a := NewWithOptions(Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})
	if err := a.SaveSettings(Settings{OutDir: t.TempDir(), LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()

	results, err := a.Import([]string{empty})

	if err != nil || len(results) != 1 {
		t.Fatalf("Import()=%+v err=%v", results, err)
	}
	if results[0].Error != "找不到可匯入的 KiCad 檔案" {
		t.Fatalf("Import() error=%q", results[0].Error)
	}
	if !strings.Contains(buffer.String(), "op=import") {
		t.Fatalf("failure was not logged: %s", buffer.String())
	}
}

// A raw filesystem error from the importer must be classified, not shown as is.
func TestAppImportMissingPathIsClassifiedNotRaw(t *testing.T) {
	logger, buffer := appLogger()
	a := NewWithOptions(Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})
	if err := a.SaveSettings(Settings{OutDir: t.TempDir(), LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "does-not-exist.zip")

	results, err := a.Import([]string{missing})

	if err != nil || len(results) != 1 || results[0].Error == "" {
		t.Fatalf("Import()=%+v err=%v", results, err)
	}
	if strings.Contains(results[0].Error, "no such file") || strings.Contains(results[0].Error, missing) {
		t.Fatalf("raw error leaked to the user: %q", results[0].Error)
	}
	if !strings.Contains(buffer.String(), "does-not-exist.zip") {
		t.Fatalf("underlying error missing from log: %s", buffer.String())
	}
}
