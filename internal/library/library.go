// Package library manages the on-disk KiCad library layout.
package library

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/partid"
)

type Options struct {
	Out, Name, ModelBase string
	Overwrite            bool
	SaveOBJ              bool
}
type Result struct {
	Symbol, Footprint, Model             string
	SymbolPath, FootprintPath, ModelPath string
	Warnings                             []string
}

type Entry struct {
	Name         string
	Footprint    string
	LCSC         string
	PartID       string
	HasFootprint bool
	HasModel     bool
}

type RemoveResult struct {
	Name      string
	Symbol    string
	Footprint string
	Model     string
	Error     string
	Cause     error `json:"-"`
}

var lcscNumber = regexp.MustCompile(`(?i)^C\d+$`)

func List(opts Options) ([]Entry, error) {
	var err error
	opts, err = defaults(opts)
	if err != nil {
		return nil, err
	}
	lib := kicad.Sanitize(opts.Name)
	data, err := os.ReadFile(filepath.Join(opts.Out, lib+".kicad_sym"))
	if os.IsNotExist(err) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	symbols, err := kicad.SymbolLibraryInfo(data)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(symbols))
	for _, symbol := range symbols {
		footprint := symbol.Footprint
		if index := strings.LastIndex(footprint, ":"); index >= 0 {
			footprint = footprint[index+1:]
		}
		entry := Entry{Name: symbol.Name, Footprint: footprint, LCSC: symbol.LCSC, PartID: libraryPartID(symbol)}
		if footprint != "" {
			footprintPath := filepath.Join(opts.Out, lib+".pretty", footprint+".kicad_mod")
			if info, statErr := os.Stat(footprintPath); statErr == nil && !info.IsDir() {
				entry.HasFootprint = true
				if footprintData, readErr := os.ReadFile(footprintPath); readErr == nil {
					for _, modelPath := range kicad.FootprintModelPaths(footprintData) {
						base := filepath.Base(strings.ReplaceAll(modelPath, "\\", "/"))
						if base == "" || base == "." || base == ".." {
							continue
						}
						if _, statErr := os.Stat(filepath.Join(opts.Out, lib+".3dshapes", base)); statErr == nil {
							entry.HasModel = true
							break
						}
					}
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func Remove(opts Options, names []string) ([]RemoveResult, error) {
	results := make([]RemoveResult, len(names))
	for i, name := range names {
		results[i] = RemoveResult{Name: name, Symbol: "missing", Footprint: "none", Model: "none"}
	}
	var err error
	opts, err = defaults(opts)
	if err != nil {
		return nil, err
	}
	lib := kicad.Sanitize(opts.Name)
	symbolPath := filepath.Join(opts.Out, lib+".kicad_sym")
	original, err := os.ReadFile(symbolPath)
	if os.IsNotExist(err) {
		return results, nil
	}
	if err != nil {
		return nil, err
	}
	symbols, err := kicad.SymbolLibraryInfo(original)
	if err != nil {
		return nil, err
	}
	type selectedSymbol struct {
		footprint string
	}
	byName := make(map[string]kicad.LibrarySymbol, len(symbols))
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}
	resolved := make([]string, len(names))
	selected := make(map[string]selectedSymbol)
	for i, name := range names {
		symbol, ok := byName[name]
		if !ok && lcscNumber.MatchString(name) {
			for _, candidate := range symbols {
				if strings.EqualFold(candidate.LCSC, name) {
					symbol, ok = candidate, true
					break
				}
			}
		}
		if !ok {
			source, value, parseErr := partid.Parse(name)
			if parseErr == nil && source == "easyeda" {
				for _, candidate := range symbols {
					if strings.EqualFold(candidate.EasyEDA, value) {
						symbol, ok = candidate, true
						break
					}
				}
			}
		}
		if !ok {
			continue
		}
		resolved[i] = symbol.Name
		if _, exists := selected[symbol.Name]; exists {
			continue
		}
		footprint := strings.TrimSpace(symbol.Footprint)
		if index := strings.LastIndex(footprint, ":"); index >= 0 {
			if footprint[:index] != lib {
				footprint = "!external!" + footprint
			} else {
				footprint = footprint[index+1:]
			}
		}
		selected[symbol.Name] = selectedSymbol{footprint: footprint}
	}
	selectedNames := make([]string, 0, len(selected))
	for _, symbol := range symbols {
		if _, ok := selected[symbol.Name]; ok {
			selectedNames = append(selectedNames, symbol.Name)
		}
	}
	if len(selectedNames) == 0 {
		return results, nil
	}
	backupPath := symbolPath + ".bak"
	if err = os.WriteFile(backupPath, original, 0644); err != nil {
		return nil, err
	}
	updated, removed, err := kicad.RemoveSymbols(original, selectedNames)
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(symbolPath, updated); err != nil {
		return nil, err
	}
	removedSet := make(map[string]bool, len(removed))
	for _, name := range removed {
		removedSet[name] = true
	}
	retainedFootprints := make(map[string]bool)
	for _, symbol := range symbols {
		if removedSet[symbol.Name] {
			continue
		}
		footprint := strings.TrimSpace(symbol.Footprint)
		if index := strings.LastIndex(footprint, ":"); index >= 0 {
			if footprint[:index] != lib {
				continue
			}
			footprint = footprint[index+1:]
		}
		if footprint != "" {
			retainedFootprints[footprint] = true
		}
	}
	footprintStatus := make(map[string]string)
	footprintStatusByName := make(map[string]string)
	footprintError := make(map[string]string)
	footprintCause := make(map[string]error)
	footprintCauseByName := make(map[string]error)
	modelPathsByFootprint := make(map[string][]string)
	processedFootprints := make(map[string]bool)
	for _, name := range selectedNames {
		item := selected[name]
		footprint := item.footprint
		if processedFootprints[footprint] {
			footprintStatusByName[name] = footprintStatus[footprint]
			footprintError[name] = footprintError[footprint]
			footprintCauseByName[name] = footprintCause[footprint]
			continue
		}
		processedFootprints[footprint] = true
		switch {
		case footprint == "":
			footprintStatus[footprint] = "none"
		case strings.HasPrefix(footprint, "!external!"):
			footprintStatus[footprint] = "kept"
		case retainedFootprints[footprint]:
			footprintStatus[footprint] = "kept"
		default:
			path := filepath.Join(opts.Out, lib+".pretty", footprint+".kicad_mod")
			data, readErr := os.ReadFile(path)
			// No early exit here: the per-symbol status below must be recorded for every outcome.
			if os.IsNotExist(readErr) {
				footprintStatus[footprint] = "missing"
			} else if readErr != nil {
				footprintStatus[footprint], footprintError[footprint], footprintCause[footprint] = "failed", readErr.Error(), readErr
			} else {
				modelPathsByFootprint[footprint] = kicad.FootprintModelPaths(data)
				if removeErr := os.Remove(path); removeErr != nil {
					footprintStatus[footprint], footprintError[footprint], footprintCause[footprint] = "failed", removeErr.Error(), removeErr
				} else {
					footprintStatus[footprint] = "removed"
				}
			}
		}
		footprintStatusByName[name] = footprintStatus[footprint]
		footprintError[name] = footprintError[footprint]
		footprintCauseByName[name] = footprintCause[footprint]
	}
	remainingModelNames := make(map[string]bool)
	prettyDir := filepath.Join(opts.Out, lib+".pretty")
	files, readDirErr := os.ReadDir(prettyDir)
	if readDirErr == nil {
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".kicad_mod") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(prettyDir, file.Name()))
			if readErr != nil {
				continue
			}
			for _, path := range kicad.FootprintModelPaths(data) {
				if base := safeModelBase(path); base != "" {
					remainingModelNames[base] = true
				}
			}
		}
	}
	modelStatus := make(map[string]string)
	modelError := make(map[string]string)
	modelCause := make(map[string]error)
	for _, name := range selectedNames {
		fpStatus := footprintStatusByName[name]
		if fpStatus != "removed" {
			if fpStatus == "kept" {
				modelStatus[name] = "kept"
			} else {
				modelStatus[name] = "none"
			}
			continue
		}
		footprint := selected[name].footprint
		paths := modelPathsByFootprint[footprint]
		if len(paths) == 0 {
			modelStatus[name] = "none"
			continue
		}
		status := "removed"
		var failure string
		var failureCause error
		for _, modelPath := range paths {
			base := safeModelBase(modelPath)
			if base == "" {
				continue
			}
			if remainingModelNames[base] {
				status = "kept"
				continue
			}
			if previous, done := modelStatus["model:"+base]; done {
				if previous == "failed" {
					status = "failed"
				}
				continue
			}
			modelFile := filepath.Join(opts.Out, lib+".3dshapes", base)
			removeErr := os.Remove(modelFile)
			if os.IsNotExist(removeErr) {
				status = "missing"
				modelStatus["model:"+base] = "missing"
				continue
			}
			if removeErr != nil {
				status = "failed"
				failure = removeErr.Error()
				failureCause = removeErr
				modelStatus["model:"+base] = "failed"
				continue
			}
			if objErr := os.Remove(filepath.Join(opts.Out, lib+".3dshapes", strings.TrimSuffix(base, filepath.Ext(base))+".obj")); objErr != nil && !os.IsNotExist(objErr) {
				status = "failed"
				failure = objErr.Error()
				failureCause = objErr
				modelStatus["model:"+base] = "failed"
				continue
			}
			modelStatus["model:"+base] = "removed"
		}
		modelStatus[name] = status
		if failure != "" {
			modelError[name] = failure
			modelCause[name] = failureCause
		}
	}
	for i, name := range names {
		symbolName := resolved[i]
		if symbolName == "" {
			continue
		}
		results[i] = RemoveResult{Name: name, Symbol: "removed", Footprint: footprintStatusByName[symbolName], Model: modelStatus[symbolName], Error: footprintError[symbolName], Cause: footprintCauseByName[symbolName]}
		if modelError[symbolName] != "" {
			results[i].Error = modelError[symbolName]
			results[i].Cause = modelCause[symbolName]
		}
	}
	return results, nil
}

