package github

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyFilesClassificationCases(t *testing.T) {
	t.Run("classifies and sorts lib dcm and mod paths", func(t *testing.T) {
		calls := 0
		client := contentsClient(t, &calls, `{"sha":"sha","tree":[{"path":"z/old.mod","type":"blob","size":1},{"path":"b/old.dcm","type":"blob","size":2},{"path":"a/old.LIB","type":"blob","size":3},{"path":"new.kicad_sym","type":"blob","size":4}]}`)
		got, err := client.Contents(context.Background(), "owner/repo")
		want := []File{{Path: "a/old.LIB", Size: 3}, {Path: "b/old.dcm", Size: 2}, {Path: "z/old.mod", Size: 1}}
		if err != nil || !reflect.DeepEqual(got.LegacyFiles, want) || got.Legacy != 3 {
			t.Fatalf("legacy files=%+v count=%d err=%v", got.LegacyFiles, got.Legacy, err)
		}
	})
}

func TestDownloadLegacyFilesCases(t *testing.T) {
	t.Run("downloads lib and dcm while skipping mod", func(t *testing.T) {
		root := t.TempDir()
		requested := make([]string, 0)
		client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
			requested = append(requested, request.URL.Path)
			body := "library"
			if strings.HasSuffix(request.URL.Path, ".dcm") {
				body = "documentation"
			}
			return githubResponse(request, http.StatusOK, body, nil), nil
		})})
		contents := Contents{Repo: Repo{FullName: "owner/repo", SHA: "sha"}, LegacyFiles: []File{{Path: "old/part.lib", Size: 7}, {Path: "old/part.dcm", Size: 13}, {Path: "old/part.mod", Size: 8}}, Legacy: 3}
		if err := client.Download(context.Background(), contents, root); err != nil {
			t.Fatal(err)
		}
		wantRequests := []string{"/owner/repo/sha/old/part.lib", "/owner/repo/sha/old/part.dcm"}
		if !reflect.DeepEqual(requested, wantRequests) {
			t.Fatalf("requests=%q", requested)
		}
		lib, libErr := os.ReadFile(filepath.Join(root, "old", "part.lib"))
		dcm, dcmErr := os.ReadFile(filepath.Join(root, "old", "part.dcm"))
		if string(lib) != "library" || string(dcm) != "documentation" || libErr != nil || dcmErr != nil {
			t.Fatalf("lib=%q dcm=%q errors=%v,%v", lib, dcm, libErr, dcmErr)
		}
		if _, err := os.Stat(filepath.Join(root, "old", "part.mod")); !os.IsNotExist(err) {
			t.Fatalf("legacy module was downloaded: %v", err)
		}
	})
}

func TestLegacyLibraryRepoIsImportable(t *testing.T) {
	t.Run("repository containing only lib is not ErrNoKiCadFiles", func(t *testing.T) {
		calls := 0
		client := New(Options{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
			calls++
			if strings.HasSuffix(request.URL.Path, "/git/trees/HEAD") {
				return githubResponse(request, http.StatusOK, `{"sha":"sha","tree":[{"path":"legacy.lib","type":"blob","size":1}]}`, nil), nil
			}
			return githubResponse(request, http.StatusOK, `{"full_name":"owner/repo","html_url":"https://github.com/owner/repo"}`, nil), nil
		})})
		got, err := client.Contents(context.Background(), "owner/repo")
		if err != nil || calls != 2 || got.Legacy != 1 || len(got.LegacyFiles) != 1 || got.LegacyFiles[0].Path != "legacy.lib" {
			t.Fatalf("contents=%+v calls=%d err=%v", got, calls, err)
		}
	})
}
