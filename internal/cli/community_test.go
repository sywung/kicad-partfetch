package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

func TestSearchCommandSourceOutputCases(t *testing.T) {
	t.Run("LCSC text output", testCLISearchLCSCText)
	t.Run("LCSC JSON output", testCLISearchLCSCJSON)
	t.Run("EasyEDA text output", testCLISearchEasyEDAText)
	t.Run("EasyEDA JSON output", testCLISearchEasyEDAJSON)
	t.Run("GitHub text output", testCLISearchGitHubText)
	t.Run("GitHub JSON output", testCLISearchGitHubJSON)
}

func cliSearchClient(t *testing.T, transport testTransport) *search.HTTPClient {
	t.Helper()
	return search.New(search.Options{Transport: transport, Now: func() time.Time { return time.Unix(100, 0) }, Sleep: func(context.Context, time.Duration) error { return nil }})
}

func lcscSearchResponse() string {
	return `{"code":200,"result":{"totalCount":7,"exactMatchResult":[{"productCode":"C42","productModel":"Part","brandNameEn":"Maker","encapStandard":"0603","productIntroEn":"resistor","stockNumber":9}]}}`
}

func communitySearchResponse() string {
	return `{"result":{"lists":{"user":[{"uuid":"0123456789abcdef0123456789abcdef","title":"Board","description":"module","owner":{"nickname":"Maker"},"dataStr":{"head":{"c_para":{"package":"Module"}}},"thumb":"//image.easyeda.com/board.png"}]},"facets":{"user":8}}}`
}

func runCLISearchCase(t *testing.T, args []string, body string, want string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	client := cliSearchClient(t, testTransport(func(r *http.Request) (*http.Response, error) {
		return testResponse(r, body), nil
	}))
	code := runSearch(context.Background(), args, client, &stdout, &stderr)
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func testCLISearchLCSCText(t *testing.T) {
	runCLISearchCase(t, []string{"Part"}, lcscSearchResponse(), "C42\tPart\tMaker\t0603\tresistor\ntotal: 7\n")
}

func testCLISearchLCSCJSON(t *testing.T) {
	runCLISearchCase(t, []string{"--json", "Part"}, lcscSearchResponse(), `{"total":7,"items":[{"partID":"C42","mpn":"Part","manufacturer":"Maker","package":"0603","description":"resistor","stock":9,"price":"","imageURL":"","datasheetURL":"","source":"lcsc","url":"","license":"","stars":0}]}`+"\n")
}

func testCLISearchEasyEDAText(t *testing.T) {
	runCLISearchCase(t, []string{"--source", "easyeda", "board"}, communitySearchResponse(), "easyeda:0123456789abcdef0123456789abcdef\tBoard\tMaker\tModule\tmodule\ntotal: 8\n")
}

func testCLISearchEasyEDAJSON(t *testing.T) {
	runCLISearchCase(t, []string{"--source", "easyeda", "--json", "board"}, communitySearchResponse(), `{"total":8,"items":[{"partID":"easyeda:0123456789abcdef0123456789abcdef","mpn":"Board","manufacturer":"Maker","package":"Module","description":"module","stock":0,"price":"","imageURL":"https://image.easyeda.com/board.png","datasheetURL":"","source":"easyeda","url":"","license":"","stars":0}]}`+"\n")
}

func runCLIGitHubSearch(t *testing.T, args []string, want string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	client := gh.New(gh.Options{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("q") != "board kicad" || r.URL.Query().Get("per_page") != "25" {
			t.Fatalf("query=%v", r.URL.Query())
		}
		return testResponse(r, `{"total_count":1,"items":[{"full_name":"owner/repo","description":"board","html_url":"https://github.com/owner/repo","stargazers_count":12,"license":{"name":"MIT"}}]}`), nil
	})})
	code := runSearchWithGitHub(context.Background(), args, cliSearchClient(t, testTransport(func(r *http.Request) (*http.Response, error) {
		t.Fatal("LCSC client must not be used")
		return nil, nil
	})), client, &stdout, &stderr)
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func testCLISearchGitHubText(t *testing.T) {
	runCLIGitHubSearch(t, []string{"--source", "github", "board"}, "github:owner/repo\towner/repo\towner\t★12\tboard\ntotal: 1\n")
}

