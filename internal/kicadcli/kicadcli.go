// Package kicadcli wraps KiCad CLI preview exports.
package kicadcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

var ErrNotFound = errors.New("kicad-cli not found")

type NamedSVG struct {
	Name string
	SVG  []byte
}

type Runner func(context.Context, string, ...string) error

type Client interface {
	Find(string) (string, error)
	SymbolSVGs(context.Context, string, string) ([]NamedSVG, error)
	FootprintSVGs(context.Context, string, string) ([]NamedSVG, error)
}

type SystemClient struct{ Runner Runner }

func (c SystemClient) Find(configured string) (string, error) { return Find(configured) }
func (c SystemClient) SymbolSVGs(ctx context.Context, executable, file string) ([]NamedSVG, error) {
	return SymbolSVGsWithRunner(ctx, executable, file, c.Runner)
}
func (c SystemClient) FootprintSVGs(ctx context.Context, executable, dir string) ([]NamedSVG, error) {
	return FootprintSVGsWithRunner(ctx, executable, dir, c.Runner)
}

type FindOptions struct {
	Stat     func(string) error
	LookPath func(string) (string, error)
	Glob     func(string) ([]string, error)
	GOOS     string
}

func Find(configured string) (string, error) { return FindWith(configured, FindOptions{}) }

func FindWith(configured string, options FindOptions) (string, error) {
	if options.Stat == nil {
		options.Stat = func(path string) error {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return errors.New("not a file")
			}
			return nil
		}
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Glob == nil {
		options.Glob = filepath.Glob
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if configured != "" && options.Stat(configured) == nil {
		return configured, nil
	}
	if options.GOOS == "darwin" {
		path := "/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli"
		if options.Stat(path) == nil {
			return path, nil
		}
	}
	if path, err := options.LookPath("kicad-cli"); err == nil {
		return path, nil
	}
	if options.GOOS == "windows" {
		return findWindows(options)
	}
	return "", ErrNotFound
}

func findWindows(options FindOptions) (string, error) {
	paths, err := options.Glob(`C:\Program Files\KiCad\*\bin\kicad-cli.exe`)
	if err != nil {
		return "", ErrNotFound
	}
	sort.Slice(paths, func(i, j int) bool { return versionLess(paths[i], paths[j]) })
	for i := len(paths) - 1; i >= 0; i-- {
		if options.Stat(paths[i]) == nil {
			return paths[i], nil
		}
	}
	return "", ErrNotFound
}

func versionLess(left, right string) bool {
	leftParts, rightParts := versionParts(windowsVersion(left)), versionParts(windowsVersion(right))
	for i := 0; i < len(leftParts) && i < len(rightParts); i++ {
		if leftParts[i] != rightParts[i] {
			return leftParts[i] < rightParts[i]
		}
	}
	return len(leftParts) < len(rightParts)
}

func versionParts(value string) []int {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r < '0' || r > '9' })
	versions := make([]int, 0, len(parts))
	for _, part := range parts {
		number, _ := strconv.Atoi(part)
		versions = append(versions, number)
	}
	return versions
}

