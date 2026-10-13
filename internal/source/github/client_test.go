package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRoundTripper func(*http.Request) (*http.Response, error)

func (f fakeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func githubResponse(request *http.Request, status int, body string, headers http.Header) *http.Response {
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestSearchQueryAndPageCases(t *testing.T) {
	t.Run("automatically appends kicad", testSearchAddsKiCad)
	t.Run("does not append duplicate kicad", testSearchKeepsKiCadOnce)
	t.Run("sends requested page", testSearchSendsPage)
}

func testSearchAddsKiCad(t *testing.T) {
	client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("User-Agent") != "kicad-partfetch" {
			t.Fatalf("headers=%v", request.Header)
		}
		if request.URL.Query().Get("q") != "t-display kicad" {
			t.Fatalf("query=%q", request.URL.Query().Get("q"))
		}
		return githubResponse(request, 200, `{"total_count":0,"items":[]}`, nil), nil
	})})
	if _, _, err := client.Search(context.Background(), "t-display", 1); err != nil {
		t.Fatal(err)
	}
}

func testSearchKeepsKiCadOnce(t *testing.T) {
	client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("q") != "KiCad library" {
			t.Fatalf("query=%q", request.URL.Query().Get("q"))
		}
		return githubResponse(request, 200, `{"total_count":1,"items":[{"full_name":"owner/repo","description":"parts","html_url":"https://github.com/owner/repo","stargazers_count":12,"license":{"name":"MIT"}}]}`, nil), nil
	})})
	repos, total, err := client.Search(context.Background(), "KiCad library", 1)
	if err != nil || total != 1 || !reflect.DeepEqual(repos, []Repo{{FullName: "owner/repo", Description: "parts", License: "MIT", URL: "https://github.com/owner/repo", Stars: 12}}) {
		t.Fatalf("repos=%+v total=%d err=%v", repos, total, err)
	}
}

func testSearchSendsPage(t *testing.T) {
	client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("page") != "3" || request.URL.Query().Get("per_page") != "25" {
			t.Fatalf("query=%v", request.URL.Query())
		}
		return githubResponse(request, 200, `{"total_count":0,"items":[]}`, nil), nil
	})})
	if _, _, err := client.Search(context.Background(), "part kicad", 3); err != nil {
		t.Fatal(err)
	}
}

func TestContentsClassificationAndCacheCases(t *testing.T) {
	t.Run("classifies and sorts tree entries and keeps truncated", testContentsClassifiesAndSorts)
	t.Run("second lookup uses in-memory cache", testContentsCachesForTenMinutes)
	t.Run("no compatible KiCad files returns specified error", testContentsNoKiCadFiles)
	t.Run("legacy-only repo reports old format", testContentsLegacyOnly)
	t.Run("truncated empty tree is still oversized", testContentsTruncatedEmptyTree)
}

func testContentsNoKiCadFiles(t *testing.T) {
	calls := 0
	client := contentsClient(t, &calls, `{"sha":"sha","tree":[{"path":"README.md","type":"blob","size":1}]}`)
	_, err := client.Contents(context.Background(), "owner/repo")
	if !errors.Is(err, ErrNoKiCadFiles) || strings.Contains(err.Error(), "舊格式") {
		t.Fatalf("error=%v", err)
	}
}

func testContentsLegacyOnly(t *testing.T) {
	calls := 0
	// SPEC-IMPORT 9.3: a .lib can be converted, so only a .mod-only repo is "legacy only".
	client := contentsClient(t, &calls, `{"sha":"sha","tree":[{"path":"old.mod","type":"blob","size":1}]}`)
	_, err := client.Contents(context.Background(), "owner/repo")
	if !errors.Is(err, ErrNoKiCadFiles) || !strings.Contains(err.Error(), "只有舊格式") {
		t.Fatalf("error=%v", err)
	}
}

func testContentsTruncatedEmptyTree(t *testing.T) {
	calls := 0
	client := contentsClient(t, &calls, `{"sha":"sha","truncated":true,"tree":[]}`)
	_, err := client.Contents(context.Background(), "owner/repo")
	if !errors.Is(err, ErrRepoTooLarge) {
		t.Fatalf("error=%v", err)
	}
}