func testCLISearchGitHubJSON(t *testing.T) {
	runCLIGitHubSearch(t, []string{"--source", "github", "--page", "3", "--json", "board"}, `{"total":1,"items":[{"partID":"github:owner/repo","mpn":"owner/repo","manufacturer":"owner","package":"","description":"board","stock":0,"price":"","imageURL":"","datasheetURL":"","source":"github","url":"https://github.com/owner/repo","license":"MIT","stars":12}]}`+"\n")
}

func TestAddCommandPartIDCases(t *testing.T) {
	t.Run("LCSC PartID", func(t *testing.T) { testCLIAddPartID(t, "C2040", "/api/products/C2040/components", false) })
	t.Run("EasyEDA PartID", func(t *testing.T) {
		testCLIAddPartID(t, "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", "/api/components/f2fc22956d5f4a41aa18d1a4337fd37a", true)
	})
	t.Run("invalid PartID makes no request", testCLIAddInvalidPartID)
	t.Run("invalid GitHub PartID makes no request", testCLIAddInvalidGitHubPartID)
	t.Run("GitHub PartID imports a repository", testCLIAddGitHubImport)
	t.Run("mixed LCSC EasyEDA and GitHub PartIDs", testCLIAddMixedSources)
}

func testCLIAddPartID(t *testing.T, id, path string, community bool) {
	fixture, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, CacheDir: t.TempDir(), Clock: &instantClock{now: time.Unix(1, 0)}, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.Contains(r.URL.Path, path) {
			t.Fatalf("request URL=%s", r.URL)
		}
		return testResponse(r, string(fixture)), nil
	})})
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"add", "--no-3d", "--out", out, "--lib", "demo", "--json", id}, client, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 || calls != 1 {
		t.Fatalf("exit=%d stderr=%q requests=%d output=%s", code, stderr.String(), calls, stdout.String())
	}
	var got output
	if err = json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Symbol != "added" || got.Footprint != "added" || got.Error != "" {
		t.Fatalf("result=%+v", got)
	}
	symbol, err := os.ReadFile(filepath.Join(out, "demo.kicad_sym"))
	if err != nil {
		t.Fatal(err)
	}
	if community && !strings.Contains(string(symbol), `(property "EasyEDA" "f2fc22956d5f4a41aa18d1a4337fd37a"`) {
		t.Fatalf("EasyEDA property missing: %s", symbol)
	}
}

func testCLIAddInvalidPartID(t *testing.T) {
	calls := 0
	client := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return testResponse(r, "{}"), nil
	})})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"add", "--json", "easyeda:not-a-uuid"}, client, &stdout, &stderr)
	var got output
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if code != 1 || calls != 0 || got.Error != `invalid part ID "easyeda:not-a-uuid"` || stderr.Len() != 0 {
		t.Fatalf("exit=%d requests=%d result=%+v stderr=%q", code, calls, got, stderr.String())
	}
}

func testCLIAddGitHubImport(t *testing.T) {
	calls := 0
	transport := testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return githubCLIResponse(t, r), nil
	})
	client := gh.New(gh.Options{Transport: transport})
	var stdout, stderr bytes.Buffer
	code := RunWithOptions(context.Background(), []string{"add", "--out", t.TempDir(), "--lib", "fixture", "--json", "github:owner/repo"}, nil, Options{GitHub: client}, &stdout, &stderr)
	var got importOutput
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if code != 0 || calls != 3 || got.Source != "github:owner/repo" || len(got.Symbols) != 1 || got.Symbols[0] != (importItemOutput{Name: "Chip", Status: "added"}) || got.Error != "" || stderr.Len() != 0 {
		t.Fatalf("exit=%d requests=%d result=%+v stderr=%q", code, calls, got, stderr.String())
	}
}

func testCLIAddInvalidGitHubPartID(t *testing.T) {
	calls := 0
	client := gh.New(gh.Options{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, nil
	})})
	var stdout, stderr bytes.Buffer
	code := RunWithOptions(context.Background(), []string{"add", "--json", "github:../repo"}, nil, Options{GitHub: client}, &stdout, &stderr)
	var got output
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if code != 1 || calls != 0 || got.Error != `invalid part ID "github:../repo"` || stderr.Len() != 0 {
		t.Fatalf("exit=%d calls=%d result=%+v stderr=%q", code, calls, got, stderr.String())
	}
}

