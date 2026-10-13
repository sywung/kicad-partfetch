// Package github searches GitHub repositories and downloads their KiCad files.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sywung/kicad-partfetch/internal/partid"
)

const (
	apiBase       = "https://api.github.com"
	rawBase       = "https://raw.githubusercontent.com"
	maxFiles      = 200
	maxFileSize   = int64(64 << 20)
	maxTotalBytes = int64(256 << 20)
)

var (
	ErrGitHubRateLimited = errors.New("GitHub API rate limited")
	ErrNotFound          = errors.New("GitHub repository not found")
	ErrRepoTooLarge      = errors.New("GitHub repository exceeds KiCad file limits")
	ErrNoKiCadFiles      = errors.New("此 repo 沒有可用的 KiCad 檔案")
)

func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func IsRateLimited(err error) bool {
	return errors.Is(err, ErrGitHubRateLimited)
}

type repoLimitError struct{ count int }

func (e repoLimitError) Error() string {
	return fmt.Sprintf("此 repo 的 KiCad 檔案過多（%d 個），請自行下載後用『匯入資料夾…』挑選", e.count)
}
func (e repoLimitError) Unwrap() error { return ErrRepoTooLarge }

// UserMessage marks the error text as written for the user.
func (e repoLimitError) UserMessage() string { return e.Error() }

type Repo struct {
	FullName, Description, License, URL, SHA string
	Stars                                    int
}

type File struct {
	Path string
	Size int64
}

type Contents struct {
	Repo                        Repo
	Symbols, Footprints, Models []File
	LegacyFiles                 []File
	Legacy                      int
	Truncated                   bool
}

type Options struct {
	HTTPClient *http.Client
	Transport  http.RoundTripper
	Token      string
	Now        func() time.Time
}

type Client struct {
	http  *http.Client
	token string
	now   func() time.Time
	mu    sync.Mutex
	cache map[string]cachedContents
}

type cachedContents struct {
	value   Contents
	expires time.Time
}

func New(o Options) *Client {
	client := o.HTTPClient
	if client == nil {
		transport := o.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		client = &http.Client{Transport: transport, Timeout: 30 * time.Second}
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Client{http: client, token: o.Token, now: now, cache: make(map[string]cachedContents)}
}

func (c *Client) Search(ctx context.Context, query string, page int) ([]Repo, int, error) {
	if page < 1 {
		page = 1
	}
	if !strings.Contains(strings.ToLower(query), "kicad") {
		query += " kicad"
	}
	target := apiBase + "/search/repositories?" + url.Values{"q": {query}, "per_page": {"25"}, "page": {fmt.Sprint(page)}}.Encode()
	var response searchResponse
	if err := c.getJSON(ctx, target, &response); err != nil {
		return nil, 0, err
	}
	repos := make([]Repo, 0, len(response.Items))
	for _, item := range response.Items {
		_, fullName, parseErr := partid.Parse("github:" + item.FullName)
		if parseErr != nil {
			return nil, 0, errors.New("GitHub returned an invalid repository identifier")
		}
		license := ""
		if item.License != nil {
			license = item.License.Name
		}
		repos = append(repos, Repo{FullName: fullName, Description: item.Description, License: license, URL: item.HTMLURL, Stars: item.StargazersCount})
	}
	return repos, response.TotalCount, nil
}

func (c *Client) Contents(ctx context.Context, fullName string) (Contents, error) {
	owner, repo, err := parseRepo(fullName)
	if err != nil {
		return Contents{}, err
	}
	fullName = owner + "/" + repo
	if contents, ok := c.cached(fullName); ok {
		return contents, nil
	}
	var repository repoResponse
	if err = c.getJSON(ctx, apiBase+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo), &repository); err != nil {
		return Contents{}, err
	}
	var tree treeResponse
	if err = c.getJSON(ctx, apiBase+"/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(repo)+"/git/trees/HEAD?recursive=1", &tree); err != nil {
		return Contents{}, err
	}
	result := classifyContents(Repo{FullName: fullName, Description: repository.Description, License: repository.License.Name, URL: repository.HTMLURL, SHA: tree.SHA, Stars: repository.StargazersCount}, tree)
	if err = validateLimits(result); err != nil {
		return result, err
	}
	if len(result.Symbols)+len(result.Footprints)+len(result.Models) == 0 && !hasLegacyLib(result.LegacyFiles) {
		if result.Legacy > 0 {
			return result, fmt.Errorf("此 repo 只有舊格式檔案: %w", ErrNoKiCadFiles)
		}
		return result, ErrNoKiCadFiles
	}
	c.store(fullName, result)
	return result, nil
}

