package easyeda

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/partid"
)

var (
	ErrNotFound    = errors.New("component not found")
	ErrRateLimited = errors.New("all EasyEDA mirrors rate limited")
	ErrBadData     = errors.New("invalid EasyEDA component data")
)

func IsNotFound(err error) bool {
	var status *statusError
	if !errors.As(err, &status) {
		return false
	}
	return status.status == http.StatusNotFound
}

func IsRateLimited(err error) bool {
	var status *statusError
	if !errors.As(err, &status) {
		return false
	}
	return status.status == http.StatusForbidden || status.status == http.StatusTooManyRequests
}

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type ClientOptions struct {
	Transport http.RoundTripper
	Clock     Clock
	CacheDir  string
	NoCache   bool
}
type Client struct {
	transport http.RoundTripper
	clock     Clock
	cacheDir  string
	noCache   bool
	mu        sync.Mutex
	last      map[string]time.Time
	hostLocks map[string]*sync.Mutex
}

func (c *Client) SetNoCache(disabled bool) { c.mu.Lock(); defer c.mu.Unlock(); c.noCache = disabled }
func (c *Client) cacheDisabled() bool      { c.mu.Lock(); defer c.mu.Unlock(); return c.noCache }

func NewClient(o ClientOptions) *Client {
	if o.Transport == nil {
		o.Transport = http.DefaultTransport
	}
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.CacheDir == "" {
		if dir, e := os.UserCacheDir(); e == nil {
			o.CacheDir = filepath.Join(dir, "kicad-partfetch")
		} else {
			o.NoCache = true
		}
	}
	return &Client{transport: o.Transport, clock: o.Clock, cacheDir: o.CacheDir, noCache: o.NoCache, last: map[string]time.Time{}, hostLocks: map[string]*sync.Mutex{}}
}
func (*Client) ID() string { return "easyeda" }
func (c *Client) Fetch(ctx context.Context, id string) (*model.Component, error) {
	source, value, err := partid.Parse(id)
	if err != nil {
		return nil, err
	}
	if source == "github" {
		return nil, errors.New("GitHub PartID requires the GitHub source client")
	}
	if source == "easyeda" {
		return c.fetchCommunity(ctx, value)
	}
	return c.fetchLCSC(ctx, value)
}

func (c *Client) fetchLCSC(ctx context.Context, id string) (*model.Component, error) {
	cache := filepath.Join(c.cacheDir, "components", id+".json")
	if !c.cacheDisabled() {
		if b, e := os.ReadFile(cache); e == nil {
			if info, ie := os.Stat(cache); ie == nil && c.clock.Now().Sub(info.ModTime()) < 7*24*time.Hour {
				return Parse(b)
			}
		}
	}
	path := fmt.Sprintf("/api/products/%s/components?version=6.4.19.5", id)
	var body []byte
	rateLimited := false
	var lastErr error
	for _, base := range []string{"https://easyeda.com", "https://lceda.cn"} {
		body, lastErr = c.get(ctx, base+path)
		if lastErr == nil {
			break
		}
		var status *statusError
		if errors.As(lastErr, &status) && (status.status == 403 || status.status == 429) {
			rateLimited = true
		}
		if !retryable(lastErr) {
			return nil, lastErr
		}
	}
	if lastErr != nil {
		if rateLimited {
			return nil, fmt.Errorf("%w: %v", ErrRateLimited, lastErr)
		}
		return nil, lastErr
	}
	component, e := Parse(body)
	if e != nil {
		return nil, e
	}
	if !c.cacheDisabled() && c.cacheDir != "" {
		if e = writeCache(cache, body); e != nil {
			return nil, e
		}
	}
	return component, nil
}

func (c *Client) fetchCommunity(ctx context.Context, id string) (*model.Component, error) {
	partID := "easyeda:" + id
	cache := filepath.Join(c.cacheDir, "components", "easyeda-"+id+".json")
	if !c.cacheDisabled() {
		if body, err := os.ReadFile(cache); err == nil {
			if info, statErr := os.Stat(cache); statErr == nil && c.clock.Now().Sub(info.ModTime()) < 7*24*time.Hour {
				component, parseErr := Parse(body)
				if parseErr != nil {
					return nil, parseErr
				}
				component.ID = partID
				return component, nil
			}
		}
	}
	body, err := c.get(ctx, "https://easyeda.com/api/components/"+id+"?version=6.4.19.5")
	if err != nil {
		var status *statusError
		if errors.As(err, &status) && (status.status == http.StatusForbidden || status.status == http.StatusTooManyRequests) {
			return nil, fmt.Errorf("%w: %v", ErrRateLimited, err)
		}
		return nil, err
	}
	component, err := Parse(body)
	if err != nil {
		return nil, err
	}
	component.ID = partID
	if !c.cacheDisabled() && c.cacheDir != "" {
		if err = writeCache(cache, body); err != nil {
			return nil, err
		}
	}
	return component, nil
}
func (c *Client) FetchModel(ctx context.Context, ref model.Model3DRef, format string) ([]byte, error) {
	format = strings.ToLower(strings.TrimPrefix(format, "."))
	if format != "step" && format != "obj" {
		return nil, fmt.Errorf("unsupported model format %q", format)
	}
	if ref.UUID == "" {
		return nil, fmt.Errorf("model UUID is empty")
	}
	cache := filepath.Join(c.cacheDir, "models", ref.UUID+"."+format)
	if !c.cacheDisabled() {
		if b, e := os.ReadFile(cache); e == nil {
			return decompressModel(b)
		}
	}
	path := "https://modules.easyeda.com/qAxj6KHrDKw4blvCG8QJPs7Y/" + ref.UUID
	if format == "obj" {
		path = "https://modules.easyeda.com/3dmodel/" + ref.UUID
	}
	body, e := c.get(ctx, path)
	if e != nil {
		return nil, e
	}
	body, e = decompressModel(body)
	if e != nil {
		return nil, e
	}
	if !c.cacheDisabled() && c.cacheDir != "" {
		if e = writeCache(cache, body); e != nil {
			return nil, e
		}
	}
	return body, nil
}

func (c *Client) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	hostLock := c.hostLock(host)
	hostLock.Lock()
	defer hostLock.Unlock()
	c.mu.Lock()
	last, ok := c.last[host]
	wait := time.Duration(0)
	if ok && c.clock.Now().Sub(last) < 1200*time.Millisecond {
		wait = 1200*time.Millisecond - c.clock.Now().Sub(last)
	}
	c.mu.Unlock()
	if wait > 0 {
		if err := c.clock.Sleep(req.Context(), wait); err != nil {
			return nil, err
		}
	}
	c.mu.Lock()
	c.last[host] = c.clock.Now()
	c.mu.Unlock()
	return c.transport.RoundTrip(req)
}

func decompressModel(data []byte) ([]byte, error) {
	if len(data) < 2 || !bytes.Equal(data[:2], []byte{0x1f, 0x8b}) {
		return data, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if e != nil {
		return nil, e
	}
	resp, e := c.RoundTrip(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	body, e := io.ReadAll(resp.Body)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &statusError{status: resp.StatusCode, body: string(body)}
	}
	return body, nil
}

func (c *Client) hostLock(host string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hostLocks[host] == nil {
		c.hostLocks[host] = &sync.Mutex{}
	}
	return c.hostLocks[host]
}

type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }
func retryable(err error) bool {
	var status *statusError
	if errors.As(err, &status) {
		return status.status == 403 || status.status == 429 || status.status >= 500
	}
	return true
}
func writeCache(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), ".cache-*")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, e = tmp.Write(data); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