func libraryPartID(symbol kicad.LibrarySymbol) string {
	if symbol.LCSC != "" {
		return symbol.LCSC
	}
	if symbol.EasyEDA != "" {
		return "easyeda:" + strings.ToLower(symbol.EasyEDA)
	}
	return ""
}

func safeModelBase(path string) string {
	base := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
	if base == "" || base == "." || base == ".." {
		return ""
	}
	return base
}

func Write(opts Options, c *model.Component, models map[string][]byte) (Result, error) {
	var err error
	opts, err = defaults(opts)
	if err != nil {
		return Result{}, err
	}
	lib := kicad.Sanitize(opts.Name)
	symName := kicad.Sanitize(c.Value)
	if symName == "" {
		symName = kicad.Sanitize(c.Title)
	}
	fpName := kicad.Sanitize(c.Footprint.Name)
	if err = os.MkdirAll(opts.Out, 0755); err != nil {
		return Result{}, err
	}
	symPath := filepath.Join(opts.Out, lib+".kicad_sym")
	fpPath := filepath.Join(opts.Out, lib+".pretty", fpName+".kicad_mod")
	result := Result{SymbolPath: symPath, FootprintPath: fpPath, Symbol: "added", Footprint: "added", Model: "skipped", Warnings: append([]string(nil), c.Warnings...)}
	var failures []error
	result.Symbol, err = writeSymbolArtifact(symPath, symName, c, lib, fpName, opts.Overwrite)
	if err != nil {
		failures = append(failures, fmt.Errorf("%w", err))
	}
	var modelUsable bool
	result.Model, result.ModelPath, modelUsable, err = writeModelArtifact(opts, lib, c, models)
	if err != nil {
		failures = append(failures, fmt.Errorf("%w", err))
	}
	if result.Model == "failed" && !hasModelWarning(result.Warnings) {
		result.Warnings = append(result.Warnings, "3D model download failed")
	}
	copyComponent := *c
	copyFootprint := c.Footprint
	if !modelUsable {
		copyFootprint.Models = nil
	}
	copyComponent.Footprint = copyFootprint
	result.Footprint, err = writeFootprintArtifact(fpPath, &copyComponent, lib, opts)
	if err != nil {
		failures = append(failures, fmt.Errorf("write footprint failed: %w", err))
	}
	if len(failures) > 0 {
		return result, joinedError{errors: failures}
	}
	return result, nil
}

