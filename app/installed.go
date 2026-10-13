package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/kicad"
	"github.com/sywung/kicad-partfetch/internal/kicadcli"
	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/partid"
	gh "github.com/sywung/kicad-partfetch/internal/source/github"
)

// PreviewInstalled renders a symbol that is already in the library, and its footprint,
// straight from the library files. It is the only way to look at imported parts, which
// have no online source to preview from.
func (a *App) PreviewInstalled(name string) (Preview, error) {
	s, err := a.GetSettings()
	if err != nil {
		return Preview{}, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return Preview{}, err
	}
	entry, err := installedEntry(opts, name)
	if err != nil {
		return Preview{}, err
	}
	cli, err := a.kicadCLI.Find(a.configuredKiCadCLI())
	if errors.Is(err, kicadcli.ErrNotFound) {
		return Preview{}, errors.New("找不到 kicad-cli，無法顯示元件庫內的圖形")
	}
	if err != nil {
		return Preview{}, errors.New("無法執行 kicad-cli")
	}
	dir, err := os.MkdirTemp("", "partfetch-installed-*")
	if err != nil {
		return Preview{}, errors.New("無法建立暫存目錄")
	}
	defer os.RemoveAll(dir)
	result := Preview{PartID: entry.Name, Name: entry.Name, Package: entry.Footprint, Source: "library", External: true, HasModel: entry.HasModel, SymbolSVGs: []string{}, FootprintSVGs: []string{}, Files: []string{}, Warnings: []string{}}
	lib := kicad.Sanitize(opts.Name)
	if result.SymbolSVGs, err = a.installedSymbolSVGs(cli, dir, filepath.Join(opts.Out, lib+".kicad_sym"), entry.Name); err != nil {
		return Preview{}, err
	}
	if entry.HasFootprint {
		source := filepath.Join(opts.Out, lib+".pretty", entry.Footprint+".kicad_mod")
		if result.FootprintSVGs, err = a.installedFootprintSVGs(cli, dir, source); err != nil {
			return Preview{}, err
		}
	} else if entry.Footprint != "" {
		result.Warnings = append(result.Warnings, "元件庫裡找不到封裝檔 "+entry.Footprint)
	}
	return result, nil
}

func installedEntry(opts library.Options, name string) (library.Entry, error) {
	entries, err := library.List(opts)
	if err != nil {
		return library.Entry{}, errors.New("無法讀取元件庫")
	}
	for _, entry := range entries {
		if entry.Name == name {
			return entry, nil
		}
	}
	return library.Entry{}, errors.New("元件庫裡找不到這個符號")
}

// installedSymbolSVGs exports one symbol by writing it, preceded by any parents it extends,
// to a single-purpose library file. Exporting the whole library would cost time per symbol.
func (a *App) installedSymbolSVGs(cli, dir, libraryPath, name string) ([]string, error) {
	data, err := os.ReadFile(libraryPath)
	if err != nil {
		return nil, errors.New("無法讀取元件庫")
	}
	symbols, err := kicad.ImportSymbols(data)
	if err != nil {
		return nil, errors.New("符號庫檔案格式錯誤")
	}
	chain := symbolChain(symbols, name)
	if len(chain) == 0 {
		return nil, errors.New("元件庫裡找不到這個符號")
	}
	version := kicad.LibraryVersion(data)
	if version == 0 {
		version = 20231120
	}
	content := []byte(fmt.Sprintf("(kicad_symbol_lib (version %d) (generator \"kicad-partfetch\") (generator_version \"0.1\")\n", version))
	for _, symbol := range chain {
		content = append(append(content, symbol.Data...), '\n')
	}
	content = append(content, ")\n"...)
	file := filepath.Join(dir, "symbol.kicad_sym")
	if err = os.WriteFile(file, content, 0600); err != nil {
		return nil, errors.New("無法建立暫存檔")
	}
	exported, err := a.kicadCLI.SymbolSVGs(context.Background(), cli, file)
	if err != nil {
		return nil, errors.New("kicad-cli 無法繪製這個符號")
	}
	return svgStrings(exported), nil
}

// symbolChain returns the named symbol's ancestors, outermost first, then the symbol itself.
func symbolChain(symbols []kicad.ImportSymbol, name string) []kicad.ImportSymbol {
	byName := make(map[string]kicad.ImportSymbol, len(symbols))
	for _, symbol := range symbols {
		byName[symbol.Name] = symbol
	}
	var chain []kicad.ImportSymbol
	seen := map[string]bool{}
	for current := name; current != "" && !seen[current]; {
		symbol, ok := byName[current]
		if !ok {
			break
		}
		seen[current] = true
		chain = append([]kicad.ImportSymbol{symbol}, chain...)
		current = symbol.Extends
	}
	if len(chain) == 0 || chain[len(chain)-1].Name != name {
		return nil
	}
	return chain
}

