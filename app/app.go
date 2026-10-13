// Package app exposes the methods bound to the Wails frontend.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/add"
	"github.com/sywung/kicad-partfetch/internal/applog"
	"github.com/sywung/kicad-partfetch/internal/failure"
	"github.com/sywung/kicad-partfetch/internal/importer"
	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/partid"
	"github.com/sywung/kicad-partfetch/internal/preview"
	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/settings"
	"github.com/sywung/kicad-partfetch/internal/source"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type SearchItem struct {
	PartID               string
	MPN                  string
	Manufacturer         string
	Package              string
	Description          string
	Stock                int
	Price                string
	ImageURL             string
	DatasheetURL         string
	Source, URL, License string
	Stars                int
}
type SearchResult struct {
	Items    []SearchItem
	Total    int
	Page     int
	PageSize int
}
type Preview struct {
	PartID, Name, Package, Manufacturer, MPN string
	SymbolSVGs                               []string
	FootprintSVG                             string
	FootprintSVGs                            []string
	Files                                    []string
	Source, URL, License                     string
	External                                 bool
	PinCount, PadCount                       int
	HasModel                                 bool
	ModelTitle                               string
	Warnings                                 []string
}
type ModelData struct {
	OBJ            string
	Offset, Rotate [3]float64
}
type Settings struct {
	OutDir, LibName, ModelBase string
	With3D, Overwrite          bool
}
type AddResult struct {
	PartID, Symbol, Footprint, Model string
	Warnings                         []string
	Error                            string
	ErrorKind                        string
}
type LibraryEntry struct {
	Name         string
	Footprint    string
	LCSC         string
	PartID       string
	HasFootprint bool
	HasModel     bool
}
type RemoveResult struct{ Name, Symbol, Footprint, Model, Error string }
type ImportItem struct{ Name, Status string }
type ImportResult struct {
	Source                      string
	Symbols, Footprints, Models []ImportItem
	Warnings                    []string
	Error                       string
}

type Searcher interface {
	Search(context.Context, string, int) (search.Result, error)
}
type GitHubClient interface {
	Search(context.Context, string, int) ([]gh.Repo, int, error)
	Contents(context.Context, string) (gh.Contents, error)
	Download(context.Context, gh.Contents, string) error
}
type KiCadCLI = kicadcli.Client

type Options struct {
	Source              source.Source
	Searcher            Searcher
	GitHub              GitHubClient
	KiCadCLI            KiCadCLI
	UpgradeSymbols      func(context.Context, string, []byte, []byte) ([]byte, error)
	SettingsPath        string
	Logger              *slog.Logger
	LogPath             string
	PickDirectory       func(context.Context) (string, error)
	PickImportFiles     func(context.Context) ([]string, error)
	PickImportDirectory func(context.Context) (string, error)
	Emit                func(string, any)
	OpenPath            func(context.Context, string) error
	OpenURL             func(context.Context, string) error
}
type App struct {
	ctx                 context.Context
	source              source.Source
	searcher            Searcher
	github              GitHubClient
	kicadCLI            KiCadCLI
	upgradeSymbols      func(context.Context, string, []byte, []byte) ([]byte, error)
	settingsPath        string
	settingsErr         error
	logger              *slog.Logger
	logPath             string
	pickDirectory       func(context.Context) (string, error)
	pickImportFiles     func(context.Context) ([]string, error)
	pickImportDirectory func(context.Context) (string, error)
	emit                func(string, any)
	openPath            func(context.Context, string) error
	openURL             func(context.Context, string) error
}

