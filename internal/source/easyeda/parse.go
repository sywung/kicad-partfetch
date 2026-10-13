package easyeda

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

const unitMM = 0.254

var referenceSuffix = regexp.MustCompile(`[?0-9]`)

func Parse(data []byte) (*model.Component, error) {
	root, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadData, err)
	}
	if root["success"] != true {
		return nil, ErrNotFound
	}
	r := obj(root["result"])
	if len(r) == 0 {
		return nil, ErrNotFound
	}
	component, err := parseComponent(r)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: %w", ErrBadData, err)
	}
	return component, err
}

func parseComponent(r map[string]any) (*model.Component, error) {
	main, ok := r["dataStr"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("component dataStr is missing")
	}
	head := obj(main["head"])
	if len(head) == 0 {
		return nil, fmt.Errorf("component dataStr.head is missing")
	}
	component := &model.Component{ID: componentID(r, head), Title: str(r["title"])}
	parseProperties(component, obj(head["c_para"]))
	component.Datasheet = str(obj(r["lcsc"])["url"])
	if err := parseComponentUnits(r, main, component); err != nil {
		return nil, err
	}
	foot, warns, err := parseFootprint(obj(r["packageDetail"]))
	if err != nil {
		return nil, err
	}
	component.Footprint = foot
	component.Warnings = append(component.Warnings, warns...)
	return component, nil
}

func componentID(r, head map[string]any) string {
	id := str(obj(r["lcsc"])["number"])
	if id != "" {
		return id
	}
	id = str(obj(r["szlcsc"])["number"])
	if id != "" {
		return id
	}
	return str(obj(head["c_para"])["Supplier Part"])
}

func parseProperties(component *model.Component, para map[string]any) {
	component.Reference = referenceSuffix.ReplaceAllString(str(para["pre"]), "")
	if component.Reference == "" {
		component.Reference = "U"
	}
	component.Value = str(para["name"])
	if component.Value == "" {
		component.Value = component.Title
	}
	component.Manufacturer = str(para["Manufacturer"])
	component.MPN = str(para["Manufacturer Part"])
	component.JLCPCBClass = str(para["JLCPCB Part Class"])
}

func parseComponentUnits(r, main map[string]any, component *model.Component) error {
	subparts := list(r["subparts"])
	if r["subparts"] != nil && subparts == nil {
		return fmt.Errorf("component subparts is not an array")
	}
	if len(subparts) > 0 {
		for _, raw := range subparts {
			unit, warns, err := parseUnit(obj(raw))
			if err != nil {
				return err
			}
			component.Units = append(component.Units, unit)
			component.Warnings = append(component.Warnings, warns...)
		}
	} else {
		unit, warns, err := parseUnit(map[string]any{"dataStr": main})
		if err != nil {
			return err
		}
		component.Units = append(component.Units, unit)
		component.Warnings = append(component.Warnings, warns...)
	}
	return nil
}

func parseUnit(part map[string]any) (model.SymbolUnit, []string, error) {
	data, ok := part["dataStr"].(map[string]any)
	if !ok {
		return model.SymbolUnit{}, nil, fmt.Errorf("unit dataStr is missing")
	}
	head := obj(data["head"])
	if len(head) == 0 {
		return model.SymbolUnit{}, nil, fmt.Errorf("unit dataStr.head is missing")
	}
	ox, err := field(head, "x")
	if err != nil {
		return model.SymbolUnit{}, nil, err
	}
	oy, err := field(head, "y")
	if err != nil {
		return model.SymbolUnit{}, nil, err
	}
	var unit model.SymbolUnit
	counts := map[string]int{}
	for _, raw := range list(data["shape"]) {
		s := str(raw)
		if strings.HasPrefix(s, "P~") {
			pin, e := parsePin(s, ox, oy)
			if e != nil {
				return unit, nil, e
			}
			unit.Pins = append(unit.Pins, pin)
			continue
		}
		g, ok, e := parseSymbolGraphic(s, ox, oy)
		if e != nil {
			return unit, nil, e
		}
		if ok {
			unit.Graphics = append(unit.Graphics, g)
		} else {
			counts[shapeKind(s)]++
		}
	}
	return unit, warningList("unknown symbol shape", counts), nil
}

