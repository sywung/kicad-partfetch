package importer

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/failure"
	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/library"
)

const (
	maxFileBytes  = int64(64 << 20)
	maxBundleSize = int64(512 << 20)
	maxItems      = 5000
)

type Item struct{ Name, Status string }
type LegacyConverter func(lib, dcm []byte) ([]byte, error)
type Result struct {
	Source       string
	Symbols      []Item
	Footprints   []Item
	Models       []Item
	Warnings     []string
	UserWarnings []string `json:"-"`
	Error        string
	Err          error `json:"-"`
}

type limits struct {
	file, bundle int64
	items        int
}
type sourceFile struct {
	path, name, origName, ext string
	data                      []byte
}
type bundle struct {
	files     []sourceFile
	legacy    []sourceFile
	oldFormat bool
}
type footprint struct {
	source, name, status string
	data                 []byte
}

func Import(opts library.Options, paths []string) []Result {
	return ImportWith(opts, paths, nil)
}

func ImportWith(opts library.Options, paths []string, convert LegacyConverter) []Result {
	return importWithConverterLimits(opts, paths, convert, defaultsLimits())
}

func importWithLimits(opts library.Options, paths []string, caps limits) []Result {
	return importWithConverterLimits(opts, paths, nil, caps)
}

func importWithConverterLimits(opts library.Options, paths []string, convert LegacyConverter, caps limits) []Result {
	results := make([]Result, 0, len(paths))
	for _, path := range paths {
		result := emptyResult(path)
		processBundle(opts, path, convert, caps, &result)
		results = append(results, result)
	}
	return results
}

func emptyResult(source string) Result {
	return Result{Source: source, Symbols: []Item{}, Footprints: []Item{}, Models: []Item{}, Warnings: []string{}, UserWarnings: []string{}}
}

func appendWarning(result *Result, output, user string) {
	result.Warnings = append(result.Warnings, output)
	result.UserWarnings = append(result.UserWarnings, user)
}

func defaultsLimits() limits {
	return limits{file: maxFileBytes, bundle: maxBundleSize, items: maxItems}
}

func processBundle(opts library.Options, path string, convert LegacyConverter, caps limits, result *Result) {
	input, err := collect(path, caps)
	if err != nil {
		result.Error = err.Error()
		result.Err = err
		return
	}
	input = convertLegacy(input, convert, result)
	if len(input.files) == 0 {
		if input.oldFormat || len(input.legacy) > 0 {
			result.Error = "只有舊版 KiCad 格式（.lib／.mod），請先用 KiCad 另存為新格式"
		} else {
			result.Error = "找不到可匯入的 KiCad 檔案"
		}
		return
	}
	if input.oldFormat {
		appendWarning(result, "忽略舊版 KiCad 格式檔案", "忽略舊版 KiCad 格式檔案")
	}
	writeOpts, lib, err := normalizeOptions(opts)
	if err != nil {
		result.Error = err.Error()
		result.Err = err
		return
	}
	prepared := prepareFiles(input, writeOpts, result)
	if result.Error != "" {
		if result.Err == nil {
			result.Err = failure.ErrBadData
		}
		return
	}
	wrote := writeModels(writeOpts, lib, prepared.models, result)
	wrote = writeFootprints(writeOpts, lib, prepared.footprints, result) || wrote
	symbolWrote, parseFailed := writeSymbols(writeOpts, lib, prepared.symbols, prepared.versions, result)
	wrote = wrote || symbolWrote
	if (parseFailed || prepared.parseFailed) && !wrote {
		result.Error = "符號庫解析失敗，沒有匯入任何檔案"
		appendResultError(result, failure.ErrBadData)
	}
	if result.Err == nil && hasFailedItems(result) {
		result.Err = failure.ErrBadData
	}
	// A converted legacy library can be empty; a bundle that yields no items at all is not a success.
	if result.Error == "" && len(result.Symbols)+len(result.Footprints)+len(result.Models) == 0 {
		result.Error = "找不到可匯入的 KiCad 檔案"
		result.Err = os.ErrNotExist
	}
}