func (a *App) installedFootprintSVGs(cli, dir, source string) ([]string, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, errors.New("無法讀取封裝檔")
	}
	pretty := filepath.Join(dir, "footprint.pretty")
	if err = os.MkdirAll(pretty, 0700); err != nil {
		return nil, errors.New("無法建立暫存目錄")
	}
	if err = os.WriteFile(filepath.Join(pretty, filepath.Base(source)), data, 0600); err != nil {
		return nil, errors.New("無法建立暫存檔")
	}
	exported, err := a.kicadCLI.FootprintSVGs(context.Background(), cli, pretty)
	if err != nil {
		return nil, errors.New("kicad-cli 無法繪製這個封裝")
	}
	return svgStrings(exported), nil
}

func svgStrings(exported []kicadcli.NamedSVG) []string {
	values := make([]string, 0, len(exported))
	for _, svg := range exported {
		values = append(values, string(svg.SVG))
	}
	return values
}

// InstalledModel carries a base64 binary glTF of a 3D model, in metres and Y-up.
// File and Note describe what is shown when it is not simply "the installed footprint's model".
type InstalledModel struct{ GLB, File, Note string }

type boardGLBExporter interface {
	BoardGLB(context.Context, string, []byte) ([]byte, error)
}

const boardLayers = `(layers (0 "F.Cu" signal) (31 "B.Cu" signal) (36 "B.SilkS" user) (37 "F.SilkS" user) (38 "B.Mask" user) (39 "F.Mask" user) (44 "Edge.Cuts" user) (46 "B.CrtYd" user) (47 "F.CrtYd" user) (48 "B.Fab" user) (49 "F.Fab" user))`

// ModelInstalled lets KiCad place and tessellate the installed footprint's model (STEP or VRML),
// which the in-app viewer cannot read on its own.
func (a *App) ModelInstalled(name string) (InstalledModel, error) {
	s, err := a.GetSettings()
	if err != nil {
		return InstalledModel{}, err
	}
	opts, err := libraryOptions(s)
	if err != nil {
		return InstalledModel{}, err
	}
	entry, err := installedEntry(opts, name)
	if err != nil {
		return InstalledModel{}, err
	}
	if !entry.HasFootprint {
		return InstalledModel{}, errors.New("元件庫裡找不到這個零件的封裝檔")
	}
	board, err := installedBoard(opts, entry.Footprint)
	if err != nil {
		return InstalledModel{}, err
	}
	glb, err := a.exportGLB(board)
	if err != nil {
		return InstalledModel{}, err
	}
	if glb == "" {
		return InstalledModel{}, errors.New("這個封裝沒有可顯示的 3D 模型（封裝未指定模型，或模型檔不在元件庫裡）")
	}
	return InstalledModel{GLB: glb}, nil
}

// exportGLB returns the base64 glTF for a board, or "" when KiCad produced no geometry.
func (a *App) exportGLB(board []byte) (string, error) {
	exporter, ok := a.kicadCLI.(boardGLBExporter)
	if !ok {
		return "", errors.New("目前的 kicad-cli 介面不支援 3D 匯出")
	}
	cli, err := a.kicadCLI.Find(a.configuredKiCadCLI())
	if errors.Is(err, kicadcli.ErrNotFound) {
		return "", errors.New("找不到 kicad-cli，無法顯示 3D 模型")
	}
	if err != nil {
		return "", errors.New("無法執行 kicad-cli")
	}
	glb, err := exporter.BoardGLB(context.Background(), cli, board)
	if err != nil {
		return "", errors.New("kicad-cli 無法轉換這個 3D 模型")
	}
	if !glbHasMeshes(glb) {
		return "", nil
	}
	return base64.StdEncoding.EncodeToString(glb), nil
}

func wrapBoard(footprint []byte) []byte {
	board := "(kicad_pcb (version 20240108) (generator \"kicad-partfetch\") (generator_version \"8.0\") (general (thickness 1.6)) (paper \"A4\") " + boardLayers + "\n"
	return append(append([]byte(board), footprint...), "\n)\n"...)
}