func (c *Client) Download(ctx context.Context, contents Contents, dir string) error {
	if err := validateDownloadable(contents); err != nil {
		return err
	}
	if strings.TrimSpace(dir) == "" {
		return errors.New("download directory is empty")
	}
	owner, repo, err := parseRepo(contents.Repo.FullName)
	if err != nil {
		return err
	}
	files := allFiles(contents)
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	data, err := c.downloadFiles(ctx, contents, files, root, owner, repo)
	if err != nil {
		return err
	}
	return writeDownloads(root, data)
}

func validateDownloadable(contents Contents) error {
	if err := validateLimits(contents); err != nil {
		return err
	}
	if len(contents.Symbols)+len(contents.Footprints)+len(contents.Models) > 0 || hasLegacyLib(contents.LegacyFiles) {
		return nil
	}
	if contents.Legacy > 0 {
		return fmt.Errorf("此 repo 只有舊格式檔案: %w", ErrNoKiCadFiles)
	}
	return ErrNoKiCadFiles
}

func (c *Client) downloadFiles(ctx context.Context, contents Contents, files []File, root, owner, repo string) ([]downloadedFile, error) {
	data := make([]downloadedFile, 0, len(files))
	var total int64
	for _, file := range files {
		target, rel, ok := downloadTarget(root, file.Path)
		if !ok {
			continue
		}
		body, err := c.downloadFile(ctx, contents, owner, repo, file, len(files))
		if err != nil {
			return nil, err
		}
		total += int64(len(body))
		if total > maxTotalBytes {
			return nil, repoTooLargeError(len(files))
		}
		data = append(data, downloadedFile{path: target, rel: rel, body: body})
	}
	return data, nil
}

func (c *Client) downloadFile(ctx context.Context, contents Contents, owner, repo string, file File, count int) ([]byte, error) {
	body, err := c.get(ctx, rawURL(owner, repo, contents.Repo.SHA, file.Path), maxFileSize+1)
	if errors.Is(err, ErrRepoTooLarge) {
		return nil, repoTooLargeError(count)
	}
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxFileSize || int64(len(body)) > file.Size {
		return nil, repoTooLargeError(count)
	}
	return body, nil
}

func writeDownloads(root string, data []downloadedFile) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	created := make([]string, 0, len(data))
	for _, file := range data {
		if err := secureWrite(root, file.path, file.body); err != nil {
			cleanupPaths(created)
			return err
		}
		created = append(created, file.path)
	}
	return nil
}

type downloadedFile struct {
	path, rel string
	body      []byte
}

func cleanupPaths(paths []string) {
	for _, name := range paths {
		_ = os.Remove(name)
	}
}

func secureWrite(root, target string, body []byte) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("download directory is not a plain directory")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("download path escapes target directory")
	}
	parts := strings.Split(filepath.Dir(rel), string(filepath.Separator))
	current := root
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			if statErr = os.Mkdir(current, 0755); statErr != nil {
				return statErr
			}
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("download path contains a non-directory component")
		}
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(target)
		return err
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, target string, out any) error {
	body, err := c.get(ctx, target, 8<<20)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func (c *Client) get(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kicad-partfetch")
	token := c.token
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read GitHub response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if rateLimited(response) {
			return nil, rateLimitError(response.Header.Get("X-RateLimit-Reset"), c.now())
		}
		if response.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("%w: HTTP %d", ErrNotFound, response.StatusCode)
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("%w: HTTP %d", ErrGitHubRateLimited, response.StatusCode)
		}
		return nil, fmt.Errorf("GitHub request returned HTTP %d", response.StatusCode)
	}
	if int64(len(body)) > limit {
		return nil, ErrRepoTooLarge
	}
	return body, nil
}

func rateLimited(response *http.Response) bool {
	return (response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests) && response.Header.Get("X-RateLimit-Remaining") == "0"
}

func rateLimitError(reset string, now time.Time) error {
	seconds, err := strconv.ParseInt(reset, 10, 64)
	if err != nil {
		return ErrGitHubRateLimited
	}
	return fmt.Errorf("%w，請於 %s 後重試", ErrGitHubRateLimited, time.Unix(seconds, 0).Local().Format("2006-01-02 15:04:05"))
}