func convertLegacy(input bundle, convert LegacyConverter, result *Result) bundle {
	input.oldFormat = false
	libs := make([]sourceFile, 0)
	for _, file := range input.legacy {
		if file.ext == ".lib" {
			libs = append(libs, file)
		}
	}
	for _, file := range libs {
		input = convertLegacyFile(input, file, convert, result)
	}
	for _, file := range input.legacy {
		if file.ext == ".mod" || file.ext == ".dcm" {
			input.oldFormat = true
		}
	}
	sort.SliceStable(input.files, func(i, j int) bool { return input.files[i].path < input.files[j].path })
	return input
}

func convertLegacyFile(input bundle, file sourceFile, convert LegacyConverter, result *Result) bundle {
	if matchingSymbol(input.files, file) != nil {
		if dcm := matchingDcm(input.legacy, file); dcm != nil {
			input.legacy = removeLegacy(input.legacy, dcm.path)
		}
		return input
	}
	dcm := matchingDcm(input.legacy, file)
	if convert == nil {
		warning := "發現舊版符號庫 " + file.name + "，但找不到 kicad-cli，無法自動轉換"
		appendWarning(result, warning, warning)
		input.oldFormat = true
		return input
	}
	converted, err := convert(file.data, legacyDCMData(dcm))
	if err != nil {
		appendResultError(result, fmt.Errorf("%w: %w", failure.ErrBadData, err))
		appendWarning(result, "舊版符號庫 "+file.name+" 轉換失敗："+err.Error(), "舊版符號庫 "+file.name+" 轉換失敗")
		input.oldFormat = true
		return input
	}
	virtual := convertedSymbolFile(file, converted)
	input.files = append(input.files, virtual)
	warning := "已用 kicad-cli 將舊版符號庫 " + file.name + " 轉為新格式"
	appendWarning(result, warning, warning)
	if dcm != nil {
		input.legacy = removeLegacy(input.legacy, dcm.path)
	}
	return input
}

func legacyDCMData(dcm *sourceFile) []byte {
	if dcm == nil {
		return nil
	}
	return dcm.data
}

func convertedSymbolFile(file sourceFile, data []byte) sourceFile {
	file.path = strings.TrimSuffix(file.path, filepath.Ext(file.path)) + ".kicad_sym"
	file.name = strings.TrimSuffix(file.name, filepath.Ext(file.name)) + ".kicad_sym"
	file.origName, file.ext, file.data = file.name, ".kicad_sym", data
	return file
}

func matchingSymbol(files []sourceFile, legacy sourceFile) *sourceFile {
	dir, stem := filepath.ToSlash(filepath.Dir(legacy.path)), strings.TrimSuffix(filepath.Base(legacy.path), filepath.Ext(legacy.path))
	for i := range files {
		candidateDir := filepath.ToSlash(filepath.Dir(files[i].path))
		candidateStem := strings.TrimSuffix(filepath.Base(files[i].path), filepath.Ext(files[i].path))
		if files[i].ext == ".kicad_sym" && dir == candidateDir && strings.EqualFold(stem, candidateStem) {
			return &files[i]
		}
	}
	return nil
}

func matchingDcm(files []sourceFile, lib sourceFile) *sourceFile {
	dir, stem := filepath.ToSlash(filepath.Dir(lib.path)), strings.TrimSuffix(filepath.Base(lib.path), filepath.Ext(lib.path))
	for i := range files {
		candidateDir := filepath.ToSlash(filepath.Dir(files[i].path))
		candidateStem := strings.TrimSuffix(filepath.Base(files[i].path), filepath.Ext(files[i].path))
		if files[i].ext == ".dcm" && dir == candidateDir && strings.EqualFold(stem, candidateStem) {
			return &files[i]
		}
	}
	return nil
}

