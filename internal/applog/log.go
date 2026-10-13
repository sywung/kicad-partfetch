// Package applog configures the application's private append-only log.
package applog

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const MaxFileSize int64 = 1 << 20

func Open(path string) (*slog.Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return discard(), err
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return discard(), err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > MaxFileSize {
		rotated := path + ".1"
		if err = os.Remove(rotated); err != nil && !os.IsNotExist(err) {
			return discard(), err
		}
		if err = os.Rename(path, rotated); err != nil {
			return discard(), err
		}
		if err = os.Chmod(rotated, 0600); err != nil {
			return discard(), err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return discard(), err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return discard(), err
	}
	if err = file.Chmod(0600); err != nil {
		_ = file.Close()
		return discard(), err
	}
	return slog.New(slog.NewTextHandler(file, nil)), nil
}

func Discard() *slog.Logger {
	return discard()
}

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
