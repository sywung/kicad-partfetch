// Package cli implements the partfetch command line interface.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/add"
	"github.com/sywung/kicad-partfetch/internal/importer"
	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/partid"
	"github.com/sywung/kicad-partfetch/internal/search"
	"github.com/sywung/kicad-partfetch/internal/source"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

type GitHubClient interface {
	Search(context.Context, string, int) ([]gh.Repo, int, error)
	Contents(context.Context, string) (gh.Contents, error)
	Download(context.Context, gh.Contents, string) error
}

type Options struct {
	GitHub         GitHubClient
	KiCadCLI       kicadcli.Client
	UpgradeSymbols func(context.Context, string, []byte, []byte) ([]byte, error)
}

type output struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"`
	Symbol    string   `json:"symbol"`
	Footprint string   `json:"footprint"`
	Model     string   `json:"model"`
	Warnings  []string `json:"warnings,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type listOutput struct {
	Name         string `json:"name"`
	Footprint    string `json:"footprint"`
	LCSC         string `json:"lcsc"`
	HasFootprint bool   `json:"hasFootprint"`
	HasModel     bool   `json:"hasModel"`
}

type removeOutput struct {
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	Footprint string `json:"footprint"`
	Model     string `json:"model"`
	Error     string `json:"error,omitempty"`
}

type importOutput struct {
	Source     string             `json:"source"`
	Symbols    []importItemOutput `json:"symbols"`
	Footprints []importItemOutput `json:"footprints"`
	Models     []importItemOutput `json:"models"`
	Warnings   []string           `json:"warnings,omitempty"`
	Error      string             `json:"error,omitempty"`
}

type importItemOutput struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func Run(ctx context.Context, args []string, src source.Source, stdout, stderr io.Writer) int {
	options := Options{GitHub: gh.New(gh.Options{})}
	return RunWithOptions(ctx, args, src, options, stdout, stderr)
}

func RunWithOptions(ctx context.Context, args []string, src source.Source, options Options, stdout, stderr io.Writer) int {
	if options.GitHub == nil {
		options.GitHub = gh.New(gh.Options{})
	}
	if options.KiCadCLI == nil {
		options.KiCadCLI = kicadcli.SystemClient{}
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: partfetch <add|info|list|remove|import|search> ...")
		return 2
	}
	switch args[0] {
	case "add":
		return runAddWithGitHub(ctx, args[1:], src, options.GitHub, options.KiCadCLI, options.UpgradeSymbols, stdout, stderr)
	case "info":
		return runInfoWithGitHub(ctx, args[1:], src, options.GitHub, stdout, stderr)
	case "list":
		return runList(args[1:], stdout, stderr)
	case "remove":
		return runRemove(args[1:], stdout, stderr)
	case "import":
		return runImport(ctx, args[1:], options.KiCadCLI, options.UpgradeSymbols, stdout, stderr)
	case "search":
		searchOptions := search.Options{}
		if transport, ok := src.(http.RoundTripper); ok {
			searchOptions.Transport = transport
		}
		return runSearchWithGitHub(ctx, args[1:], search.New(searchOptions), options.GitHub, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

func runImport(ctx context.Context, args []string, cli kicadcli.Client, upgrade func(context.Context, string, []byte, []byte) ([]byte, error), stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", ".", "output directory")
	lib := flags.String("lib", "partfetch", "library name")
	overwrite := flags.Bool("overwrite", false, "replace existing items")
	modelBase := flags.String("model-base", "", "3D model path prefix")
	jsonOut := flags.Bool("json", false, "print JSON results")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	paths := flags.Args()
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "import requires one or more files or directories")
		return 2
	}
	convert := legacyConverter(ctx, cli, upgrade, "")
	results := importer.ImportWith(library.Options{Out: *out, Name: *lib, ModelBase: *modelBase, Overwrite: *overwrite}, paths, convert)
	failed := false
	for _, result := range results {
		if err := writeImportResult(stdout, result, *jsonOut); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		failed = failed || importResultFailed(result)
	}
	if failed {
		return 1
	}
	return 0
}

func legacyConverter(ctx context.Context, cli kicadcli.Client, upgrade func(context.Context, string, []byte, []byte) ([]byte, error), configured string) importer.LegacyConverter {
	executable, err := cli.Find(configured)
	if err != nil {
		return nil
	}
	if upgrade == nil {
		upgrade = kicadcli.UpgradeSymbolLibrary
	}
	return func(lib, dcm []byte) ([]byte, error) { return upgrade(ctx, executable, lib, dcm) }
}

func writeImportResult(stdout io.Writer, result importer.Result, jsonOut bool) error {
	if jsonOut {
		return writeImportJSON(stdout, result)
	}
	writeImportText(stdout, result)
	return nil
}

func writeImportJSON(stdout io.Writer, result importer.Result) error {
	value := importOutput{Source: result.Source, Symbols: importItems(result.Symbols), Footprints: importItems(result.Footprints), Models: importItems(result.Models), Warnings: result.Warnings, Error: result.Error}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, string(encoded))
	return err
}

func writeImportText(stdout io.Writer, result importer.Result) {
	fmt.Fprintf(stdout, "%s:\n", result.Source)
	writeImportItems(stdout, "symbol", result.Symbols)
	writeImportItems(stdout, "footprint", result.Footprints)
	writeImportItems(stdout, "model", result.Models)
	for _, warning := range result.Warnings {
		fmt.Fprintf(stdout, "  warning: %s\n", warning)
	}
	if result.Error != "" {
		fmt.Fprintf(stdout, "  error: %s\n", result.Error)
	}
}

func writeImportItems(stdout io.Writer, kind string, items []importer.Item) {
	for _, item := range items {
		fmt.Fprintf(stdout, "  %s %s: %s\n", kind, item.Name, item.Status)
	}
}

func importResultFailed(result importer.Result) bool {
	if result.Error != "" {
		return true
	}
	for _, items := range [][]importer.Item{result.Symbols, result.Footprints, result.Models} {
		for _, item := range items {
			if item.Status == "failed" {
				return true
			}
		}
	}
	return false
}

func importItems(items []importer.Item) []importItemOutput {
	values := make([]importItemOutput, 0, len(items))
	for _, item := range items {
		values = append(values, importItemOutput{Name: item.Name, Status: item.Status})
	}
	return values
}

func runList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", ".", "output directory")
	lib := flags.String("lib", "partfetch", "library name")
	jsonOut := flags.Bool("json", false, "print JSON results")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	entries, err := library.List(library.Options{Out: *out, Name: *lib})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *jsonOut {
		values := make([]listOutput, 0, len(entries))
		for _, entry := range entries {
			values = append(values, listOutput{Name: entry.Name, Footprint: entry.Footprint, LCSC: entry.LCSC, HasFootprint: entry.HasFootprint, HasModel: entry.HasModel})
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, string(encoded))
		return 0
	}
	for _, entry := range entries {
		footprint, model := "no", "no"
		if entry.HasFootprint {
			footprint = "yes"
		}
		if entry.HasModel {
			model = "yes"
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\tfootprint=%s\tmodel=%s\n", entry.Name, entry.Footprint, entry.LCSC, footprint, model)
	}
	return 0
}

func runRemove(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("remove", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", ".", "output directory")
	lib := flags.String("lib", "partfetch", "library name")
	jsonOut := flags.Bool("json", false, "print JSON results")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	names := flags.Args()
	if len(names) == 0 {
		fmt.Fprintln(stderr, "remove requires one or more symbol names or part numbers")
		return 2
	}
	results, err := library.Remove(library.Options{Out: *out, Name: *lib}, names)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	failed := false
	for _, result := range results {
		if result.Symbol != "removed" || result.Footprint == "failed" || result.Model == "failed" {
			failed = true
		}
		if *jsonOut {
			value := removeOutput{Name: result.Name, Symbol: result.Symbol, Footprint: result.Footprint, Model: result.Model, Error: result.Error}
			encoded, marshalErr := json.Marshal(value)
			if marshalErr != nil {
				fmt.Fprintln(stderr, marshalErr)
				return 1
			}
			fmt.Fprintln(stdout, string(encoded))
			continue
		}
		fmt.Fprintf(stdout, "%s: symbol=%s footprint=%s model=%s", result.Name, result.Symbol, result.Footprint, result.Model)
		if result.Error != "" {
			fmt.Fprintf(stdout, " error=%s", result.Error)
		}
		fmt.Fprintln(stdout)
	}
	if failed {
		return 1
	}
	return 0
}

func runAdd(ctx context.Context, args []string, src source.Source, stdout, stderr io.Writer) int {
	return runAddWithGitHub(ctx, args, src, gh.New(gh.Options{}), kicadcli.SystemClient{}, nil, stdout, stderr)
}

func runAddWithGitHub(ctx context.Context, args []string, src source.Source, github GitHubClient, cli kicadcli.Client, upgrade func(context.Context, string, []byte, []byte) ([]byte, error), stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", ".", "output directory")
	lib := flags.String("lib", "partfetch", "library name")
	overwrite := flags.Bool("overwrite", false, "replace existing items")
	no3d := flags.Bool("no-3d", false, "skip 3D model download")
	obj := flags.Bool("obj", false, "also save the OBJ model")
	modelBase := flags.String("model-base", "", "3D model path prefix")
	noCache := flags.Bool("no-cache", false, "disable cache")
	jsonOut := flags.Bool("json", false, "print JSON results")
	if e := flags.Parse(args); e != nil {
		return 2
	}
	ids := flags.Args()
	if len(ids) == 0 {
		fmt.Fprintln(stderr, "add requires one or more part numbers")
		return 2
	}
	failed := false
	if cache, ok := src.(interface{ SetNoCache(bool) }); ok && *noCache {
		cache.SetNoCache(true)
	}
	opts := library.Options{Out: *out, Name: *lib, ModelBase: *modelBase, Overwrite: *overwrite, SaveOBJ: *obj}
	for _, id := range ids {
		if addOne(ctx, id, src, github, cli, upgrade, opts, *no3d, stdout, *jsonOut) {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

func addOne(ctx context.Context, id string, src source.Source, github GitHubClient, cli kicadcli.Client, upgrade func(context.Context, string, []byte, []byte) ([]byte, error), opts library.Options, no3D bool, stdout io.Writer, jsonOut bool) bool {
	sourceName, value, err := partid.Parse(id)
	if err != nil {
		writeResult(stdout, output{ID: id, Symbol: "failed", Footprint: "failed", Model: "skipped", Error: err.Error()}, jsonOut)
		return true
	}
	if sourceName == "github" {
		return runGitHubAdd(ctx, value, github, legacyConverter(ctx, cli, upgrade, ""), opts, stdout, jsonOut) != 0
	}
	if sourceName == "easyeda" {
		value = "easyeda:" + value
	}
	result := process(ctx, value, src, opts, no3D)
	writeResult(stdout, result, jsonOut)
	return result.Error != ""
}

func runGitHubAdd(ctx context.Context, fullName string, client GitHubClient, convert importer.LegacyConverter, opts library.Options, stdout io.Writer, jsonOut bool) int {
	partID := "github:" + fullName
	result := importer.Result{Source: partID, Symbols: []importer.Item{}, Footprints: []importer.Item{}, Models: []importer.Item{}, Warnings: []string{}}
	contents, err := client.Contents(ctx, fullName)
	if err != nil {
		result.Error = err.Error()
		writeImportTextOrJSON(stdout, result, jsonOut)
		return 1
	}
	dir, err := os.MkdirTemp("", "partfetch-github-add-*")
	if err != nil {
		result.Error = err.Error()
		writeImportTextOrJSON(stdout, result, jsonOut)
		return 1
	}
	defer os.RemoveAll(dir)
	if err = client.Download(ctx, contents, dir); err != nil {
		result.Error = err.Error()
		writeImportTextOrJSON(stdout, result, jsonOut)
		return 1
	}
	imports := importer.ImportWith(opts, []string{dir}, convert)
	if len(imports) > 0 {
		result = imports[0]
		result.Source = partID
	}
	writeImportTextOrJSON(stdout, result, jsonOut)
	if importResultFailed(result) {
		return 1
	}
	return 0
}

func writeImportTextOrJSON(stdout io.Writer, result importer.Result, jsonOut bool) {
	if err := writeImportResult(stdout, result, jsonOut); err != nil {
		fmt.Fprintln(stdout, err)
	}
}

func printGitHubInfo(ctx context.Context, fullName string, client GitHubClient, stdout, stderr io.Writer) int {
	contents, err := client.Contents(ctx, fullName)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Repository: %s\nDescription: %s\nLicense: %s\nStars: %d\nURL: %s\nCommit: %s\n", contents.Repo.FullName, contents.Repo.Description, licenseLabel(contents.Repo.License), contents.Repo.Stars, contents.Repo.URL, contents.Repo.SHA)
	printGitHubFiles(stdout, "Symbols", contents.Symbols)
	printGitHubFiles(stdout, "Footprints", contents.Footprints)
	printGitHubFiles(stdout, "Models", contents.Models)
	printGitHubFiles(stdout, "Legacy files", contents.LegacyFiles)
	fmt.Fprintf(stdout, "Legacy: %d\nTruncated: %t\n", contents.Legacy, contents.Truncated)
	return 0
}

func printGitHubFiles(stdout io.Writer, label string, files []gh.File) {
	fmt.Fprintf(stdout, "%s (%d):\n", label, len(files))
	for _, file := range files {
		fmt.Fprintf(stdout, "  %s\t%d bytes\n", file.Path, file.Size)
	}
}

func licenseLabel(value string) string {
	if value == "" {
		return "未聲明授權"
	}
	return value
}

func process(ctx context.Context, id string, src source.Source, opts library.Options, no3d bool) output {
	processed := add.Process(ctx, id, src, opts, no3d)
	r := output{ID: id, Title: processed.Title, Symbol: processed.Library.Symbol, Footprint: processed.Library.Footprint, Model: processed.Library.Model, Warnings: processed.Library.Warnings}
	if processed.Err != nil {
		r.Error = processed.Err.Error()
	}
	if r.Symbol == "" {
		r.Symbol = "failed"
	}
	if r.Footprint == "" {
		r.Footprint = "failed"
	}
	if r.Model == "" {
		r.Model = "skipped"
	}
	return r
}

func runInfo(ctx context.Context, args []string, src source.Source, stdout, stderr io.Writer) int {
	return runInfoWithGitHub(ctx, args, src, gh.New(gh.Options{}), stdout, stderr)
}

func runInfoWithGitHub(ctx context.Context, args []string, src source.Source, github GitHubClient, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: partfetch info C2040")
		return 2
	}
	sourceName, canonicalID, parseErr := partid.Parse(args[0])
	if parseErr != nil {
		fmt.Fprintln(stderr, parseErr)
		return 1
	}
	if sourceName == "github" {
		return printGitHubInfo(ctx, canonicalID, github, stdout, stderr)
	}
	if sourceName == "easyeda" {
		canonicalID = "easyeda:" + canonicalID
	}
	c, e := src.Fetch(ctx, canonicalID)
	if e != nil {
		fmt.Fprintln(stderr, e)
		return 1
	}
	pins := 0
	for _, u := range c.Units {
		pins += len(u.Pins)
	}
	has3d := len(c.Footprint.Models) > 0
	fmt.Fprintf(stdout, "Name: %s\nPackage: %s\nPins: %d\nPads: %d\n3D: %t\n", c.Value, c.Footprint.Name, pins, len(c.Footprint.Pads), has3d)
	return 0
}

type cliSearchResult struct {
	Total int             `json:"total"`
	Items []cliSearchItem `json:"items"`
}

type cliSearchItem struct {
	PartID       string `json:"partID"`
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	Package      string `json:"package"`
	Description  string `json:"description"`
	Stock        int    `json:"stock"`
	Price        string `json:"price"`
	ImageURL     string `json:"imageURL"`
	DatasheetURL string `json:"datasheetURL"`
	Source       string `json:"source"`
	URL          string `json:"url"`
	License      string `json:"license"`
	Stars        int    `json:"stars"`
}

func runSearch(ctx context.Context, args []string, client search.Client, stdout, stderr io.Writer) int {
	return runSearchWithGitHub(ctx, args, client, nil, stdout, stderr)
}

func runSearchWithGitHub(ctx context.Context, args []string, client search.Client, github GitHubClient, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	flags.SetOutput(stderr)
	sourceName := flags.String("source", "lcsc", "search source")
	page := flags.Int("page", 1, "result page")
	jsonOut := flags.Bool("json", false, "print JSON results")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	query := strings.Join(flags.Args(), " ")
	if strings.TrimSpace(query) == "" {
		fmt.Fprintln(stderr, "search requires a query")
		return 2
	}
	if *sourceName == "github" {
		if github == nil {
			github = gh.New(gh.Options{})
		}
		repos, total, err := github.Search(ctx, query, *page)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result, err := githubSearchResult(repos, total, *page)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return writeSearchResult(stdout, result, *jsonOut)
	}
	if *sourceName != "lcsc" && *sourceName != "easyeda" {
		fmt.Fprintf(stderr, "invalid source %q\n", *sourceName)
		return 1
	}
	var result search.Result
	var err error
	if *sourceName == "easyeda" {
		community, ok := client.(interface {
			Community(context.Context, string, int) (search.Result, error)
		})
		if !ok {
			err = fmt.Errorf("EasyEDA 社群搜尋不可用")
		} else {
			result, err = community.Community(ctx, query, *page)
		}
	} else {
		result, err = client.Search(ctx, query, *page)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return writeSearchResult(stdout, result, *jsonOut)
}

func githubSearchResult(repos []gh.Repo, total, page int) (search.Result, error) {
	items := make([]search.Item, 0, len(repos))
	for _, repo := range repos {
		_, fullName, err := partid.Parse("github:" + repo.FullName)
		if err != nil {
			return search.Result{}, fmt.Errorf("GitHub returned an invalid repository identifier")
		}
		owner := strings.SplitN(fullName, "/", 2)[0]
		items = append(items, search.Item{PartID: "github:" + fullName, MPN: fullName, Manufacturer: owner, Description: repo.Description, Source: "github", URL: repo.URL, License: repo.License, Stars: repo.Stars})
	}
	return search.Result{Items: items, Total: total, Page: page}, nil
}

func writeSearchResult(stdout io.Writer, result search.Result, jsonOut bool) int {
	if jsonOut {
		encoded := cliSearchResult{Total: result.Total, Items: make([]cliSearchItem, 0, len(result.Items))}
		for _, item := range result.Items {
			encoded.Items = append(encoded.Items, cliSearchItem{PartID: item.PartID, MPN: item.MPN, Manufacturer: item.Manufacturer, Package: item.Package, Description: item.Description, Stock: item.Stock, Price: item.Price, ImageURL: item.ImageURL, DatasheetURL: item.DatasheetURL, Source: item.Source, URL: item.URL, License: item.License, Stars: item.Stars})
		}
		data, err := json.Marshal(encoded)
		if err != nil {
			fmt.Fprintln(stdout, err)
			return 1
		}
		fmt.Fprintln(stdout, string(data))
		return 0
	}
	for _, item := range result.Items {
		name := item.MPN
		packageOrStars := item.Package
		if item.Source == "github" {
			packageOrStars = fmt.Sprintf("★%d", item.Stars)
		} else if item.Stars > 0 {
			packageOrStars = fmt.Sprintf("★%d", item.Stars)
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", item.PartID, name, item.Manufacturer, packageOrStars, item.Description)
	}
	fmt.Fprintf(stdout, "total: %d\n", result.Total)
	return 0
}

func writeResult(w io.Writer, r output, jsonOut bool) {
	if jsonOut {
		b, e := json.Marshal(r)
		if e != nil {
			fmt.Fprintf(w, "{\"id\":%q,\"error\":%q}\n", r.ID, e.Error())
			return
		}
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintf(w, "%s: symbol=%s footprint=%s model=%s", r.ID, r.Symbol, r.Footprint, r.Model)
	if r.Error != "" {
		fmt.Fprintf(w, " error=%s", r.Error)
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, " warnings=%s", strings.Join(r.Warnings, "; "))
	}
	fmt.Fprintln(w)
}