func New() *App {
	settingsPath, err := settings.ConfigPath()
	if err != nil {
		return NewWithOptions(Options{})
	}
	logPath := filepath.Join(filepath.Dir(settingsPath), "partfetch.log")
	logger, _ := applog.Open(logPath)
	return NewWithOptions(Options{SettingsPath: settingsPath, Logger: logger, LogPath: logPath})
}
func NewWithOptions(o Options) *App {
	if o.Source == nil {
		o.Source = easyeda.NewClient(easyeda.ClientOptions{})
	}
	if o.Searcher == nil {
		searchOptions := search.Options{}
		if transport, ok := o.Source.(http.RoundTripper); ok {
			searchOptions.Transport = transport
		}
		o.Searcher = search.New(searchOptions)
	}
	if o.GitHub == nil {
		o.GitHub = gh.New(gh.Options{})
	}
	if o.KiCadCLI == nil {
		o.KiCadCLI = kicadcli.SystemClient{}
	}
	explicitSettingsPath := o.SettingsPath != ""
	settingsPath := o.SettingsPath
	var settingsErr error
	if settingsPath == "" {
		settingsPath, settingsErr = settings.ConfigPath()
	}
	logPath := o.LogPath
	if logPath == "" && settingsPath != "" {
		logPath = filepath.Join(filepath.Dir(settingsPath), "partfetch.log")
	}
	if logPath == "" {
		logPath = filepath.Join(os.TempDir(), "kicad-partfetch", "partfetch.log")
	}
	logger := o.Logger
	if logger == nil {
		if o.LogPath != "" || explicitSettingsPath {
			logger, _ = applog.Open(logPath)
		} else {
			logger = applog.Discard()
		}
	}
	a := &App{source: o.Source, searcher: o.Searcher, github: o.GitHub, kicadCLI: o.KiCadCLI, upgradeSymbols: o.UpgradeSymbols, settingsPath: settingsPath, settingsErr: settingsErr, logger: logger, logPath: logPath, pickDirectory: o.PickDirectory, pickImportFiles: o.PickImportFiles, pickImportDirectory: o.PickImportDirectory, emit: o.Emit, openPath: o.OpenPath, openURL: o.OpenURL}
	a.logger.Info("application started")
	if a.pickDirectory == nil {
		a.pickDirectory = func(ctx context.Context) (string, error) {
			return wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{Title: "選擇 KiCad 元件庫目錄"})
		}
	}
	if a.pickImportFiles == nil {
		a.pickImportFiles = func(ctx context.Context) ([]string, error) {
			return wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{Title: "匯入 KiCad 元件檔案", Filters: []wailsruntime.FileFilter{{DisplayName: "KiCad 元件檔案", Pattern: "*.zip;*.kicad_sym;*.kicad_mod;*.step;*.stp;*.wrl"}}})
		}
	}
	if a.pickImportDirectory == nil {
		a.pickImportDirectory = func(ctx context.Context) (string, error) {
			return wailsruntime.OpenDirectoryDialog(ctx, wailsruntime.OpenDialogOptions{Title: "選擇 KiCad 元件資料夾"})
		}
	}
	if a.emit == nil {
		a.emit = func(name string, payload any) {
			if a.ctx != nil {
				wailsruntime.EventsEmit(a.ctx, name, payload)
			}
		}
	}
	if a.openURL == nil {
		a.openURL = func(ctx context.Context, target string) error { wailsruntime.BrowserOpenURL(ctx, target); return nil }
	}
	if a.openPath == nil {
		a.openPath = systemOpenPath
	}
	return a
}
func (a *App) Startup(ctx context.Context) { a.ctx = ctx }

func (a *App) LogPath() string {
	return a.logPath
}

func (a *App) OpenLog() error {
	if err := a.openPath(a.ctx, filepath.Dir(a.logPath)); err != nil {
		return errors.New("無法開啟紀錄檔所在目錄")
	}
	return nil
}

func (a *App) reportError(op, partID, sourceName, outDir string, err error) error {
	a.logFailure(op, partID, err)
	if message, ok := authoredMessage(err); ok {
		return errors.New(message)
	}
	return errors.New(userMessage(op, failure.Classify(err), partID, sourceName, outDir))
}

// authoredMessage returns the text of errors that were written for the user,
// which is more specific than any message derived from the error's kind.
func authoredMessage(err error) (string, bool) {
	if errors.Is(err, gh.ErrNoKiCadFiles) {
		return err.Error(), true
	}
	var damaged *settings.DamagedError
	if errors.As(err, &damaged) {
		return "設定檔損毀，已載入預設值；儲存設定可重建檔案", true
	}
	if errors.Is(err, gh.ErrRepoTooLarge) {
		var limit interface{ UserMessage() string }
		if errors.As(err, &limit) {
			return limit.UserMessage(), true
		}
		return "此 repo 的 KiCad 檔案過多或過大，請自行下載後用『匯入資料夾…』挑選", true
	}
	return "", false
}

// importMessage turns one import result into the message shown to the user.
// Messages the importer wrote itself are kept; raw underlying errors are classified.
func (a *App) importMessage(op, partID, sourceName, outDir string, result importer.Result) (string, string) {
	authored := result.Error != "" && (result.Err == nil || result.Error != result.Err.Error())
	if authored {
		cause := result.Err
		if cause == nil {
			cause = failure.ErrBadData
		}
		a.logFailure(op, partID, fmt.Errorf("%s: %w", result.Error, cause))
		return result.Error, string(failure.Classify(cause))
	}
	if result.Err == nil {
		return "", ""
	}
	if result.Error == "" && !importHasFailedItems(result) {
		a.logFailure(op, partID, result.Err)
		return "", ""
	}
	return a.reportError(op, partID, sourceName, outDir, result.Err).Error(), string(failure.Classify(result.Err))
}

func (a *App) logFailure(op, partID string, err error) {
	kind := failure.Classify(err)
	level := slog.LevelWarn
	if kind == failure.Unknown || kind == failure.BadData {
		level = slog.LevelError
	}
	attrs := []any{"op", op, "kind", string(kind), "err", err.Error()}
	if partID != "" {
		attrs = append(attrs, "part", partID)
	}
	a.logger.Log(context.Background(), level, "operation failed", attrs...)
}

