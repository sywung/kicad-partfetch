package easyeda

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommunityFetchCases(t *testing.T) {
	t.Run("fetch URL, PartID and cache filename", testCommunityFetchAndCache)
	t.Run("403 does not switch to lceda.cn", testCommunityFetchStaysOnEasyEDA)
}

func testCommunityFetchAndCache(t *testing.T) {
	fixture, err := os.ReadFile("testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	const uuid = "f2fc22956d5f4a41aa18d1a4337fd37a"
	dir := t.TempDir()
	calls := 0
	client := NewClient(ClientOptions{CacheDir: dir, Clock: &fakeClock{now: time.Unix(100, 0)}, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://easyeda.com/api/components/"+uuid+"?version=6.4.19.5" {
			t.Fatalf("URL=%s", r.URL)
		}
		return response(r, http.StatusOK, string(fixture)), nil
	})})
	component, err := client.Fetch(context.Background(), "easyeda:"+uuid)
	if err != nil {
		t.Fatal(err)
	}
	if component.ID != "easyeda:"+uuid || calls != 1 {
		t.Fatalf("component ID=%q requests=%d", component.ID, calls)
	}
	cache := filepath.Join(dir, "components", "easyeda-"+uuid+".json")
	if _, err = os.Stat(cache); err != nil {
		t.Fatalf("cache file %s: %v", cache, err)
	}
	cached, err := client.Fetch(context.Background(), "easyeda:"+uuid)
	if err != nil || cached.ID != "easyeda:"+uuid || calls != 1 {
		t.Fatalf("cached component ID=%v requests=%d error=%v", cached, calls, err)
	}
}

func testCommunityFetchStaysOnEasyEDA(t *testing.T) {
	var hosts []string
	client := NewClient(ClientOptions{NoCache: true, Clock: &fakeClock{now: time.Unix(100, 0)}, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hosts = append(hosts, r.URL.Host)
		return response(r, http.StatusForbidden, "blocked"), nil
	})})
	_, err := client.Fetch(context.Background(), "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a")
	if !errors.Is(err, ErrRateLimited) || len(hosts) != 1 || hosts[0] != "easyeda.com" {
		t.Fatalf("error=%v hosts=%v", err, hosts)
	}
}
