package applog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenCreatesPrivateAppendLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "partfetch.log")
	logger, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("test entry")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "test entry") {
		t.Fatalf("log contents=%q err=%v", data, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Fatalf("log mode=%#o, want 0600", got)
		}
		directory, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if got := directory.Mode().Perm(); got != 0700 {
			t.Fatalf("log directory mode=%#o, want 0700", got)
		}
	}
}

func TestOpenRotatesOversizedLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partfetch.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(MaxFileSize+1))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil || len(rotated) != int(MaxFileSize+1) {
		t.Fatalf("rotated size=%d err=%v", len(rotated), err)
	}
	current, err := os.Stat(path)
	if err != nil || current.Size() != 0 {
		t.Fatalf("new log stat=%v err=%v", current, err)
	}
}

func TestOpenFailureReturnsDiscardLogger(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	logger, err := Open(filepath.Join(blocker, "partfetch.log"))
	if err == nil || logger == nil {
		t.Fatalf("Open() logger=%v err=%v, want usable logger and error", logger, err)
	}
	logger.Info("discarded safely")
}