func userMessage(op string, kind failure.Kind, partID, sourceName, outDir string) string {
	object := partID
	if strings.HasPrefix(object, "github:") {
		object = strings.TrimPrefix(object, "github:")
	}
	if object == "" {
		object = "這個項目"
	}
	sourceLabel := "LCSC"
	if sourceName == "easyeda" || strings.HasPrefix(partID, "easyeda:") {
		sourceLabel = "EasyEDA"
	} else if sourceName == "github" || strings.HasPrefix(partID, "github:") {
		sourceLabel = "GitHub"
	}
	switch kind {
	case failure.NotFound:
		if strings.HasPrefix(partID, "github:") || sourceName == "github" {
			return "找不到 GitHub repo " + object + "，請確認 repo 名稱"
		}
		return "找不到零件 " + object + "，請確認編號"
	case failure.Permission:
		if op == "settings" {
			return "無法寫入設定檔 " + outDir + "，請確認目錄權限"
		}
		return "無法寫入元件庫目錄 " + outDir + "，請確認目錄權限"
	case failure.DiskFull:
		if op == "settings" {
			return "磁碟空間不足，無法儲存設定檔 " + outDir
		}
		return "磁碟空間不足，無法寫入 " + outDir
	case failure.Network:
		return "無法連線到 " + sourceLabel + "，請確認網路連線"
	case failure.RateLimited:
		return "來源暫時限制請求，請稍後再試"
	case failure.BadData:
		return "零件資料無法轉換，這個零件可能不受支援"
	case failure.ToolMissing:
		return "找不到 kicad-cli，請安裝 KiCad 或設定 kicad-cli 路徑"
	default:
		verbs := map[string]string{"add": "加入", "preview": "載入預覽", "search": "搜尋", "model": "載入 3D 模型", "import": "匯入", "list": "讀取元件庫", "remove": "移除", "settings": "處理設定"}
		verb := verbs[op]
		if verb == "" {
			verb = "執行"
		}
		return verb + "失敗，詳情請看紀錄檔"
	}
}

func (a *App) Search(sourceName, query string, page int) (SearchResult, error) {
	if sourceName != "lcsc" && sourceName != "easyeda" && sourceName != "github" {
		return SearchResult{}, fmt.Errorf("invalid source %q", sourceName)
	}
	if page < 1 {
		page = 1
	}
	if items, ok := pastedPartIDs(query); ok {
		return SearchResult{Items: items, Total: len(items), Page: page, PageSize: 25}, nil
	}
	if sourceName == "github" {
		return a.searchGitHub(query, page)
	}
	result, err := a.searchSource(sourceName, query, page)
	if err != nil {
		return SearchResult{}, a.reportError("search", "", sourceName, "", err)
	}
	return bindSearchResult(sourceName, result, page), nil
}

func (a *App) searchGitHub(query string, page int) (SearchResult, error) {
	repos, total, err := a.github.Search(context.Background(), query, page)
	if err != nil {
		return SearchResult{}, a.reportError("search", "", "github", "", err)
	}
	items := make([]SearchItem, 0, len(repos))
	for _, repo := range repos {
		_, fullName, parseErr := partid.Parse("github:" + repo.FullName)
		if parseErr != nil {
			return SearchResult{}, a.reportError("search", "", "github", "", fmt.Errorf("%w: invalid repository identifier: %v", failure.ErrBadData, parseErr))
		}
		owner := strings.SplitN(fullName, "/", 2)[0]
		items = append(items, SearchItem{PartID: "github:" + fullName, MPN: fullName, Manufacturer: owner, Description: repo.Description, Source: "github", URL: repo.URL, License: repo.License, Stars: repo.Stars})
	}
	return SearchResult{Items: items, Total: total, Page: page, PageSize: 25}, nil
}

func pastedPartIDs(query string) ([]SearchItem, bool) {
	parts := strings.Fields(strings.NewReplacer(",", " ", "\n", " ", "\r", " ").Replace(query))
	if len(parts) == 0 {
		return nil, false
	}
	items := make([]SearchItem, 0, len(parts))
	for _, part := range parts {
		source, value, err := partid.Parse(part)
		if err != nil {
			return nil, false
		}
		partID := value
		if source != "lcsc" {
			partID = source + ":" + value
		}
		items = append(items, SearchItem{PartID: partID, Source: source})
	}
	return items, true
}

func (a *App) searchSource(sourceName, query string, page int) (search.Result, error) {
	if sourceName == "easyeda" {
		community, ok := a.searcher.(interface {
			Community(context.Context, string, int) (search.Result, error)
		})
		if !ok {
			return search.Result{}, errors.New("EasyEDA 社群搜尋不可用")
		}
		return community.Community(context.Background(), query, page)
	}
	return a.searcher.Search(context.Background(), query, page)
}