func removeLegacy(files []sourceFile, path string) []sourceFile {
	for i := range files {
		if files[i].path == path {
			return append(files[:i], files[i+1:]...)
		}
	}
	return files
}

type preparedBundle struct {
	models      []sourceFile
	footprints  []footprint
	symbols     []kicad.ImportSymbol
	versions    []int
	parseFailed bool
}

func prepareFiles(input bundle, opts library.Options, result *Result) preparedBundle {
	var prepared preparedBundle
	var rawFootprints, models []sourceFile
	seenFootprints, seenModels := make(map[string]bool), make(map[string]bool)
	footprintFileCount := 0
	for _, file := range input.files {
		switch file.ext {
		case ".step", ".stp", ".wrl":
			file.origName = file.name
			name := kicad.Sanitize(strings.TrimSuffix(filepath.Base(file.name), filepath.Ext(file.name))) + filepath.Ext(file.name)
			if seenModels[name] {
				warning := "3D 模型目的地檔名重複，略過 " + file.path
				appendWarning(result, warning, warning)
				continue
			}
			seenModels[name] = true
			file.name = name
			models = append(models, file)
		case ".kicad_mod":
			footprintFileCount++
			name := kicad.Sanitize(strings.TrimSuffix(filepath.Base(file.name), filepath.Ext(file.name)))
			if seenFootprints[name] {
				warning := "封裝目的地檔名重複，略過 " + file.path
				appendWarning(result, warning, warning)
				continue
			}
			seenFootprints[name] = true
			file.name = name
			rawFootprints = append(rawFootprints, file)
		case ".kicad_sym":
			symbols, err := kicad.ImportSymbols(file.data)
			if err != nil {
				prepared.parseFailed = true
				appendResultError(result, fmt.Errorf("%w: %w", failure.ErrBadData, err))
				appendWarning(result, "符號庫 "+file.path+" 解析失敗："+err.Error(), "符號庫 "+file.path+" 解析失敗")
				continue
			}
			version := kicad.LibraryVersion(file.data)
			if version > 20231120 {
				warning := fmt.Sprintf("符號格式版本 %d 較新，KiCad 8／9 可能無法開啟此元件庫", version)
				appendWarning(result, warning, warning)
			}
			prepared.versions = append(prepared.versions, version)
			prepared.symbols = append(prepared.symbols, symbols...)
		}
	}
	prepared.models = models
	prepared.footprints = prepareFootprints(rawFootprints, footprintFileCount, models, opts, result)
	prepared.symbols = rewriteImportedSymbols(prepared.symbols, prepared.footprints, opts.Name, result)
	return prepared
}

func hasFailedItems(result *Result) bool {
	for _, items := range [][]Item{result.Symbols, result.Footprints, result.Models} {
		for _, item := range items {
			if item.Status == "failed" {
				return true
			}
		}
	}
	return false
}

func prepareFootprints(files []sourceFile, sourceCount int, models []sourceFile, opts library.Options, result *Result) []footprint {
	prepared := make([]footprint, 0, len(files))
	for _, file := range files {
		if !kicad.FootprintType(file.data) {
			warning := "封裝 " + file.name + " 格式無效，略過"
			appendWarning(result, warning, warning)
			prepared = append(prepared, footprint{source: file.path, name: file.name, status: "failed"})
			continue
		}
		replacements := make(map[string]string)
		paths := kicad.FootprintModelPaths(file.data)
		for _, path := range paths {
			match := findModel(path, models)
			if match == nil {
				warning := fmt.Sprintf("封裝 %s 的 3D 模型 %s 不在匯入內容中", file.name, path)
				appendWarning(result, warning, warning)
				continue
			}
			replacements[path] = modelPath(opts, match.name)
		}
		data, _, err := kicad.RewriteFootprintModels(file.data, replacements)
		if err != nil {
			appendResultError(result, fmt.Errorf("%w: %w", failure.ErrBadData, err))
			appendWarning(result, "封裝 "+file.name+" 改寫失敗："+err.Error(), "封裝 "+file.name+" 改寫失敗")
			prepared = append(prepared, footprint{source: file.path, name: file.name, status: "failed"})
			continue
		}
		if len(paths) == 0 && sourceCount == 1 && len(models) > 0 {
			match := preferredModel(models)
			path := modelPath(opts, match.name)
			if inserted, ok := kicad.InsertFootprintModel(data, path); ok {
				data = inserted
				warning := "已自動掛上 3D 模型 " + match.name + "，位置未經校正，請在 KiCad 檢查"
				appendWarning(result, warning, warning)
			}
		}
		prepared = append(prepared, footprint{source: file.path, name: file.name, data: data})
	}
	return prepared
}