func warningList(category string, counts map[string]int) []string {
	kinds := make([]string, 0, len(counts))
	for kind := range counts {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	warnings := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		warnings = append(warnings, fmt.Sprintf("%s %q (%d)", category, kind, counts[kind]))
	}
	return warnings
}

func parsePin(s string, ox, oy float64) (model.Pin, error) {
	parts := strings.Split(s, "^^")
	head := strings.Split(parts[0], "~")
	if len(head) < 6 {
		return model.Pin{}, fmt.Errorf("malformed pin: %s", s)
	}
	x, y, err := pinPosition(head)
	if err != nil {
		return model.Pin{}, err
	}
	name, number, nameVisible, numberVisible := pinLabels(parts, head[3])
	angle, length, shape, err := pinGeometry(parts)
	if err != nil {
		return model.Pin{}, err
	}
	return model.Pin{Number: number, Name: name, NameVisible: nameVisible, NumberVisible: numberVisible, Electrical: electricalType(head[2]), Shape: shape, X: (x - ox) * unitMM, Y: -(y - oy) * unitMM, Angle: angle, Length: length}, nil
}

func pinPosition(head []string) (float64, float64, error) {
	x, e := num(head[4])
	if e != nil {
		return 0, 0, e
	}
	y, e := num(head[5])
	return x, y, e
}
func pinLabels(parts []string, fallback string) (string, string, bool, bool) {
	name, nameVisible := pinText(parts, 3)
	number, numberVisible := pinText(parts, 4)
	if number == "" {
		number = fallback
	}
	return name, number, nameVisible, numberVisible
}
func pinGeometry(parts []string) (float64, float64, string, error) {
	path := ""
	if len(parts) > 2 {
		path = parts[2]
	}
	axis, delta, e := pinDirection(path)
	if e != nil {
		return 0, 0, "", e
	}
	angle := 0.0
	if axis == 'h' && delta < 0 {
		angle = 180
	}
	if axis == 'v' {
		angle = 90
		if delta > 0 {
			angle = 270
		}
	}
	dot := len(parts) > 5 && strings.HasPrefix(parts[5], "1~")
	clock := len(parts) > 6 && strings.HasPrefix(parts[6], "1~")
	shape := "line"
	if dot && clock {
		shape = "inverted_clock"
	} else if dot {
		shape = "inverted"
	} else if clock {
		shape = "clock"
	}
	return angle, abs(delta) * unitMM, shape, nil
}

func pinText(parts []string, index int) (string, bool) {
	if index >= len(parts) {
		return "", false
	}
	fields := strings.Split(parts[index], "~")
	if len(fields) < 5 {
		return "", false
	}
	return fields[4], fields[0] == "1"
}
func electricalType(code string) string {
	switch code {
	case "1":
		return "input"
	case "2":
		return "output"
	case "3":
		return "bidirectional"
	case "4":
		return "power_in"
	default:
		return "unspecified"
	}
}
func pinDirection(path string) (byte, float64, error) {
	path = strings.SplitN(path, "~", 2)[0]
	fields := pathToken.FindAllString(path, -1)
	if len(fields) < 4 {
		return 0, 0, fmt.Errorf("invalid pin path %q", path)
	}
	for i := 0; i < len(fields); i++ {
		if fields[i] == "h" || fields[i] == "v" {
			if i+1 >= len(fields) {
				return 0, 0, fmt.Errorf("missing pin length in %q", path)
			}
			n, e := num(fields[i+1])
			return fields[i][0], n, e
		}
	}
	return 0, 0, fmt.Errorf("missing pin axis in %q", path)
}
func abs(n float64) float64 {
	if n < 0 {
		return -n
	}
	return n
}
func shapeKind(s string) string {
	if i := strings.IndexByte(s, '~'); i >= 0 {
		return s[:i]
	}
	return s
}