func searchError(sourceName string) error {
	if sourceName == "easyeda" {
		return errors.New("搜尋 EasyEDA 社群失敗，請確認網路連線後重試")
	}
	return errors.New("搜尋 LCSC 失敗，請確認網路連線後重試")
}

func bindSearchResult(sourceName string, result search.Result, page int) SearchResult {
	items := make([]SearchItem, 0, len(result.Items))
	for _, item := range result.Items {
		itemSource := item.Source
		if itemSource == "" {
			itemSource = sourceName
		}
		items = append(items, SearchItem{PartID: item.PartID, MPN: item.MPN, Manufacturer: item.Manufacturer, Package: item.Package, Description: item.Description, Stock: item.Stock, Price: item.Price, ImageURL: item.ImageURL, DatasheetURL: item.DatasheetURL, Source: itemSource, URL: item.URL, License: item.License, Stars: item.Stars})
	}
	return SearchResult{Items: items, Total: result.Total, Page: page, PageSize: 25}
}
func (a *App) Preview(partID string) (Preview, error) {
	sourceName, canonicalID, err := partid.Parse(partID)
	if err != nil {
		return Preview{}, err
	}
	if sourceName == "github" {
		result, previewErr := a.previewGitHub(canonicalID)
		if previewErr != nil {
			return Preview{}, a.reportError("preview", "github:"+canonicalID, "github", "", previewErr)
		}
		return result, nil
	}
	if sourceName == "easyeda" {
		canonicalID = "easyeda:" + canonicalID
	}
	c, err := a.source.Fetch(context.Background(), canonicalID)
	if err != nil {
		return Preview{}, a.reportError("preview", canonicalID, sourceName, "", err)
	}
	r := Preview{PartID: canonicalID, Name: c.Value, Package: c.Footprint.Name, Manufacturer: c.Manufacturer, MPN: c.MPN, FootprintSVG: preview.FootprintSVG(c.Footprint), FootprintSVGs: []string{}, Files: []string{}, Source: sourceName, PadCount: len(c.Footprint.Pads), SymbolSVGs: []string{}, Warnings: append([]string{}, c.Warnings...), HasModel: len(c.Footprint.Models) > 0}
	r.FootprintSVGs = append(r.FootprintSVGs, r.FootprintSVG)
	for i, u := range c.Units {
		r.SymbolSVGs = append(r.SymbolSVGs, preview.SymbolSVG(u, i+1))
		r.PinCount += len(u.Pins)
	}
	if r.HasModel {
		r.ModelTitle = c.Footprint.Models[0].Title
	}
	return r, nil
}

func (a *App) previewGitHub(fullName string) (Preview, error) {
	contents, err := a.github.Contents(context.Background(), fullName)
	if err != nil {
		return Preview{}, err
	}
	result := Preview{PartID: "github:" + fullName, Name: lastRepoPart(fullName), Manufacturer: strings.SplitN(fullName, "/", 2)[0], Source: "github", URL: contents.Repo.URL, License: contents.Repo.License, Files: githubFiles(contents), SymbolSVGs: []string{}, FootprintSVGs: []string{}, Warnings: []string{}, HasModel: len(contents.Models) > 0, External: true}
	if result.HasModel {
		result.ModelTitle = filepath.Base(contents.Models[0].Path)
	}
	dir, err := os.MkdirTemp("", "partfetch-github-preview-*")
	if err != nil {
		return Preview{}, err
	}
	defer os.RemoveAll(dir)
	if err = a.github.Download(context.Background(), contents, dir); err != nil {
		return Preview{}, err
	}
	cli, err := a.kicadCLI.Find(a.configuredKiCadCLI())
	if errors.Is(err, kicadcli.ErrNotFound) {
		result.Warnings = append(result.Warnings, "找不到 kicad-cli，無法產生預覽圖；仍可加入元件庫")
		return result, nil
	}
	if err != nil {
		return Preview{}, err
	}
	symbolFiles := a.upgradeGitHubSymbols(context.Background(), cli, dir, contents, &result)
	addGitHubPreviewWarnings(&result, len(symbolFiles), contents)
	result.SymbolSVGs, err = exportGitHubSymbols(context.Background(), a.kicadCLI, cli, dir, symbolFiles, &result)
	if err != nil {
		return Preview{}, err
	}
	result.FootprintSVGs, err = exportGitHubFootprints(context.Background(), a.kicadCLI, cli, dir, contents.Footprints, &result)
	if err != nil {
		return Preview{}, err
	}
	return result, nil
}

func addGitHubPreviewWarnings(result *Preview, symbolCount int, contents gh.Contents) {
	if symbolCount > 5 {
		result.Warnings = append(result.Warnings, "僅預覽前 5 個符號檔")
	}
	if len(contents.Footprints) > 24 {
		result.Warnings = append(result.Warnings, "僅預覽前 24 個封裝")
	}
}