func rewriteImportedSymbols(symbols []kicad.ImportSymbol, footprints []footprint, libraryName string, result *Result) []kicad.ImportSymbol {
	libraryName = kicad.Sanitize(libraryName)
	values := make(map[string]string, len(symbols))
	for _, symbol := range symbols {
		if !symbol.HasFootprint {
			if len(footprints) == 1 {
				warning := "符號 " + symbol.Name + " 沒有 Footprint 屬性，未自動指定封裝"
				appendWarning(result, warning, warning)
			}
			continue
		}
		ref := symbol.Footprint
		if index := strings.LastIndex(ref, ":"); index >= 0 {
			ref = ref[index+1:]
		}
		if ref == "" && len(footprints) == 1 {
			values[symbol.Name] = libraryName + ":" + footprints[0].name
			continue
		}
		if ref == "" && len(footprints) > 1 {
			if match := findNamedFootprint(symbol.Name, footprints, false); match != nil {
				values[symbol.Name] = libraryName + ":" + match.name
				warning := fmt.Sprintf("符號 %s 未指定封裝，已依名稱配對到 %s，請確認", symbol.Name, match.name)
				appendWarning(result, warning, warning)
				continue
			}
			if match := findNamedFootprint(symbol.Name, footprints, true); match != nil {
				values[symbol.Name] = libraryName + ":" + match.name
				warning := fmt.Sprintf("符號 %s 未指定封裝，已依名稱配對到 %s，請確認", symbol.Name, match.name)
				appendWarning(result, warning, warning)
				continue
			}
		}
		if ref == "" {
			continue
		}
		match := findFootprint(ref, footprints, false)
		if match == nil {
			match = findFootprint(ref, footprints, true)
		}
		if match == nil {
			warning := fmt.Sprintf("符號 %s 的封裝 %s 不在匯入內容中，保留原值", symbol.Name, symbol.Footprint)
			appendWarning(result, warning, warning)
			continue
		}
		values[symbol.Name] = libraryName + ":" + match.name
	}
	for i := range symbols {
		if value, ok := values[symbols[i].Name]; ok {
			updated, err := kicad.RewriteSymbolFootprints(symbols[i].Data, map[string]string{symbols[i].Name: value})
			if err == nil {
				symbols[i].Data = updated
				symbols[i].Footprint = value
			}
		}
	}
	return symbols
}

func findNamedFootprint(name string, footprints []footprint, fold bool) *footprint {
	var match *footprint
	for i := range footprints {
		original := strings.TrimSuffix(filepath.Base(footprints[i].source), filepath.Ext(footprints[i].source))
		matches := name == original || name == footprints[i].name
		if fold {
			matches = strings.EqualFold(name, original) || strings.EqualFold(name, footprints[i].name)
		}
		if matches {
			if match != nil {
				return nil
			}
			match = &footprints[i]
		}
	}
	return match
}

func findFootprint(ref string, footprints []footprint, fold bool) *footprint {
	for i := range footprints {
		stem := footprints[i].name
		original := strings.TrimSuffix(filepath.Base(footprints[i].source), filepath.Ext(footprints[i].source))
		if (!fold && (ref == original || ref == stem)) || (fold && (strings.EqualFold(ref, original) || strings.EqualFold(ref, stem))) {
			return &footprints[i]
		}
	}
	return nil
}