func parseRepo(fullName string) (string, string, error) {
	_, canonical, err := partid.Parse("github:" + fullName)
	if err != nil {
		return "", "", err
	}
	parts := strings.Split(canonical, "/")
	return parts[0], parts[1], nil
}

func rawURL(owner, repo, sha, file string) string {
	parts := strings.Split(strings.ReplaceAll(file, "\\", "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return rawBase + "/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/" + url.PathEscape(sha) + "/" + strings.Join(parts, "/")
}

func downloadTarget(root, name string) (string, string, bool) {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "." || strings.HasPrefix(clean, "/") || filepath.IsAbs(name) || windowsAbsolute(name) || hasParentSegment(name) {
		return "", "", false
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	return target, rel, true
}

func windowsAbsolute(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':'
}

func hasParentSegment(value string) bool {
	for _, part := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func allFiles(contents Contents) []File {
	files := make([]File, 0, len(contents.Symbols)+len(contents.Footprints)+len(contents.Models)+len(contents.LegacyFiles))
	files = append(files, contents.Symbols...)
	files = append(files, contents.Footprints...)
	files = append(files, contents.Models...)
	for _, file := range contents.LegacyFiles {
		ext := strings.ToLower(path.Ext(file.Path))
		if ext == ".lib" || ext == ".dcm" {
			files = append(files, file)
		}
	}
	return files
}

func hasLegacyLib(files []File) bool {
	for _, file := range files {
		if strings.EqualFold(path.Ext(file.Path), ".lib") {
			return true
		}
	}
	return false
}

func validateLimits(contents Contents) error {
	files := allFiles(contents)
	if contents.Truncated || len(files) > maxFiles {
		return repoTooLargeError(len(files))
	}
	var total int64
	for _, file := range files {
		if file.Size < 0 || file.Size > maxFileSize {
			return repoTooLargeError(len(files))
		}
		total += file.Size
	}
	if total > maxTotalBytes {
		return repoTooLargeError(len(files))
	}
	return nil
}

func repoTooLargeError(count int) error {
	return repoLimitError{count: count}
}

func (c *Client) cached(name string) (Contents, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[name]
	if !ok || !c.now().Before(entry.expires) {
		delete(c.cache, name)
		return Contents{}, false
	}
	return entry.value, true
}

func (c *Client) store(name string, contents Contents) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[name] = cachedContents{value: contents, expires: c.now().Add(10 * time.Minute)}
}

func classifyContents(repo Repo, tree treeResponse) Contents {
	contents := Contents{Repo: repo, Symbols: []File{}, Footprints: []File{}, Models: []File{}, LegacyFiles: []File{}, Truncated: tree.Truncated}
	for _, entry := range tree.Tree {
		if entry.Type != "blob" {
			continue
		}
		ext := strings.ToLower(path.Ext(entry.Path))
		file := File{Path: entry.Path, Size: entry.Size}
		switch ext {
		case ".kicad_sym":
			contents.Symbols = append(contents.Symbols, file)
		case ".kicad_mod":
			contents.Footprints = append(contents.Footprints, file)
		case ".step", ".stp", ".wrl":
			contents.Models = append(contents.Models, file)
		case ".lib", ".mod", ".dcm":
			contents.Legacy++
			contents.LegacyFiles = append(contents.LegacyFiles, file)
		}
	}
	sortFiles(contents.Symbols)
	sortFiles(contents.Footprints)
	sortFiles(contents.Models)
	sortFiles(contents.LegacyFiles)
	return contents
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
}

type searchResponse struct {
	TotalCount int `json:"total_count"`
	Items      []struct {
		FullName        string `json:"full_name"`
		Description     string `json:"description"`
		HTMLURL         string `json:"html_url"`
		StargazersCount int    `json:"stargazers_count"`
		License         *struct {
			Name string `json:"name"`
		} `json:"license"`
	} `json:"items"`
}

type repoResponse struct {
	FullName        string `json:"full_name"`
	Description     string `json:"description"`
	HTMLURL         string `json:"html_url"`
	StargazersCount int    `json:"stargazers_count"`
	License         struct {
		Name string `json:"name"`
	} `json:"license"`
}

type treeResponse struct {
	SHA       string `json:"sha"`
	Truncated bool   `json:"truncated"`
	Tree      []struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tree"`
}