func (a *App) upgradeGitHubSymbols(ctx context.Context, executable, root string, contents gh.Contents, result *Preview) []gh.File {
	files := append([]gh.File(nil), contents.Symbols...)
	upgrade := a.upgradeSymbols
	if upgrade == nil {
		upgrade = kicadcli.UpgradeSymbolLibrary
	}
	for _, legacy := range contents.LegacyFiles {
		if !strings.EqualFold(filepath.Ext(legacy.Path), ".lib") || githubHasSibling(contents.Symbols, legacy.Path, ".kicad_sym") {
			continue
		}
		libPath := filepath.Join(root, filepath.FromSlash(legacy.Path))
		libData, err := os.ReadFile(libPath)
		if err != nil {
			a.logFailure("preview", "github:"+contents.Repo.FullName, err)
			result.Warnings = append(result.Warnings, "舊版符號庫 "+legacy.Path+" 讀取失敗")
			continue
		}
		dcmData, err := readGitHubDCM(root, contents.LegacyFiles, legacy.Path)
		if err != nil {
			a.logFailure("preview", "github:"+contents.Repo.FullName, err)
			result.Warnings = append(result.Warnings, "舊版文件庫 "+legacy.Path+" 讀取失敗")
			continue
		}
		converted, err := upgrade(ctx, executable, libData, dcmData)
		if err != nil {
			a.logFailure("preview", "github:"+contents.Repo.FullName, fmt.Errorf("%w: %w", failure.ErrBadData, err))
			result.Warnings = append(result.Warnings, "舊版符號庫 "+legacy.Path+" 轉換失敗")
			continue
		}
		virtual := strings.TrimSuffix(legacy.Path, filepath.Ext(legacy.Path)) + ".kicad_sym"
		if err = os.WriteFile(filepath.Join(root, filepath.FromSlash(virtual)), converted, 0600); err != nil {
			a.logFailure("preview", "github:"+contents.Repo.FullName, err)
			result.Warnings = append(result.Warnings, "轉換符號庫 "+legacy.Path+" 寫入失敗")
			continue
		}
		files = append(files, gh.File{Path: virtual, Size: int64(len(converted))})
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

func githubHasSibling(files []gh.File, path, ext string) bool {
	want := strings.TrimSuffix(path, filepath.Ext(path)) + ext
	for _, file := range files {
		if filepath.ToSlash(filepath.Dir(file.Path)) == filepath.ToSlash(filepath.Dir(want)) && strings.EqualFold(filepath.Base(file.Path), filepath.Base(want)) {
			return true
		}
	}
	return false
}

func readGitHubDCM(root string, files []gh.File, libPath string) ([]byte, error) {
	for _, file := range files {
		if strings.EqualFold(filepath.Ext(file.Path), ".dcm") && githubHasSibling([]gh.File{{Path: file.Path}}, libPath, ".dcm") {
			return os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		}
	}
	return nil, nil
}

func exportGitHubSymbols(ctx context.Context, cli KiCadCLI, executable, root string, files []gh.File, result *Preview) ([]string, error) {
	paths := files
	if len(paths) > 5 {
		paths = paths[:5]
	}
	values := make([]string, 0)
	for _, file := range paths {
		exported, err := cli.SymbolSVGs(ctx, executable, filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return nil, err
		}
		limit := len(exported)
		if limit > 12 {
			limit = 12
			result.Warnings = append(result.Warnings, "符號檔 "+file.Path+" 僅預覽前 12 個 SVG")
		}
		for _, svg := range exported[:limit] {
			values = append(values, string(svg.SVG))
		}
	}
	return values, nil
}

func exportGitHubFootprints(ctx context.Context, cli KiCadCLI, executable, root string, files []gh.File, result *Preview) ([]string, error) {
	if len(files) == 0 {
		return []string{}, nil
	}
	if len(files) > 24 {
		files = files[:24]
	}
	values := make([]string, 0, len(files))
	for index, file := range files {
		pretty := filepath.Join(root, fmt.Sprintf("__preview_%03d.pretty", index))
		if err := os.MkdirAll(pretty, 0755); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Path)))
		if err != nil {
			return nil, err
		}
		if err = os.WriteFile(filepath.Join(pretty, filepath.Base(file.Path)), data, 0600); err != nil {
			return nil, err
		}
		exported, exportErr := cli.FootprintSVGs(ctx, executable, pretty)
		if exportErr != nil {
			return nil, exportErr
		}
		for _, svg := range exported {
			if len(values) == 24 {
				result.Warnings = append(result.Warnings, "僅預覽前 24 個封裝")
				return values, nil
			}
			values = append(values, string(svg.SVG))
		}
	}
	return values, nil
}

