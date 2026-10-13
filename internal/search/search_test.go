package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, e := os.ReadFile("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestSearchExactMatchFixture(t *testing.T) {
	calls := 0
	client := New(Options{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/ftps/wm/search/v3/global" || r.Method != "POST" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		for k, want := range map[string]string{"Origin": "https://www.lcsc.com", "Referer": "https://www.lcsc.com/", "Accept-Language": "en-US,en;q=0.9"} {
			if got := r.Header.Get(k); got != want {
				t.Errorf("%s=%q, want %q", k, got, want)
			}
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Fatal(e)
		}
		if body["keyword"] != "STM32F103C8T6" || body["pageSize"] != float64(25) {
			t.Errorf("body=%v", body)
		}
		return response(r, 200, fixture(t, "global_exact.json")), nil
	})})
	got, err := client.Search(context.Background(), "STM32F103C8T6", 1)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(got.Items) == 0 {
		t.Fatalf("calls=%d result=%+v", calls, got)
	}
	if got.Items[0].PartID != "C8734" || got.Items[0].Price != "$1.5012" {
		t.Fatalf("first item=%+v", got.Items[0])
	}
}

func TestSearchProductListAndCategoryFixtures(t *testing.T) {
	t.Run("product list", func(t *testing.T) {
		fixture := `{"code":200,"result":{"totalCount":3,"exactMatchResult":null,"productSearchResultVO":{"productList":[{"productCode":"C42","productModel":"M","productPriceList":[{"ladder":100,"usdPrice":0.15},{"ladder":1,"usdPrice":0.25}]}]}}}`
		client := New(Options{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return response(r, 200, fixture), nil })})
		got, err := client.Search(context.Background(), "M", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Items) != 1 || got.Total != 3 || got.Items[0].Price != "$0.2500" {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("category", func(t *testing.T) {
		calls := 0
		clock := &fakeClock{now: time.Unix(10, 0)}
		client := New(Options{Now: clock.Now, Sleep: clock.Sleep, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return response(r, 200, fixture(t, "global_category.json")), nil
			}
			if r.URL.Path != "/ftps/wm/product/query/list" {
				t.Fatalf("path=%s", r.URL.Path)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"keyword", "globalKeyword", "catalogIdList", "brandIdList", "encapValueList", "isStock", "isOtherSuppliers", "isAsianBrand", "isDeals", "isRohsCert", "paramNameValueMap", "currentPage", "pageSize", "scene"} {
				if _, ok := body[key]; !ok {
					t.Errorf("missing required field %s", key)
				}
			}
			if body["scene"] != "FULL_MATCH" || body["currentPage"] != float64(3) {
				t.Errorf("body=%v", body)
			}
			if got := body["catalogIdList"].([]any); len(got) != 1 || got[0] != float64(1032) {
				t.Errorf("selected catalog=%v, want 1032", body["catalogIdList"])
			}
			return response(r, 200, fixture(t, "query_list.json")), nil
		})})
		got, err := client.Search(context.Background(), "AMS1117", 3)
		if err != nil {
			t.Fatal(err)
		}
		if calls != 2 || got.Total != 240 || len(got.Items) == 0 || got.Items[0].PartID != "C6186" {
			t.Fatalf("calls=%d result=%+v", calls, got)
		}
	})
}

func TestSearchCategory405(t *testing.T) {
	calls := 0
	clock := &fakeClock{now: time.Unix(20, 0)}
	client := New(Options{Now: clock.Now, Sleep: clock.Sleep, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, 200, fixture(t, "global_category.json")), nil
		}
		return response(r, 200, `{"code":405,"msg":"Invalid field"}`), nil
	})})
	if _, err := client.Search(context.Background(), "AMS1117", 1); err == nil || !strings.Contains(err.Error(), "405") {
		t.Fatalf("err=%v", err)
	}
}

func TestSearchThrottlesRequests(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	client := New(Options{Now: clock.Now, Sleep: clock.Sleep, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `{"code":200,"result":{"totalCount":0,"exactMatchResult":[]}}`), nil
	})})
	for i := 0; i < 2; i++ {
		if _, err := client.Search(context.Background(), "none", 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.sleeps) != 1 || clock.sleeps[0] != time.Second {
		t.Fatalf("sleeps=%v", clock.sleeps)
	}
}

