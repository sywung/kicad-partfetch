// Package failure classifies errors for user-facing messages.
package failure

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/url"

	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type Kind string

const (
	NotFound    Kind = "not_found"
	RateLimited Kind = "rate_limited"
	Network     Kind = "network"
	Permission  Kind = "permission"
	DiskFull    Kind = "disk_full"
	BadData     Kind = "bad_data"
	ToolMissing Kind = "tool_missing"
	Unknown     Kind = "unknown"
)

var ErrBadData = errors.New("invalid source data")

func Classify(err error) Kind {
	if err == nil {
		return Unknown
	}
	switch {
	case errors.Is(err, easyeda.ErrNotFound), easyeda.IsNotFound(err), gh.IsNotFound(err), search.IsNotFound(err):
		return NotFound
	case errors.Is(err, easyeda.ErrRateLimited), errors.Is(err, gh.ErrGitHubRateLimited), easyeda.IsRateLimited(err), gh.IsRateLimited(err), search.IsRateLimited(err):
		return RateLimited
	case errors.Is(err, kicadcli.ErrNotFound):
		return ToolMissing
	case errors.Is(err, ErrBadData), errors.Is(err, easyeda.ErrBadData):
		return BadData
	case isDiskFull(err):
		return DiskFull
	case errors.Is(err, fs.ErrPermission), isPermission(err):
		return Permission
	case errors.Is(err, context.DeadlineExceeded), isNetwork(err):
		return Network
	default:
		return Unknown
	}
}

func isNetwork(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}