func githubFiles(contents gh.Contents) []string {
	files := make([]string, 0, len(contents.Symbols)+len(contents.Footprints)+len(contents.Models)+len(contents.LegacyFiles))
	for _, group := range [][]gh.File{contents.Symbols, contents.Footprints, contents.Models, contents.LegacyFiles} {
		for _, file := range group {
			files = append(files, file.Path)
		}
	}
	return files
}

func lastRepoPart(fullName string) string {
	parts := strings.SplitN(fullName, "/", 2)
	return parts[len(parts)-1]
}

func (a *App) configuredKiCadCLI() string {
	loaded, err := settings.Load(a.settingsPath)
	if err != nil {
		return ""
	}
	return loaded.KicadCLI
}
func (a *App) Model(partID string) (ModelData, error) {
	sourceName, canonicalID, err := partid.Parse(partID)
	if err != nil {
		return ModelData{}, err
	}
	if sourceName == "github" {
		return ModelData{}, errors.New("GitHub 來源的 3D 模型無法在此預覽，請加入後在 KiCad 檢視")
	}
	if sourceName == "easyeda" {
		canonicalID = "easyeda:" + canonicalID
	}
	c, err := a.source.Fetch(context.Background(), canonicalID)
	if err != nil {
		return ModelData{}, a.reportError("model", canonicalID, sourceName, "", err)
	}
	if len(c.Footprint.Models) == 0 {
		return ModelData{}, errors.New("此零件沒有可用的 3D 模型")
	}
	ref := c.Footprint.Models[0]
	obj, err := a.source.FetchModel(context.Background(), ref, "obj")
	if err != nil {
		return ModelData{}, a.reportError("model", canonicalID, sourceName, "", err)
	}
	vertices, err := easyeda.ParseOBJVertices(obj)
	if err != nil {
		return ModelData{}, a.reportError("model", canonicalID, sourceName, "", fmt.Errorf("%w: %w", failure.ErrBadData, err))
	}
	offset, rotate := easyeda.StepPlacement(ref, vertices)
	return ModelData{OBJ: string(obj), Offset: [3]float64{offset.X, offset.Y, offset.Z}, Rotate: rotate}, nil
}
func (a *App) GetSettings() (Settings, error) {
	if a.settingsErr != nil {
		return fromSettings(settings.Defaults()), a.reportError("settings", "", "", "", a.settingsErr)
	}
	s, err := settings.Load(a.settingsPath)
	if err != nil {
		return fromSettings(s), a.reportError("settings", "", "", a.settingsPath, err)
	}
	return fromSettings(s), nil
}
func (a *App) SaveSettings(s Settings) error {
	if a.settingsErr != nil {
		return a.reportError("settings", "", "", "", a.settingsErr)
	}
	if strings.TrimSpace(s.OutDir) == "" {
		return errors.New("請先選擇元件庫目錄")
	}
	if strings.TrimSpace(s.LibName) == "" {
		return errors.New("元件庫名稱不可空白")
	}
	if s.ModelBase == "" {
		s.ModelBase = "absolute"
	}
	value := toSettings(s)
	if previous, err := settings.Load(a.settingsPath); err == nil {
		value.KicadCLI = previous.KicadCLI
	}
	if err := settings.Save(a.settingsPath, value); err != nil {
		return a.reportError("settings", "", "", filepath.Dir(a.settingsPath), err)
	}
	return nil
}
func fromSettings(s settings.Settings) Settings {
	return Settings{OutDir: s.OutDir, LibName: s.LibName, ModelBase: s.ModelBase, With3D: s.With3D, Overwrite: s.Overwrite}
}
func toSettings(s Settings) settings.Settings {
	return settings.Settings{OutDir: s.OutDir, LibName: s.LibName, ModelBase: s.ModelBase, With3D: s.With3D, Overwrite: s.Overwrite}
}
func (a *App) PickDirectory() (string, error) {
	path, err := a.pickDirectory(a.ctx)
	if err != nil {
		return "", errors.New("選擇目錄失敗")
	}
	return path, nil
}

func (a *App) PickImportFiles() ([]string, error) {
	paths, err := a.pickImportFiles(a.ctx)
	if err != nil {
		return []string{}, errors.New("選擇匯入檔案失敗")
	}
	if paths == nil {
		return []string{}, nil
	}
	return paths, nil
}

func (a *App) PickImportDirectory() (string, error) {
	path, err := a.pickImportDirectory(a.ctx)
	if err != nil {
		return "", errors.New("選擇匯入資料夾失敗")
	}
	return path, nil
}