// Shape of the real "esp32" response: no products, only a category tree whose
// largest node is a parent. Querying a parent ID returns zero rows upstream.
func TestSearchQueriesLeafCategoriesNotParents(t *testing.T) {
	global := `{"code":200,"result":{"totalCount":309,"scene":"FULL_MATCH","catalogVOS":[
		{"catalogId":35,"productNum":227,"level":1,"childCatelogs":[{"catalogId":560,"productNum":212,"level":2},{"catalogId":559,"productNum":15,"level":2}]},
		{"catalogId":11,"productNum":61,"level":1,"childCatelogs":[{"catalogId":180,"productNum":54,"level":2,"childCatelogs":[{"catalogId":854,"productNum":41,"level":3}]}]}],
		"topResults":[{"catalogId":560,"productNum":212,"level":2},{"catalogId":941,"productNum":9,"level":3}]}}`
	calls := 0
	clock := &fakeClock{now: time.Unix(10, 0)}
	client := New(Options{Now: clock.Now, Sleep: clock.Sleep, Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, 200, global), nil
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(body["catalogIdList"])
		if want := "[560,559,854,941]"; string(got) != want {
			t.Errorf("catalogIdList=%s, want %s", got, want)
		}
		return response(r, 200, `{"code":200,"result":{"totalRow":290,"dataList":[{"productCode":"C82899","productModel":"ESP32-WROOM-32E-N4"}]}}`), nil
	})})
	got, err := client.Search(context.Background(), "esp32", 1)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || got.Total != 290 || len(got.Items) != 1 || got.Items[0].PartID != "C82899" {
		t.Fatalf("calls=%d result=%+v", calls, got)
	}
}

func TestCommunitySearchCases(t *testing.T) {
	t.Run("form fields and product mapping", testCommunityFormAndMapping)
	t.Run("owner username fallback", testCommunityUsernameFallback)
	t.Run("403 maps to rate limit", testCommunityForbiddenRateLimit)
}

func testCommunityFormAndMapping(t *testing.T) {
	transport := roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Host != "easyeda.com" || r.URL.Path != "/api/components/search" {
			t.Fatalf("request=%s %s", r.Method, r.URL)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("Content-Type=%q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		want := url.Values{"type": {"3"}, "doctype[]": {"2"}, "returnListStyle": {"classifyarr"}, "uid": {"user"}, "wd": {"timer module"}, "page": {"3"}, "pageSize": {"25"}}
		if !reflect.DeepEqual(form, want) {
			t.Fatalf("form=%v, want %v", form, want)
		}
		return response(r, 200, `{"result":{"lists":{"user":[{"uuid":"F2FC22956D5F4A41AA18D1A4337FD37A","title":"Timer Module","description":"community timer","owner":{"nickname":"Ada"},"dataStr":{"head":{"c_para":{"package":"SOT-23"}}},"thumb":"//image.easyeda.com/components/timer.png"}]},"facets":{"user":41}}}`), nil
	})
	client := New(Options{Transport: transport})
	got, err := client.Community(context.Background(), "timer module", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Items: []Item{{PartID: "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", MPN: "Timer Module", Manufacturer: "Ada", Package: "SOT-23", Description: "community timer", ImageURL: "https://image.easyeda.com/components/timer.png", Source: "easyeda"}}, Total: 41, Page: 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Community()=%+v, want %+v", got, want)
	}
}

func testCommunityUsernameFallback(t *testing.T) {
	client := New(Options{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return response(r, 200, `{"result":{"lists":{"user":[{"uuid":"0123456789abcdef0123456789abcdef","title":"Board","owner":{"username":"maker"},"dataStr":{"head":{"c_para":{"package":"Module"}}},"thumb":"/board.png"}]},"facets":{"user":1}}}`), nil
	})})
	got, err := client.Community(context.Background(), "board", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Manufacturer != "maker" || got.Items[0].ImageURL != "/board.png" || got.Total != 1 {
		t.Fatalf("result=%+v", got)
	}
}

func testCommunityForbiddenRateLimit(t *testing.T) {
	calls := 0
	client := New(Options{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, http.StatusForbidden, "blocked"), nil
	})})
	_, err := client.Community(context.Background(), "module", 1)
	if !errors.Is(err, easyeda.ErrRateLimited) || calls != 1 {
		t.Fatalf("error=%v requests=%d, want rate limit and one request", err, calls)
	}
}
