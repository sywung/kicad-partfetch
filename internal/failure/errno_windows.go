//go:build windows

package failure

import (
	"errors"
	"syscall"
)

func isPermission(err error) bool {
	return errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EPERM)
}

func isDiskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}