func (a *App) Import(paths []string) ([]ImportResult, error) {
	if len(paths) == 0 {
		return []ImportResult{}, errors.New("請先選擇要匯入的檔案")
	}
	s, err := a.GetSettings()
	if err != nil {
		return []ImportResult{}, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return []ImportResult{}, a.reportError("import", "", "", s.OutDir, err)
	}
	results := importer.ImportWith(opts, paths, a.legacyConverter(context.Background()))
	out := make([]ImportResult, 0, len(results))
	for _, result := range results {
		message, _ := a.importMessage("import", "", "", opts.Out, result)
		out = append(out, ImportResult{Source: result.Source, Symbols: importResultItems(result.Symbols), Footprints: importResultItems(result.Footprints), Models: importResultItems(result.Models), Warnings: append([]string{}, result.UserWarnings...), Error: message})
	}
	return out, nil
}

func importHasFailedItems(result importer.Result) bool {
	for _, items := range [][]importer.Item{result.Symbols, result.Footprints, result.Models} {
		for _, item := range items {
			if item.Status == "failed" {
				return true
			}
		}
	}
	return false
}

func (a *App) legacyConverter(ctx context.Context) importer.LegacyConverter {
	executable, err := a.kicadCLI.Find(a.configuredKiCadCLI())
	if err != nil {
		return nil
	}
	upgrade := a.upgradeSymbols
	if upgrade == nil {
		upgrade = kicadcli.UpgradeSymbolLibrary
	}
	return func(lib, dcm []byte) ([]byte, error) { return upgrade(ctx, executable, lib, dcm) }
}

func importResultItems(items []importer.Item) []ImportItem {
	out := make([]ImportItem, 0, len(items))
	for _, item := range items {
		out = append(out, ImportItem{Name: item.Name, Status: item.Status})
	}
	return out
}
func (a *App) Add(partIDs []string) ([]AddResult, error) {
	if len(partIDs) == 0 {
		return nil, errors.New("請先選擇要加入的零件")
	}
	canonicalIDs, err := canonicalAddIDs(partIDs)
	if err != nil {
		return nil, err
	}
	s, err := a.GetSettings()
	if err != nil {
		return nil, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return nil, a.reportError("add", "", "", s.OutDir, err)
	}
	out := make([]AddResult, 0, len(partIDs))
	total := len(canonicalIDs)
	for i, id := range canonicalIDs {
		r := a.addOne(context.Background(), id, opts, !s.With3D)
		out = append(out, r)
		a.emit("add:progress", map[string]any{"index": i + 1, "total": total, "result": r})
	}
	return out, nil
}

func canonicalAddIDs(ids []string) ([]string, error) {
	canonical := make([]string, len(ids))
	for i, id := range ids {
		sourceName, value, err := partid.Parse(id)
		if err != nil {
			return nil, err
		}
		if sourceName != "lcsc" {
			value = sourceName + ":" + value
		}
		canonical[i] = value
	}
	return canonical, nil
}

func (a *App) addOne(ctx context.Context, id string, opts library.Options, no3D bool) AddResult {
	if strings.HasPrefix(id, "github:") {
		return a.addGitHub(ctx, strings.TrimPrefix(id, "github:"), id, opts)
	}
	processed := add.Process(ctx, id, a.source, opts, no3D)
	result := AddResult{PartID: id, Symbol: processed.Library.Symbol, Footprint: processed.Library.Footprint, Model: processed.Library.Model, Warnings: append([]string{}, processed.UserWarnings...)}
	for _, warningErr := range processed.WarningErrors {
		a.logFailure("add", id, warningErr)
	}
	if processed.Err != nil {
		result.ErrorKind = string(failure.Classify(processed.Err))
		result.Error = a.reportError("add", id, sourceForPartID(id), opts.Out, processed.Err).Error()
	}
	if result.Symbol == "" {
		result.Symbol = "failed"
	}
	if result.Footprint == "" {
		result.Footprint = "failed"
	}
	if result.Model == "" {
		result.Model = "skipped"
	}
	return result
}

func sourceForPartID(id string) string {
	if strings.HasPrefix(id, "easyeda:") {
		return "easyeda"
	}
	return "lcsc"
}