func findModel(path string, models []sourceFile) *sourceFile {
	base := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	for _, model := range models {
		if filepath.Base(model.origName) == base {
			return &model
		}
	}
	for _, model := range models {
		if strings.EqualFold(filepath.Base(model.origName), base) {
			return &model
		}
	}
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	var candidates []sourceFile
	for _, model := range models {
		if strings.EqualFold(strings.TrimSuffix(filepath.Base(model.origName), filepath.Ext(model.origName)), stem) {
			candidates = append(candidates, model)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sortModels(candidates)
	return &candidates[0]
}

func preferredModel(models []sourceFile) *sourceFile {
	candidates := append([]sourceFile(nil), models...)
	sortModels(candidates)
	return &candidates[0]
}

func sortModels(models []sourceFile) {
	rank := func(ext string) int {
		switch strings.ToLower(filepath.Ext(ext)) {
		case ".step":
			return 0
		case ".stp":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		ri, rj := rank(models[i].name), rank(models[j].name)
		if ri != rj {
			return ri < rj
		}
		return models[i].path < models[j].path
	})
}

func modelPath(opts library.Options, name string) string {
	base := strings.TrimRight(opts.ModelBase, "/\\")
	return base + "/" + kicad.Sanitize(opts.Name) + ".3dshapes/" + name
}

func normalizeOptions(opts library.Options) (library.Options, string, error) {
	if opts.Out == "" {
		opts.Out = "."
	}
	if opts.Name == "" {
		opts.Name = "partfetch"
	}
	lib := kicad.Sanitize(opts.Name)
	if opts.ModelBase == "" {
		absolute, err := filepath.Abs(opts.Out)
		if err != nil {
			return opts, "", err
		}
		opts.ModelBase = absolute
	}
	return opts, lib, nil
}

func writeModels(opts library.Options, lib string, models []sourceFile, result *Result) bool {
	wrote := false
	for _, model := range models {
		name := model.name
		path := filepath.Join(opts.Out, lib+".3dshapes", name)
		status, err := writeFile(path, model.data, opts.Overwrite)
		if err != nil {
			appendResultError(result, err)
		}
		warnIfKeptDifferent(result, status, path, model.data, "3D 模型 "+name)
		result.Models = append(result.Models, Item{Name: name, Status: status})
		wrote = wrote || status == "added" || status == "updated"
	}
	return wrote
}

func writeFootprints(opts library.Options, lib string, footprints []footprint, result *Result) bool {
	wrote := false
	for _, item := range footprints {
		if item.status == "failed" {
			result.Footprints = append(result.Footprints, Item{Name: item.name, Status: "failed"})
			continue
		}
		path := filepath.Join(opts.Out, lib+".pretty", item.name+".kicad_mod")
		status, err := writeFile(path, item.data, opts.Overwrite)
		if err != nil {
			appendResultError(result, err)
		}
		warnIfKeptDifferent(result, status, path, item.data, "封裝 "+item.name)
		result.Footprints = append(result.Footprints, Item{Name: item.name, Status: status})
		wrote = wrote || status == "added" || status == "updated"
	}
	return wrote
}

func writeSymbols(opts library.Options, lib string, imported []kicad.ImportSymbol, versions []int, result *Result) (bool, bool) {
	if len(imported) == 0 {
		return false, false
	}
	path := filepath.Join(opts.Out, lib+".kicad_sym")
	existing, existingNames, err := readExistingSymbols(path)
	if err != nil {
		appendResultError(result, err)
		appendSymbolStatuses(result, imported, "failed")
		appendWarning(result, "既有符號庫讀取或解析失敗："+err.Error(), "既有符號庫讀取或解析失敗")
		return false, false
	}
	statuses := symbolStatuses(imported, existingNames, opts.Overwrite)
	valid := validImportedSymbols(imported, statuses, existingNames, result)
	replacements, appended, changed := symbolChanges(imported, statuses, valid)
	for i, symbol := range imported {
		result.Symbols = append(result.Symbols, Item{Name: symbol.Name, Status: statuses[i]})
	}
	if !changed {
		return false, len(versions) == 0
	}
	version := 20231120
	for _, importedVersion := range versions {
		if importedVersion > version {
			version = importedVersion
		}
	}
	merged, err := kicad.AppendSymbols(existing, replacements, appended, version)
	if err != nil {
		appendResultError(result, fmt.Errorf("%w: %w", failure.ErrBadData, err))
		markNewSymbolsFailed(result, imported)
		appendWarning(result, "符號庫合併失敗："+err.Error(), "符號庫合併失敗")
		return false, false
	}
	if len(existing) > 0 {
		if err := atomicWrite(path+".bak", existing); err != nil {
			appendResultError(result, err)
			markNewSymbolsFailed(result, imported)
			return false, false
		}
	}
	if err := atomicWrite(path, merged); err != nil {
		appendResultError(result, err)
		markNewSymbolsFailed(result, imported)
		return false, false
	}
	return true, false
}

func readExistingSymbols(path string) ([]byte, map[string]bool, error) {
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, map[string]bool{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	names := make(map[string]bool)
	if len(bytes.TrimSpace(existing)) == 0 {
		return existing, names, nil
	}
	symbols, err := kicad.ImportSymbols(existing)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", failure.ErrBadData, err)
	}
	for _, symbol := range symbols {
		names[symbol.Name] = true
	}
	return existing, names, nil
}

func symbolStatuses(symbols []kicad.ImportSymbol, existing map[string]bool, overwrite bool) []string {
	statuses := make([]string, len(symbols))
	for i, symbol := range symbols {
		if !existing[symbol.Name] {
			statuses[i] = "added"
		} else if overwrite {
			statuses[i] = "updated"
		} else {
			statuses[i] = "exists"
		}
	}
	return statuses
}

func symbolChanges(symbols []kicad.ImportSymbol, statuses []string, valid []bool) (map[string][]byte, [][]byte, bool) {
	replacements := make(map[string][]byte)
	appended := make([][]byte, 0, len(symbols))
	changed := false
	for i, symbol := range symbols {
		if !valid[i] {
			statuses[i] = "failed"
			continue
		}
		if statuses[i] == "exists" {
			continue
		}
		changed = true
		if statuses[i] == "updated" {
			replacements[symbol.Name] = symbol.Data
		} else {
			appended = append(appended, symbol.Data)
		}
	}
	return replacements, appended, changed
}

func validImportedSymbols(symbols []kicad.ImportSymbol, statuses []string, existing map[string]bool, result *Result) []bool {
	valid := make([]bool, len(symbols))
	byName := make(map[string][]int)
	for i, symbol := range symbols {
		byName[symbol.Name] = append(byName[symbol.Name], i)
	}
	for i, symbol := range symbols {
		if statuses[i] == "exists" {
			valid[i] = true
		}
		if symbol.Extends == "" && statuses[i] != "exists" {
			valid[i] = true
		}
	}
	for pass := 0; pass < len(symbols); pass++ {
		changed := false
		for i, symbol := range symbols {
			if valid[i] || statuses[i] == "exists" || symbol.Extends == "" {
				continue
			}
			if existing[symbol.Extends] || anyValid(byName[symbol.Extends], valid) {
				valid[i], changed = true, true
			}
		}
		if !changed {
			break
		}
	}
	for i, symbol := range symbols {
		if valid[i] {
			continue
		}
		warning := fmt.Sprintf("符號 %s 的父符號 %s 不存在", symbol.Name, symbol.Extends)
		appendWarning(result, warning, warning)
	}
	return valid
}

func anyValid(indices []int, valid []bool) bool {
	for _, index := range indices {
		if valid[index] {
			return true
		}
	}
	return false
}

func appendSymbolStatuses(result *Result, symbols []kicad.ImportSymbol, status string) {
	for _, symbol := range symbols {
		result.Symbols = append(result.Symbols, Item{Name: symbol.Name, Status: status})
	}
}

func markNewSymbolsFailed(result *Result, symbols []kicad.ImportSymbol) {
	for i := len(result.Symbols) - len(symbols); i < len(result.Symbols); i++ {
		if i >= 0 && result.Symbols[i].Status != "exists" {
			result.Symbols[i].Status = "failed"
		}
	}
}

// warnIfKeptDifferent flags a file that was left in place although the incoming one differs.
// Without it a symbol can end up silently bound to someone else's footprint of the same name
// (names that differ only by case collide on macOS and Windows).
func warnIfKeptDifferent(result *Result, status, path string, incoming []byte, label string) {
	if status != "exists" {
		return
	}
	current, err := os.ReadFile(path)
	if err != nil || bytes.Equal(current, incoming) {
		return
	}
	warning := label + " 已存在且內容不同，未覆寫；元件庫裡仍是原本那一份（要取代請開啟覆寫）"
	appendWarning(result, warning, warning)
}

func writeFile(path string, data []byte, overwrite bool) (string, error) {
	_, err := os.Stat(path)
	if err == nil && !overwrite {
		return "exists", nil
	}
	status := "added"
	if err == nil {
		status = "updated"
	} else if !os.IsNotExist(err) {
		return "failed", err
	}
	if err := atomicWrite(path, data); err != nil {
		return "failed", err
	}
	return status, nil
}

func appendResultError(result *Result, err error) {
	if err == nil {
		return
	}
	if result.Err == nil {
		result.Err = err
		return
	}
	result.Err = errors.Join(result.Err, err)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partfetch-import-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func collect(path string, caps limits) (bundle, error) {
	info, err := os.Stat(path)
	if err != nil {
		return bundle{}, err
	}
	if info.IsDir() {
		return collectDirectory(path, caps)
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		return collectZip(path, caps)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		return bundle{}, err
	}
	if int64(len(file)) > caps.file {
		return bundle{}, fmt.Errorf("單一檔案超過大小上限")
	}
	ext := strings.ToLower(filepath.Ext(path))
	oldFormat := !ignoredPath(filepath.Base(path)) && ext == ".mod"
	var files, legacy []sourceFile
	if collectible(ext) && !ignoredPath(filepath.Base(path)) {
		files = append(files, sourceFile{path: path, name: filepath.Base(path), origName: filepath.Base(path), ext: ext, data: file})
	}
	if legacyExt(ext) && !ignoredPath(filepath.Base(path)) {
		legacy = append(legacy, sourceFile{path: path, name: filepath.Base(path), origName: filepath.Base(path), ext: ext, data: file})
	}
	collected, err := collectFiles(files, 1, int64(len(file)), caps, oldFormat)
	collected.legacy = legacy
	return collected, err
}

func collectDirectory(root string, caps limits) (bundle, error) {
	collector := directoryCollector{root: root, caps: caps, files: make([]sourceFile, 0)}
	err := filepath.WalkDir(root, collector.visit)
	if err != nil {
		return bundle{}, err
	}
	return collectFiles(collector.files, collector.items, collector.total, caps, collector.oldFormat)
}

type directoryCollector struct {
	root      string
	caps      limits
	files     []sourceFile
	items     int
	total     int64
	oldFormat bool
}

func (c *directoryCollector) visit(path string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if path == c.root {
		return nil
	}
	c.items++
	if c.items > c.caps.items {
		return fmt.Errorf("匯入項目超過數量上限")
	}
	rel, err := filepath.Rel(c.root, path)
	if err != nil {
		return err
	}
	if ignoredPath(filepath.ToSlash(rel)) || entry.IsDir() {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	c.total += info.Size()
	if c.total > c.caps.bundle {
		return fmt.Errorf("整包未壓縮大小超過上限")
	}
	if entry.Type()&os.ModeSymlink != 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(entry.Name()))
	if ext == ".mod" {
		c.oldFormat = true
	}
	if !collectible(ext) && !legacyExt(ext) {
		return nil
	}
	return c.readFile(path, rel, ext)
}

func (c *directoryCollector) readFile(path, rel, ext string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if int64(len(data)) > c.caps.file {
		return fmt.Errorf("單一檔案超過大小上限")
	}
	c.files = append(c.files, sourceFile{path: filepath.ToSlash(rel), name: filepath.Base(rel), origName: filepath.Base(rel), ext: ext, data: data})
	return nil
}

func collectZip(path string, caps limits) (bundle, error) {
	reader, err := zip.OpenReader(path)
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return bundle{}, err
	}
	if reader == nil {
		return bundle{}, err
	}
	defer reader.Close()
	files := make([]sourceFile, 0)
	items, total, oldFormat := 0, int64(0), false
	for _, entry := range reader.File {
		items++
		if items > caps.items {
			return bundle{}, fmt.Errorf("匯入項目超過數量上限")
		}
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		isDir := entry.FileInfo().IsDir()
		if ignoredPath(name) {
			continue
		}
		data, err := readZipEntry(entry, caps.file, caps.bundle-total)
		if err != nil {
			return bundle{}, err
		}
		total += int64(len(data))
		if total > caps.bundle {
			return bundle{}, fmt.Errorf("整包未壓縮大小超過上限")
		}
		if isDir {
			continue
		}
		base := filepath.Base(name)
		ext := strings.ToLower(filepath.Ext(base))
		if ext == ".mod" {
			oldFormat = true
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if collectible(ext) || legacyExt(ext) {
			files = append(files, sourceFile{path: name, name: base, origName: base, ext: ext, data: data})
		}
	}
	return collectFiles(files, items, total, caps, oldFormat)
}

func readZipEntry(entry *zip.File, maxFile, remaining int64) ([]byte, error) {
	raw, err := entry.OpenRaw()
	if err != nil {
		return nil, err
	}
	var reader io.Reader = raw
	var decompressor io.ReadCloser
	if entry.Method == zip.Deflate {
		decompressor = flate.NewReader(raw)
		defer decompressor.Close()
		reader = decompressor
	} else if entry.Method != zip.Store {
		return nil, fmt.Errorf("不支援 zip 壓縮方式 %d", entry.Method)
	}
	limit := maxFile
	if remaining < limit {
		limit = remaining
	}
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxFile {
		return nil, fmt.Errorf("單一檔案超過大小上限")
	}
	if int64(len(data)) > remaining {
		return nil, fmt.Errorf("整包未壓縮大小超過上限")
	}
	if crc32.ChecksumIEEE(data) != entry.CRC32 {
		return nil, zip.ErrChecksum
	}
	return data, nil
}

func collectFiles(files []sourceFile, items int, total int64, caps limits, oldFormat bool) (bundle, error) {
	if items > caps.items {
		return bundle{}, fmt.Errorf("匯入項目超過數量上限")
	}
	if total > caps.bundle {
		return bundle{}, fmt.Errorf("整包未壓縮大小超過上限")
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].path < files[j].path })
	legacy := make([]sourceFile, 0)
	compatible := make([]sourceFile, 0, len(files))
	for _, file := range files {
		if legacyExt(file.ext) {
			legacy = append(legacy, file)
		} else {
			compatible = append(compatible, file)
		}
	}
	return bundle{files: compatible, legacy: legacy, oldFormat: oldFormat}, nil
}

func ignoredPath(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "__MACOSX" || (part != ".." && strings.HasPrefix(part, ".")) {
			return true
		}
	}
	return false
}

func collectible(ext string) bool {
	switch ext {
	case ".kicad_sym", ".kicad_mod", ".step", ".stp", ".wrl":
		return true
	}
	return false
}

func legacyExt(ext string) bool { return ext == ".lib" || ext == ".dcm" || ext == ".mod" }