func hasModelWarning(warnings []string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "3D model download failed") {
			return true
		}
	}
	return false
}

func defaults(opts Options) (Options, error) {
	if opts.Name == "" {
		opts.Name = "partfetch"
	}
	if opts.Out == "" {
		opts.Out = "."
	}
	if opts.ModelBase == "" {
		path, e := filepath.Abs(opts.Out)
		if e != nil {
			return opts, e
		}
		opts.ModelBase = path
	}
	return opts, nil
}

func writeSymbolArtifact(path, name string, c *model.Component, lib, footprint string, overwrite bool) (string, error) {
	exists, e := symbolExists(path, name)
	if e != nil {
		return "failed", e
	}
	if exists && !overwrite {
		return "exists", nil
	}
	status := "added"
	if exists {
		status = "updated"
	}
	if e = writeSymbols(path, name, kicad.Symbol(c, lib, footprint)); e != nil {
		return "failed", e
	}
	return status, nil
}

func writeModelArtifact(opts Options, lib string, c *model.Component, models map[string][]byte) (string, string, bool, error) {
	if len(c.Footprint.Models) == 0 {
		return "skipped", "", true, nil
	}
	ref := c.Footprint.Models[0]
	path := filepath.Join(opts.Out, lib+".3dshapes", kicad.Sanitize(ref.Title)+".step")
	data, ok := models[ref.UUID+".step"]
	if !ok {
		data, ok = models[ref.UUID]
	}
	status := "failed"
	usable := false
	if ok {
		var err error
		status, err = writeBlob(path, data, opts.Overwrite)
		if err != nil {
			return "failed", path, false, fmt.Errorf("write model failed: %w", err)
		}
		usable = true
	} else if _, e := os.Stat(path); e == nil {
		status, usable = "exists", true
	}
	if opts.SaveOBJ {
		if err := writeOBJArtifact(opts, lib, ref, models); err != nil {
			return "failed", path, usable, err
		}
	}
	return status, path, usable, nil
}