func (a *App) addGitHub(ctx context.Context, fullName, partID string, opts library.Options) AddResult {
	result := AddResult{PartID: partID, Symbol: "failed", Footprint: "failed", Model: "skipped", Warnings: []string{}}
	contents, err := a.github.Contents(ctx, fullName)
	if err != nil {
		result.ErrorKind = string(failure.Classify(err))
		result.Error = a.reportError("add", partID, "github", opts.Out, err).Error()
		return result
	}
	dir, err := os.MkdirTemp("", "partfetch-github-add-*")
	if err != nil {
		result.ErrorKind = string(failure.Classify(err))
		result.Error = a.reportError("add", partID, "github", opts.Out, err).Error()
		return result
	}
	defer os.RemoveAll(dir)
	if err = a.github.Download(ctx, contents, dir); err != nil {
		result.ErrorKind = string(failure.Classify(err))
		result.Error = a.reportError("add", partID, "github", opts.Out, err).Error()
		return result
	}
	imports := importer.ImportWith(opts, []string{dir}, a.legacyConverter(ctx))
	if len(imports) == 0 {
		err := fmt.Errorf("%w: GitHub repo import returned no results", failure.ErrBadData)
		result.ErrorKind = string(failure.Classify(err))
		result.Error = a.reportError("add", partID, "github", opts.Out, err).Error()
		return result
	}
	imported := imports[0]
	imported.Source = partID
	result.Symbol = aggregateImportStatus(imported.Symbols)
	result.Footprint = aggregateImportStatus(imported.Footprints)
	result.Model = aggregateImportStatus(imported.Models)
	result.Warnings = append(result.Warnings, fmt.Sprintf("符號 %d · 封裝 %d · 3D %d", len(imported.Symbols), len(imported.Footprints), len(imported.Models)))
	result.Warnings = append(result.Warnings, imported.UserWarnings...)
	result.Error, result.ErrorKind = a.importMessage("add", partID, "github", opts.Out, imported)
	return result
}

func aggregateImportStatus(items []importer.Item) string {
	if len(items) == 0 {
		return "skipped"
	}
	for _, status := range []string{"failed", "added", "updated", "exists"} {
		for _, item := range items {
			if item.Status == status {
				return status
			}
		}
	}
	return "skipped"
}
func libraryOptions(s Settings) (library.Options, error) {
	out, err := filepath.Abs(s.OutDir)
	if err != nil {
		return library.Options{}, errors.New("元件庫目錄無效")
	}
	base := s.ModelBase
	switch base {
	case "", "absolute":
		base = out
	case "kiprjmod":
		base = "${KIPRJMOD}/" + filepath.Base(filepath.Clean(out))
	}
	return library.Options{Out: out, Name: s.LibName, ModelBase: base, Overwrite: s.Overwrite}, nil
}
func (a *App) ListLibrary() ([]LibraryEntry, error) {
	s, err := a.GetSettings()
	if err != nil {
		return nil, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return nil, a.reportError("list", "", "", s.OutDir, err)
	}
	entries, err := library.List(opts)
	if err != nil {
		return nil, a.reportError("list", "", "", opts.Out, err)
	}
	out := make([]LibraryEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, LibraryEntry{Name: entry.Name, Footprint: entry.Footprint, LCSC: entry.LCSC, PartID: entry.PartID, HasFootprint: entry.HasFootprint, HasModel: entry.HasModel})
	}
	return out, nil
}
func (a *App) RemoveFromLibrary(names []string) ([]RemoveResult, error) {
	if len(names) == 0 {
		return []RemoveResult{}, errors.New("請先選擇要移除的項目")
	}
	s, err := a.GetSettings()
	if err != nil {
		return []RemoveResult{}, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return []RemoveResult{}, a.reportError("remove", strings.Join(names, ", "), "", s.OutDir, err)
	}
	results, err := library.Remove(opts, names)
	if err != nil {
		return []RemoveResult{}, a.reportError("remove", strings.Join(names, ", "), "", opts.Out, err)
	}
	out := make([]RemoveResult, 0, len(results))
	for _, result := range results {
		message := result.Error
		if message != "" {
			err := result.Cause
			if err == nil {
				err = fmt.Errorf("%w: %s", failure.ErrBadData, message)
			}
			message = a.reportError("remove", result.Name, "", opts.Out, err).Error()
		}
		out = append(out, RemoveResult{Name: result.Name, Symbol: result.Symbol, Footprint: result.Footprint, Model: result.Model, Error: message})
	}
	return out, nil
}
func (a *App) OpenInFileManager(path string) error {
	s, err := a.GetSettings()
	if err != nil {
		return err
	}
	root, err := filepath.Abs(s.OutDir)
	if err != nil {
		return errors.New("元件庫目錄無效")
	}
	target := path
	if target == "" {
		target = root
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return errors.New("目錄路徑無效")
	}
	root, err = resolveExistingPath(root)
	if err != nil {
		return errors.New("元件庫目錄無效")
	}
	target, err = resolveExistingPath(target)
	if err != nil {
		return errors.New("目錄路徑無效")
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("只允許開啟元件庫目錄內的路徑")
	}
	if err = a.openPath(a.ctx, target); err != nil {
		return errors.New("無法在檔案管理員中開啟目錄")
	}
	return nil
}

func resolveExistingPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(path)
	var tail []string
	for {
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", resolveErr
		}
		tail = append(tail, filepath.Base(candidate))
		candidate = parent
	}
}
func (a *App) OpenURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return errors.New("只允許開啟 HTTPS 網址")
	}
	if err = a.openURL(a.ctx, u.String()); err != nil {
		return errors.New("無法開啟網址")
	}
	return nil
}
func systemOpenPath(_ context.Context, path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch file manager: %w", err)
	}
	return nil
}