func windowsVersion(path string) string {
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	for i, part := range parts {
		if strings.EqualFold(part, "KiCad") && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return path
}

func SymbolSVGs(ctx context.Context, cli, symFile string) ([]NamedSVG, error) {
	return SymbolSVGsWithRunner(ctx, cli, symFile, commandRunner)
}

func FootprintSVGs(ctx context.Context, cli, prettyDir string) ([]NamedSVG, error) {
	return FootprintSVGsWithRunner(ctx, cli, prettyDir, commandRunner)
}

func UpgradeSymbolLibrary(ctx context.Context, cli string, libData, dcmData []byte) ([]byte, error) {
	return UpgradeSymbolLibraryWithRunner(ctx, cli, libData, dcmData, commandRunner)
}

func UpgradeSymbolLibraryWithRunner(ctx context.Context, cli string, libData, dcmData []byte, runner Runner) ([]byte, error) {
	if cli == "" {
		return nil, ErrNotFound
	}
	if runner == nil {
		runner = commandRunner
	}
	dir, err := os.MkdirTemp("", "partfetch-kicadcli-upgrade-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	libPath := filepath.Join(dir, "in.lib")
	if err = os.WriteFile(libPath, libData, 0600); err != nil {
		return nil, err
	}
	if dcmData != nil {
		if err = os.WriteFile(filepath.Join(dir, "in.dcm"), dcmData, 0600); err != nil {
			return nil, err
		}
	}
	output := filepath.Join(dir, "out.kicad_sym")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = runner(ctx, cli, "sym", "upgrade", "-o", output, libPath); err != nil {
		return nil, fmt.Errorf("kicad-cli symbol upgrade failed: %w", err)
	}
	return os.ReadFile(output)
}

// builtinTheme pins kicad-cli to KiCad's stock colours. Without it the export
// follows whatever theme the user picked in their own KiCad editors, and the
// GUI could no longer map those colours onto its own palette.
const builtinTheme = "_builtin_default"

// footprintLayers limits the footprint export to the layers a preview needs.
// Without it kicad-cli plots every layer, so a through-hole pad is painted once
// per inner copper layer and ends up in the colour of an arbitrary inner layer.
// F.Cu is plotted after B.Cu, which leaves through-hole pads in the front colour.
const footprintLayers = "B.Cu,F.Cu,B.SilkS,F.SilkS,B.Fab,F.Fab,B.CrtYd,F.CrtYd,Edge.Cuts,Dwgs.User,Cmts.User"

func SymbolSVGsWithRunner(ctx context.Context, cli, symFile string, runner Runner) ([]NamedSVG, error) {
	return export(ctx, cli, []string{"sym", "export", "svg"}, nil, symFile, runner)
}

func FootprintSVGsWithRunner(ctx context.Context, cli, prettyDir string, runner Runner) ([]NamedSVG, error) {
	return export(ctx, cli, []string{"fp", "export", "svg"}, []string{"--layers", footprintLayers}, prettyDir, runner)
}

func export(ctx context.Context, cli string, args, options []string, input string, runner Runner) ([]NamedSVG, error) {
	if cli == "" {
		return []NamedSVG{}, ErrNotFound
	}
	if runner == nil {
		runner = commandRunner
	}
	dir, err := os.MkdirTemp("", "partfetch-kicadcli-*")
	if err != nil {
		return []NamedSVG{}, err
	}
	defer os.RemoveAll(dir)
	args = append(args, "-o", dir)
	args = append(args, options...)
	args = append(args, "--theme", builtinTheme, input)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = runner(ctx, cli, args...); err != nil {
		return []NamedSVG{}, fmt.Errorf("kicad-cli export failed: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []NamedSVG{}, err
	}
	svgs := make([]NamedSVG, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".svg") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			return []NamedSVG{}, readErr
		}
		svgs = append(svgs, NamedSVG{Name: entry.Name(), SVG: body})
	}
	sort.Slice(svgs, func(i, j int) bool { return svgs[i].Name < svgs[j].Name })
	return svgs, nil
}

var commandRunner Runner = func(ctx context.Context, executable string, args ...string) error {
	return exec.CommandContext(ctx, executable, args...).Run()
}

// BoardGLB exports a board file, with the 3D models its footprints reference, as binary glTF.
// KiCad does the STEP/VRML tessellation and applies each model's offset and rotation, so the
// result is placed exactly as KiCad's own 3D viewer shows it.
func (c SystemClient) BoardGLB(ctx context.Context, executable string, board []byte) ([]byte, error) {
	return BoardGLBWithRunner(ctx, executable, board, c.Runner)
}

func BoardGLBWithRunner(ctx context.Context, cli string, board []byte, runner Runner) ([]byte, error) {
	if cli == "" {
		return nil, ErrNotFound
	}
	if runner == nil {
		runner = commandRunner
	}
	dir, err := os.MkdirTemp("", "partfetch-kicadcli-glb-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "in.kicad_pcb")
	if err = os.WriteFile(input, board, 0600); err != nil {
		return nil, err
	}
	output := filepath.Join(dir, "out.glb")
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err = runner(ctx, cli, "pcb", "export", "glb", "--subst-models", "--no-board-body", "--force", "-o", output, input); err != nil {
		return nil, fmt.Errorf("kicad-cli glb export failed: %w", err)
	}
	return os.ReadFile(output)
}