func writeOBJArtifact(opts Options, lib string, ref model.Model3DRef, models map[string][]byte) error {
	data, ok := models[ref.UUID+".obj"]
	if !ok {
		return nil
	}
	path := filepath.Join(opts.Out, lib+".3dshapes", kicad.Sanitize(ref.Title)+".obj")
	if _, err := writeBlob(path, data, opts.Overwrite); err != nil {
		return fmt.Errorf("write OBJ model failed: %w", err)
	}
	return nil
}

func writeFootprintArtifact(path string, c *model.Component, lib string, opts Options) (string, error) {
	if _, e := os.Stat(path); e == nil && !opts.Overwrite {
		return "exists", nil
	}
	return writeBlob(path, kicad.Footprint(c, lib, opts.ModelBase), opts.Overwrite)
}

func symbolExists(path, name string) (bool, error) {
	data, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	spans, e := symbols(data)
	if e != nil {
		return false, e
	}
	for _, s := range spans {
		if s == name {
			return true, nil
		}
	}
	return false, nil
}

func writeSymbols(path, name string, symbol []byte) error {
	old, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		merged, e := kicad.NewSymbolLibrary(map[string][]byte{name: symbol})
		if e != nil {
			return e
		}
		return atomicWrite(path, merged)
	}
	if e != nil {
		return e
	}
	if e = os.WriteFile(path+".bak", old, 0644); e != nil {
		return e
	}
	merged, e := kicad.MergeSymbols(old, map[string][]byte{name: symbol})
	if e != nil {
		return e
	}
	return atomicWrite(path, merged)
}

func symbols(data []byte) ([]string, error) { return kicad.SymbolNames(data) }

func writeBlob(path string, data []byte, overwrite bool) (string, error) {
	_, statErr := os.Stat(path)
	if statErr == nil && !overwrite {
		return "exists", nil
	}
	if e := atomicWrite(path, data); e != nil {
		return "failed", e
	}
	if statErr == nil {
		return "updated", nil
	}
	return "added", nil
}

type joinedError struct{ errors []error }

func (e joinedError) Error() string {
	parts := make([]string, len(e.errors))
	for i, err := range e.errors {
		parts[i] = err.Error()
	}
	return strings.Join(parts, "; ")
}

func (e joinedError) Unwrap() []error {
	return e.errors
}
func atomicWrite(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	tmp, e := os.CreateTemp(filepath.Dir(path), ".partfetch-*")
	if e != nil {
		return e
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, e = tmp.Write(data); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Sync(); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	return os.Rename(name, path)
}