// ModelExternal previews a GitHub repository's first 3D file before anything is added.
// If a footprint in the repository references the file, KiCad places it as that footprint says;
// otherwise the bare model is shown through a stand-in footprint.
func (a *App) ModelExternal(partID string) (InstalledModel, error) {
	sourceName, fullName, err := partid.Parse(partID)
	if err != nil || sourceName != "github" {
		return InstalledModel{}, errors.New("只有 GitHub 來源支援這個 3D 預覽")
	}
	contents, err := a.github.Contents(context.Background(), fullName)
	if err != nil {
		return InstalledModel{}, err
	}
	if len(contents.Models) == 0 {
		return InstalledModel{}, errors.New("這個 repo 沒有 3D 模型檔")
	}
	dir, err := os.MkdirTemp("", "partfetch-github-model-*")
	if err != nil {
		return InstalledModel{}, errors.New("無法建立暫存目錄")
	}
	defer os.RemoveAll(dir)
	if err = a.github.Download(context.Background(), contents, dir); err != nil {
		return InstalledModel{}, err
	}
	modelPath := contents.Models[0].Path
	local := filepath.ToSlash(filepath.Join(dir, filepath.FromSlash(modelPath)))
	board, owner := externalModelBoard(dir, contents.Footprints, modelPath, local)
	glb, err := a.exportGLB(board)
	if err != nil {
		return InstalledModel{}, err
	}
	if glb == "" {
		return InstalledModel{}, errors.New("KiCad 無法從這個 3D 檔產生可顯示的模型")
	}
	note := "這個模型沒有掛在任何封裝上，只顯示模型本體；加入後需在 KiCad 的封裝屬性手動指定"
	if owner != "" {
		note = "依封裝 " + owner + " 的設定擺放"
	}
	if len(contents.Models) > 1 {
		note += fmt.Sprintf("（共 %d 個 3D 檔，顯示第一個）", len(contents.Models))
	}
	return InstalledModel{GLB: glb, File: modelPath, Note: note}, nil
}

// externalModelBoard returns a board for the model and the name of the footprint that owns it, if any.
func externalModelBoard(dir string, footprints []gh.File, modelPath, local string) ([]byte, string) {
	want := strings.ToLower(pathBase(modelPath))
	for _, file := range footprints {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(file.Path)))
		if err != nil {
			continue
		}
		for _, path := range kicad.FootprintModelPaths(data) {
			if strings.ToLower(pathBase(path)) != want {
				continue
			}
			if rewritten, _, rewriteErr := kicad.RewriteFootprintModels(data, map[string]string{path: local}); rewriteErr == nil {
				return wrapBoard(rewritten), strings.TrimSuffix(pathBase(file.Path), filepath.Ext(file.Path))
			}
		}
	}
	standIn := "(footprint \"model\" (layer \"F.Cu\") (model " + kicad.Quote(local) + " (offset (xyz 0 0 0)) (scale (xyz 1 1 1)) (rotate (xyz 0 0 0))))"
	return wrapBoard([]byte(standIn)), ""
}

func pathBase(path string) string {
	return filepath.Base(strings.ReplaceAll(path, "\\", "/"))
}

// installedBoard wraps the footprint in a minimal board. Model paths are pointed at the
// library's own 3dshapes directory so that ${KIPRJMOD}-style prefixes still resolve.
func installedBoard(opts library.Options, footprint string) ([]byte, error) {
	lib := kicad.Sanitize(opts.Name)
	data, err := os.ReadFile(filepath.Join(opts.Out, lib+".pretty", footprint+".kicad_mod"))
	if err != nil {
		return nil, errors.New("無法讀取封裝檔")
	}
	shapes := filepath.Join(opts.Out, lib+".3dshapes")
	resolved := map[string]string{}
	for _, path := range kicad.FootprintModelPaths(data) {
		base := filepath.Base(strings.ReplaceAll(path, "\\", "/"))
		if base == "" || base == "." || base == ".." {
			continue
		}
		candidate := filepath.Join(shapes, base)
		if _, statErr := os.Stat(candidate); statErr == nil {
			resolved[path] = filepath.ToSlash(candidate)
		}
	}
	if data, _, err = kicad.RewriteFootprintModels(data, resolved); err != nil {
		return nil, errors.New("封裝檔格式錯誤")
	}
	return wrapBoard(data), nil
}

// glbHasMeshes reads the JSON chunk of a binary glTF. KiCad writes a valid but empty file
// when a footprint has no usable model.
func glbHasMeshes(glb []byte) bool {
	if len(glb) < 20 || string(glb[:4]) != "glTF" {
		return false
	}
	length := int(binary.LittleEndian.Uint32(glb[12:16]))
	if length <= 0 || 20+length > len(glb) {
		return false
	}
	var document struct {
		Meshes []json.RawMessage `json:"meshes"`
	}
	if json.Unmarshal(glb[20:20+length], &document) != nil {
		return false
	}
	return len(document.Meshes) > 0
}
