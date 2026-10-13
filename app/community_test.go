package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

type appRoundTripper func(*http.Request) (*http.Response, error)

func (f appRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func appResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}
}

type appFakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *appFakeClock) Now() time.Time { return c.now }

func (c *appFakeClock) Sleep(_ context.Context, duration time.Duration) error {
	c.sleeps = append(c.sleeps, duration)
	c.now = c.now.Add(duration)
	return nil
}

func TestSearchSourceDispatchCases(t *testing.T) {
	t.Run("LCSC source dispatch", testAppSearchLCSCDispatch)
	t.Run("EasyEDA source dispatch", testAppSearchEasyEDADispatch)
	t.Run("invalid source makes no request", testAppSearchRejectsSources)
}

func testAppSearchLCSCDispatch(t *testing.T) {
	client := search.New(search.Options{Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "wmsc.lcsc.com" || r.URL.Path != "/ftps/wm/search/v3/global" {
			t.Fatalf("request URL=%s", r.URL)
		}
		return appResponse(r, http.StatusOK, `{"code":200,"result":{"totalCount":1,"exactMatchResult":[{"productCode":"C1","productModel":"Part","brandNameEn":"Maker","encapStandard":"0603","productIntroEn":"resistor","stockNumber":99}]}}`), nil
	})})
	a := NewWithOptions(Options{Source: &fakeSource{component: testComponent()}, Searcher: client})
	got, err := a.Search("lcsc", "Part", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchItem{{PartID: "C1", MPN: "Part", Manufacturer: "Maker", Package: "0603", Description: "resistor", Stock: 99, Source: "lcsc"}}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items=%+v, want %+v", got.Items, want)
	}
}

func testAppSearchEasyEDADispatch(t *testing.T) {
	client := search.New(search.Options{Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "easyeda.com" || r.URL.Path != "/api/components/search" {
			t.Fatalf("request URL=%s", r.URL)
		}
		return appResponse(r, http.StatusOK, `{"result":{"lists":{"user":[{"uuid":"0123456789abcdef0123456789abcdef","title":"Board","description":"module","owner":{"nickname":"Maker"},"dataStr":{"head":{"c_para":{"package":"Module"}}},"thumb":"//image.easyeda.com/board.png"}]},"facets":{"user":1}}}`), nil
	})})
	a := NewWithOptions(Options{Source: &fakeSource{component: testComponent()}, Searcher: client})
	got, err := a.Search("easyeda", "board", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchItem{{PartID: "easyeda:0123456789abcdef0123456789abcdef", MPN: "Board", Manufacturer: "Maker", Package: "Module", Description: "module", ImageURL: "https://image.easyeda.com/board.png", Source: "easyeda"}}
	if !reflect.DeepEqual(got.Items, want) {
		t.Fatalf("items=%+v, want %+v", got.Items, want)
	}
}

func testAppSearchRejectsSources(t *testing.T) {
	calls := 0
	client := search.New(search.Options{Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return appResponse(r, http.StatusOK, `{"code":200,"result":{"totalCount":0,"exactMatchResult":[]}}`), nil
	})})
	a := NewWithOptions(Options{Searcher: client})
	_, err := a.Search("snapeda", "part", 1)
	if err == nil || err.Error() != `invalid source "snapeda"` || calls != 0 {
		t.Fatalf("error=%v requests=%d", err, calls)
	}
}

func TestPastedMixedPartIDsShortCircuitSearch(t *testing.T) {
	calls := 0
	client := search.New(search.Options{Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return appResponse(r, http.StatusInternalServerError, "unexpected"), nil
	})})
	a := NewWithOptions(Options{Searcher: client})
	got, err := a.Search("lcsc", "c12 easyeda:F2FC22956D5F4A41AA18D1A4337FD37A github:Owner/Repo", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchItem{{PartID: "C12", Source: "lcsc"}, {PartID: "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", Source: "easyeda"}, {PartID: "github:Owner/Repo", Source: "github"}}
	if !reflect.DeepEqual(got.Items, want) || got.Page != 2 || calls != 0 {
		t.Fatalf("result=%+v requests=%d, want %+v and no requests", got, calls, want)
	}
}

func TestCommunitySearchAndFetchSharePerHostThrottle(t *testing.T) {
	fixture, err := os.ReadFile("../internal/source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	clock := &appFakeClock{now: time.Unix(100, 0)}
	calls := 0
	provider := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, CacheDir: t.TempDir(), Clock: clock, Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method == http.MethodPost {
			return appResponse(r, http.StatusOK, `{"result":{"lists":{"user":[]},"facets":{"user":0}}}`), nil
		}
		return appResponse(r, http.StatusOK, string(fixture)), nil
	})})
	a := NewWithOptions(Options{Source: provider})
	if _, err = a.Search("easyeda", "timer", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Preview("easyeda:0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	wantSleeps := []time.Duration{1200 * time.Millisecond}
	if calls != 2 || !reflect.DeepEqual(clock.sleeps, wantSleeps) {
		t.Fatalf("requests=%d sleeps=%v, want 2 requests and %v", calls, clock.sleeps, wantSleeps)
	}
}

func TestCommunityBindingJSONHasNoNullSlices(t *testing.T) {
	client := search.New(search.Options{Transport: appRoundTripper(func(r *http.Request) (*http.Response, error) {
		return appResponse(r, http.StatusInternalServerError, "unexpected"), nil
	})})
	a := NewWithOptions(Options{Source: &fakeSource{component: &model.Component{Value: "Bare", Footprint: model.Footprint{Name: "FP"}}}, Searcher: client})
	preview, err := a.Preview("easyeda:0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	searchResult, err := a.Search("lcsc", "C10", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("Preview arrays are non-null", func(t *testing.T) {
		encoded, marshalErr := json.Marshal(preview)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "null") || !strings.Contains(string(encoded), `"FootprintSVGs":["`) || !strings.Contains(string(encoded), `"Files":[]`) {
			t.Fatalf("Preview JSON has null or missing arrays: %s", encoded)
		}
	})
	t.Run("Search Items is non-null", func(t *testing.T) {
		encoded, marshalErr := json.Marshal(searchResult)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "null") || !strings.Contains(string(encoded), `"Items":[`) {
			t.Fatalf("Search JSON has null or missing Items: %s", encoded)
		}
	})
}
