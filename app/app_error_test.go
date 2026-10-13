package app

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type timeoutSourceError struct{}

func (timeoutSourceError) Error() string {
	return "fixture source timeout"
}

func (timeoutSourceError) Timeout() bool {
	return true
}

func (timeoutSourceError) Temporary() bool {
	return true
}

func appLogger() (*slog.Logger, *bytes.Buffer) {
	buffer := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buffer, nil)), buffer
}

func configuredAddApp(t *testing.T, source *fakeSource, logger *slog.Logger, output string) *App {
	t.Helper()
	a := NewWithOptions(Options{Source: source, SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})
	if err := a.SaveSettings(Settings{OutDir: output, LibName: "parts", ModelBase: "absolute", With3D: false}); err != nil {
		t.Fatal(err)
	}
	return a
}

func assertLoggedOnce(t *testing.T, output, op, kind, raw string) {
	t.Helper()
	if strings.Count(output, "msg=\"operation failed\"") != 1 {
		t.Fatalf("log should contain one failure record: %s", output)
	}
	for _, field := range []string{"op=" + op, "kind=" + kind, "err=", raw} {
		if !strings.Contains(output, field) {
			t.Errorf("log=%s, missing %q", output, field)
		}
	}
}

func TestAppAddNotFoundClassifiesAndLogs(t *testing.T) {
	logger, buffer := appLogger()
	a := configuredAddApp(t, &fakeSource{fetchErr: easyeda.ErrNotFound}, logger, t.TempDir())
	results, err := a.Add([]string{"C99999999"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Add()=%+v err=%v", results, err)
	}
	if results[0].ErrorKind != "not_found" || !strings.Contains(results[0].Error, "C99999999") {
		t.Fatalf("Add() result=%+v", results[0])
	}
	assertLoggedOnce(t, buffer.String(), "add", "not_found", "component not found")
}

func TestAppAddReadonlyOutputClassifiesAndLogs(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only permission behavior is unavailable as root or on Windows")
	}
	out := t.TempDir()
	if err := os.Chmod(out, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(out, 0700)
	logger, buffer := appLogger()
	a := configuredAddApp(t, &fakeSource{component: testComponent()}, logger, out)
	results, err := a.Add([]string{"C111"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Add()=%+v err=%v", results, err)
	}
	if results[0].ErrorKind != "permission" || !strings.Contains(results[0].Error, out) {
		t.Fatalf("Add() result=%+v", results[0])
	}
	assertLoggedOnce(t, buffer.String(), "add", "permission", "permission denied")
}

func TestAppAddErrorMessagesAreDistinct(t *testing.T) {
	logger, _ := appLogger()
	missing := configuredAddApp(t, &fakeSource{fetchErr: easyeda.ErrNotFound}, logger, t.TempDir())
	missingResults, err := missing.Add([]string{"C777"})
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		if err := os.Chmod(path, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0700)
	}
	denied := configuredAddApp(t, &fakeSource{component: testComponent()}, logger, path)
	deniedResults, err := denied.Add([]string{"C777"})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("read-only permission behavior is unavailable as root or on Windows")
	}
	if missingResults[0].Error == deniedResults[0].Error || missingResults[0].Error == "加入失敗，請確認零件資料與輸出目錄" || deniedResults[0].Error == "加入失敗，請確認零件資料與輸出目錄" {
		t.Fatalf("errors are not specific: missing=%q denied=%q", missingResults[0].Error, deniedResults[0].Error)
	}
}

func TestAppAddNetworkFailureClassifiesAndLogs(t *testing.T) {
	logger, buffer := appLogger()
	a := configuredAddApp(t, &fakeSource{fetchErr: timeoutSourceError{}}, logger, t.TempDir())
	results, err := a.Add([]string{"C445"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Add()=%+v err=%v", results, err)
	}
	if results[0].ErrorKind != "network" || !strings.Contains(results[0].Error, "LCSC") {
		t.Fatalf("Add() result=%+v", results[0])
	}
	assertLoggedOnce(t, buffer.String(), "add", "network", "fixture source timeout")
}

func TestAppPreviewNotFoundAndNetworkHaveDifferentLoggedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind string
		raw  string
	}{
		{name: "not found", err: easyeda.ErrNotFound, kind: "not_found", raw: "component not found"},
		{name: "network", err: timeoutSourceError{}, kind: "network", raw: "fixture source timeout"},
	}
	var messages []string
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			logger, buffer := appLogger()
			a := NewWithOptions(Options{Source: &fakeSource{fetchErr: test.err}, SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})
			_, err := a.Preview("C789")
			if err == nil {
				t.Fatal("Preview() expected an error")
			}
			messages = append(messages, err.Error())
			assertLoggedOnce(t, buffer.String(), "preview", test.kind, test.raw)
		})
	}
	if len(messages) != 2 || messages[0] == messages[1] {
		t.Fatalf("preview messages are not distinct: %q", messages)
	}
}

func TestAddGitHubRateLimitedUsesChineseMessage(t *testing.T) {
	logger, _ := appLogger()
	repo := gh.Contents{Repo: gh.Repo{FullName: "owner/repo"}}
	client := &appFakeGitHub{contents: repo, contentsErr: gh.ErrGitHubRateLimited}
	a := NewWithOptions(Options{GitHub: client, SettingsPath: filepath.Join(t.TempDir(), "settings.json"), Logger: logger})
	if err := a.SaveSettings(Settings{OutDir: t.TempDir(), LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	results, err := a.Add([]string{"github:owner/repo"})
	if err != nil || len(results) != 1 {
		t.Fatalf("Add()=%+v err=%v", results, err)
	}
	if results[0].ErrorKind != "rate_limited" || strings.Contains(strings.ToLower(results[0].Error), "rate limited") || !strings.Contains(results[0].Error, "來源暫時限制") {
		t.Fatalf("Add() result=%+v", results[0])
	}
}
