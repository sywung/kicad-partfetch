package easyeda

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}
func response(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}
}

func TestHTTPMirrorRateLimitThrottleAndCache(t *testing.T) {
	fixture, e := os.ReadFile("testdata/C2040.json")
	if e != nil {
		t.Fatal(e)
	}
	t.Run("mirror failover", func(t *testing.T) { testMirrorFailover(t, fixture) })
	t.Run("403 rate limit", testRateLimit)
	t.Run("host throttle", testHostThrottle)
	t.Run("component cache", func(t *testing.T) { testComponentCache(t, fixture) })
}

func testMirrorFailover(t *testing.T, fixture []byte) {
	var hosts []string
	client := NewClient(ClientOptions{NoCache: true, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		if r.URL.Host == "easyeda.com" {
			return response(r, 403, "Request blocked"), nil
		}
		return response(r, 200, string(fixture)), nil
	})})
	component, err := client.Fetch(context.Background(), "c2040")
	if err != nil {
		t.Fatal(err)
	}
	if component.ID != "C2040" || len(hosts) != 2 || hosts[0] != "easyeda.com" || hosts[1] != "lceda.cn" {
		t.Fatalf("component=%v hosts=%v", component, hosts)
	}
}

func testRateLimit(t *testing.T) {
	client := NewClient(ClientOptions{NoCache: true, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { return response(r, 403, "Request blocked"), nil })})
	_, err := client.Fetch(context.Background(), "C2040")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error=%v, want ErrRateLimited", err)
	}
}

func testHostThrottle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	count := 0
	client := NewClient(ClientOptions{NoCache: true, Clock: clock, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) { count++; return response(r, 200, "ok"), nil })})
	for i := 0; i < 2; i++ {
		if _, err := client.get(context.Background(), "https://easyeda.com/test"); err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 || len(clock.sleeps) != 1 || clock.sleeps[0] != 1200*time.Millisecond {
		t.Fatalf("requests=%d sleeps=%v", count, clock.sleeps)
	}
}

func testComponentCache(t *testing.T, fixture []byte) {
	dir := t.TempDir()
	path := filepath.Join(dir, "components", "C2040.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	client := NewClient(ClientOptions{CacheDir: dir, Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})})
	if _, err := client.Fetch(context.Background(), "C2040"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("cache hit issued a request")
	}
}
