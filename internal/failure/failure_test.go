package failure

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"syscall"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type timeoutFailure struct{}

func (timeoutFailure) Error() string {
	return "timed out"
}

func (timeoutFailure) Timeout() bool {
	return true
}

func (timeoutFailure) Temporary() bool {
	return true
}

func TestClassify(t *testing.T) {
	wrapped := func(err error) error {
		return fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err))
	}
	cases := []struct {
		name string
		err  error
		want Kind
	}{
		{name: "nil is unknown", err: nil, want: Unknown},
		{name: "not found", err: easyeda.ErrNotFound, want: NotFound},
		{name: "github not found", err: gh.ErrNotFound, want: NotFound},
		{name: "rate limited easyeda", err: easyeda.ErrRateLimited, want: RateLimited},
		{name: "rate limited github", err: gh.ErrGitHubRateLimited, want: RateLimited},
		{name: "network error interface", err: timeoutFailure{}, want: Network},
		{name: "url error", err: &url.Error{Op: "Get", URL: "https://example.test", Err: timeoutFailure{}}, want: Network},
		{name: "op error", err: &net.OpError{Op: "dial", Net: "tcp", Err: timeoutFailure{}}, want: Network},
		{name: "dns error", err: &net.DNSError{Err: "lookup timed out", Name: "example.test", IsTimeout: true}, want: Network},
		{name: "deadline", err: context.DeadlineExceeded, want: Network},
		{name: "permission sentinel", err: syscall.EACCES, want: Permission},
		{name: "filesystem permission sentinel", err: fs.ErrPermission, want: Permission},
		{name: "permission denied", err: syscall.EPERM, want: Permission},
		{name: "read only filesystem", err: syscall.EROFS, want: Permission},
		{name: "disk full", err: syscall.ENOSPC, want: DiskFull},
		{name: "bad data", err: ErrBadData, want: BadData},
		{name: "easyeda bad data", err: easyeda.ErrBadData, want: BadData},
		{name: "missing tool", err: kicadcli.ErrNotFound, want: ToolMissing},
		{name: "unknown", err: errors.New("other failure"), want: Unknown},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := test.err
			if err != nil {
				err = wrapped(err)
			}
			if got := Classify(err); got != test.want {
				t.Fatalf("Classify()=%q, want %q", got, test.want)
			}
		})
	}
}