func testCLIAddMixedSources(t *testing.T) {
	fixture, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := testTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.URL.Host == "api.github.com" || request.URL.Host == "raw.githubusercontent.com" {
			return githubCLIResponse(t, request), nil
		}
		return testResponse(request, string(fixture)), nil
	})
	easyClient := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, CacheDir: t.TempDir(), Transport: transport})
	githubClient := gh.New(gh.Options{Transport: transport})
	var stdout, stderr bytes.Buffer
	code := RunWithOptions(context.Background(), []string{"add", "--no-3d", "--out", t.TempDir(), "--lib", "mix", "--json", "C2040", "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a", "github:owner/repo"}, easyClient, Options{GitHub: githubClient}, &stdout, &stderr)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if code != 0 || calls != 5 || len(lines) != 3 || stderr.Len() != 0 {
		t.Fatalf("exit=%d calls=%d lines=%q stderr=%q", code, calls, lines, stderr.String())
	}
	var ghResult importOutput
	if err = json.Unmarshal([]byte(lines[2]), &ghResult); err != nil || ghResult.Source != "github:owner/repo" || len(ghResult.Symbols) != 1 {
		t.Fatalf("GitHub add result=%+v err=%v", ghResult, err)
	}
}

func TestInfoCommandPartIDCases(t *testing.T) {
	t.Run("EasyEDA PartID", testCLIInfoEasyEDAPartID)
	t.Run("GitHub PartID prints repository files", testCLIInfoGitHub)
}

func testCLIInfoEasyEDAPartID(t *testing.T) {
	fixture, err := os.ReadFile("../source/easyeda/testdata/C2040.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := easyeda.NewClient(easyeda.ClientOptions{NoCache: true, Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/components/f2fc22956d5f4a41aa18d1a4337fd37a" {
			t.Fatalf("URL=%s", r.URL)
		}
		return testResponse(r, string(fixture)), nil
	})})
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"info", "easyeda:f2fc22956d5f4a41aa18d1a4337fd37a"}, client, &stdout, &stderr)
	if code != 0 || calls != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Name: ") {
		t.Fatalf("exit=%d requests=%d stdout=%q stderr=%q", code, calls, stdout.String(), stderr.String())
	}
}

func testCLIInfoGitHub(t *testing.T) {
	calls := 0
	client := gh.New(gh.Options{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return githubCLIResponse(t, r), nil
	})})
	var stdout, stderr bytes.Buffer
	code := RunWithOptions(context.Background(), []string{"info", "github:owner/repo"}, nil, Options{GitHub: client}, &stdout, &stderr)
	if code != 0 || calls != 2 || !strings.Contains(stdout.String(), "Repository: owner/repo\n") || !strings.Contains(stdout.String(), "Symbols (1):\n  lib.kicad_sym") || stderr.Len() != 0 {
		t.Fatalf("exit=%d requests=%d stdout=%q stderr=%q", code, calls, stdout.String(), stderr.String())
	}
}

func githubCLIResponse(t *testing.T, request *http.Request) *http.Response {
	t.Helper()
	if request.URL.Host == "api.github.com" && request.URL.Path == "/repos/owner/repo" {
		return testResponse(request, `{"full_name":"owner/repo","description":"board","html_url":"https://github.com/owner/repo","stargazers_count":4,"license":{"name":"MIT"}}`)
	}
	if request.URL.Host == "api.github.com" && request.URL.Path == "/repos/owner/repo/git/trees/HEAD" {
		return testResponse(request, `{"sha":"commit","truncated":false,"tree":[{"path":"lib.kicad_sym","type":"blob","size":53}]}`)
	}
	if request.URL.Host == "raw.githubusercontent.com" {
		return testResponse(request, `(kicad_symbol_lib (version 20231120) (symbol "Chip"))`)
	}
	t.Fatalf("unexpected GitHub request %s", request.URL)
	return nil
}