func testContentsClassifiesAndSorts(t *testing.T) {
	calls := 0
	client := contentsClient(t, &calls, `{"sha":"tree-sha","truncated":true,"tree":[{"path":"z.kicad_sym","type":"blob","size":2},{"path":"A.KICAD_MOD","type":"blob","size":3},{"path":"x.STEP","type":"blob","size":4},{"path":"old.mod","type":"blob","size":1},{"path":"folder","type":"tree","size":0},{"path":"a.kicad_sym","type":"blob","size":1}]}`)
	got, err := client.Contents(context.Background(), "owner/repo")
	if !errors.Is(err, ErrRepoTooLarge) || got.Repo.SHA != "tree-sha" || !got.Truncated || got.Legacy != 1 || len(got.Symbols) != 2 || got.Symbols[0].Path != "a.kicad_sym" || got.Footprints[0].Path != "A.KICAD_MOD" || got.Models[0].Path != "x.STEP" {
		t.Fatalf("contents=%+v err=%v", got, err)
	}
}

func testContentsCachesForTenMinutes(t *testing.T) {
	calls := 0
	client := contentsClient(t, &calls, `{"sha":"abc","truncated":false,"tree":[{"path":"part.kicad_sym","type":"blob","size":1}]}`)
	if _, err := client.Contents(context.Background(), "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Contents(context.Background(), "owner/repo"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("HTTP calls=%d, want repo and tree only once", calls)
	}
}

func contentsClient(t *testing.T, calls *int, tree string) *Client {
	t.Helper()
	return New(Options{Now: func() time.Time { return time.Unix(100, 0) }, Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		*calls++
		if strings.HasSuffix(request.URL.Path, "/git/trees/HEAD") {
			return githubResponse(request, 200, tree, nil), nil
		}
		return githubResponse(request, 200, `{"full_name":"owner/repo","description":"description","html_url":"https://github.com/owner/repo","stargazers_count":4,"license":{"name":"MIT"}}`, nil), nil
	})})
}

func TestDownloadSafetyAndLimitCases(t *testing.T) {
	t.Run("preserves repository relative paths", testDownloadPreservesRelativePaths)
	t.Run("skips traversal and absolute paths", testDownloadSkipsUnsafePaths)
	t.Run("refuses symlink path components", testDownloadRefusesSymlinkEscape)
	t.Run("rejects more than 200 files without residue", testDownloadRejectsFileCount)
	t.Run("rejects declared aggregate size without residue", testDownloadRejectsAggregateSize)
	t.Run("rejects declared file size over limit without residue", testDownloadRejectsDeclaredFileSize)
	t.Run("rejects actual bytes beyond declared size without residue", testDownloadRejectsActualFileSize)
}

func testDownloadRejectsAggregateSize(t *testing.T) {
	root := t.TempDir()
	files := []File{{Path: "a.kicad_sym", Size: maxFileSize}, {Path: "b.kicad_sym", Size: maxFileSize}, {Path: "c.kicad_sym", Size: maxFileSize}, {Path: "d.kicad_sym", Size: maxFileSize}, {Path: "e.kicad_sym", Size: maxFileSize}}
	client := downloadClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("aggregate limit must be checked before download")
		return nil, nil
	})
	err := client.Download(context.Background(), Contents{Repo: Repo{FullName: "owner/repo"}, Symbols: files}, root)
	assertNoPartialDownload(t, root, err)
}

func testDownloadPreservesRelativePaths(t *testing.T) {
	root := t.TempDir()
	client := downloadClient(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://raw.githubusercontent.com/owner/repo/commit-a/nested/part.kicad_sym" {
			t.Fatalf("URL=%s", request.URL)
		}
		return githubResponse(request, 200, "symbol", nil), nil
	})
	contents := Contents{Repo: Repo{FullName: "owner/repo", SHA: "commit-a"}, Symbols: []File{{Path: "nested/part.kicad_sym", Size: 6}}}
	if err := client.Download(context.Background(), contents, root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "nested", "part.kicad_sym"))
	if err != nil || string(got) != "symbol" {
		t.Fatalf("data=%q err=%v", got, err)
	}
}

func testDownloadSkipsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	client := downloadClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe path must not be requested")
		return nil, nil
	})
	contents := Contents{Repo: Repo{FullName: "owner/repo", SHA: "sha"}, Symbols: []File{{Path: "../escape.kicad_sym", Size: 1}, {Path: "/absolute.kicad_sym", Size: 1}, {Path: `C:\outside.kicad_sym`, Size: 1}}}
	if err := client.Download(context.Background(), contents, root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func testDownloadRefusesSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}
	client := downloadClient(func(request *http.Request) (*http.Response, error) {
		return githubResponse(request, 200, "ok", nil), nil
	})
	contents := Contents{Repo: Repo{FullName: "owner/repo", SHA: "sha"}, Symbols: []File{{Path: "nested/part.kicad_sym", Size: 2}}}
	err := client.Download(context.Background(), contents, root)
	if err == nil {
		t.Fatal("symlink target was accepted")
	}
	entries, readErr := os.ReadDir(outside)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("outside directory was modified: %v, %v", entries, readErr)
	}
}

func testDownloadRejectsFileCount(t *testing.T) {
	root := t.TempDir()
	files := make([]File, 201)
	for i := range files {
		files[i] = File{Path: strings.Repeat("a", i+1) + ".kicad_sym", Size: 0}
	}
	client := downloadClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("limit must be checked before download")
		return nil, nil
	})
	err := client.Download(context.Background(), Contents{Repo: Repo{FullName: "owner/repo"}, Symbols: files}, root)
	want := "此 repo 的 KiCad 檔案過多（201 個），請自行下載後用『匯入資料夾…』挑選"
	if err == nil || err.Error() != want {
		t.Fatalf("error=%v, want %q", err, want)
	}
	assertNoPartialDownload(t, root, err)
}

func testDownloadRejectsDeclaredFileSize(t *testing.T) {
	root := t.TempDir()
	client := downloadClient(func(*http.Request) (*http.Response, error) {
		t.Fatal("limit must be checked before download")
		return nil, nil
	})
	err := client.Download(context.Background(), Contents{Repo: Repo{FullName: "owner/repo"}, Symbols: []File{{Path: "large.kicad_sym", Size: maxFileSize + 1}}}, root)
	assertNoPartialDownload(t, root, err)
}

func testDownloadRejectsActualFileSize(t *testing.T) {
	root := t.TempDir()
	client := downloadClient(func(request *http.Request) (*http.Response, error) {
		return githubResponse(request, 200, "actual bytes", nil), nil
	})
	err := client.Download(context.Background(), Contents{Repo: Repo{FullName: "owner/repo", SHA: "sha"}, Symbols: []File{{Path: "part.kicad_sym", Size: 2}}}, root)
	assertNoPartialDownload(t, root, err)
}

func downloadClient(roundTrip fakeRoundTripper) *Client { return New(Options{Transport: roundTrip}) }

func assertNoPartialDownload(t *testing.T, root string, err error) {
	t.Helper()
	if !errors.Is(err, ErrRepoTooLarge) {
		t.Fatalf("error=%v, want ErrRepoTooLarge", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("partial files remain: entries=%v err=%v", entries, readErr)
	}
}

func TestRateLimitAndTokenHeaderCases(t *testing.T) {
	t.Run("rate limit error includes local reset time", testRateLimitHasResetTime)
	t.Run("HTTP 429 rate limit error is typed", testRateLimitTooManyRequests)
	t.Run("token is sent only when configured", testGitHubTokenHeader)
}

func testRateLimitTooManyRequests(t *testing.T) {
	client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Reset", "200")
		return githubResponse(request, http.StatusTooManyRequests, "", header), nil
	})})
	_, _, err := client.Search(context.Background(), "x", 1)
	if !errors.Is(err, ErrGitHubRateLimited) {
		t.Fatalf("error=%v", err)
	}
}

func testRateLimitHasResetTime(t *testing.T) {
	reset := time.Unix(200, 0)
	client := New(Options{Now: func() time.Time { return time.Unix(100, 0) }, Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
		header := make(http.Header)
		header.Set("X-RateLimit-Remaining", "0")
		header.Set("X-RateLimit-Reset", "200")
		return githubResponse(request, http.StatusForbidden, "never surfaced", header), nil
	})})
	_, _, err := client.Search(context.Background(), "x", 1)
	if !errors.Is(err, ErrGitHubRateLimited) || !strings.Contains(err.Error(), reset.Local().Format("2006-01-02 15:04:05")) || strings.Contains(err.Error(), "never surfaced") {
		t.Fatalf("error=%v", err)
	}
}

func testGitHubTokenHeader(t *testing.T) {
	t.Run("configured token", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "fixture-secret")
		client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "Bearer fixture-secret" {
				t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
			}
			return githubResponse(request, 200, `{"total_count":0,"items":[]}`, nil), nil
		})})
		if _, _, err := client.Search(context.Background(), "x", 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("no token", func(t *testing.T) {
		t.Setenv("GITHUB_TOKEN", "")
		client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Authorization") != "" {
				t.Fatalf("unexpected authorization header")
			}
			return githubResponse(request, 200, `{"total_count":0,"items":[]}`, nil), nil
		})})
		if _, _, err := client.Search(context.Background(), "x", 1); err != nil {
			t.Fatal(err)
		}
	})
}
