package easyeda

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

var layers = map[string]string{"1": "F.Cu", "2": "B.Cu", "3": "F.SilkS", "4": "B.SilkS", "5": "F.Paste", "6": "B.Paste", "7": "F.Mask", "8": "B.Mask", "10": "Edge.Cuts", "12": "Cmts.User", "13": "F.Fab", "14": "B.Fab", "15": "Dwgs.User", "101": "F.Fab"}

func parseFootprint(pkg map[string]any) (model.Footprint, []string, error) {
	data, ok := pkg["dataStr"].(map[string]any)
	if !ok {
		return model.Footprint{}, nil, fmt.Errorf("package dataStr is missing")
	}
	head := obj(data["head"])
	if len(head) == 0 {
		return model.Footprint{}, nil, fmt.Errorf("package dataStr.head is missing")
	}
	ox, e := field(head, "x")
	if e != nil {
		return model.Footprint{}, nil, e
	}
	oy, e := field(head, "y")
	if e != nil {
		return model.Footprint{}, nil, e
	}
	f := model.Footprint{Name: str(pkg["title"])}
	warnings, err := parseFootprintShapes(list(data["shape"]), ox, oy, &f)
	if err != nil {
		return f, warnings, err
	}
	if f.Name == "" {
		f.Name = str(obj(head["c_para"])["package"])
	}
	includePadBounds(&f)
	if f.HasCourtyard {
		expandCourtyard(&f, .25)
	}
	return f, warnings, nil
}

func parseFootprintShapes(shapes []any, ox, oy float64, f *model.Footprint) ([]string, error) {
	counts := map[string]int{}
	for _, raw := range shapes {
		s := str(raw)
		kind := shapeKind(s)
		fields := strings.Split(s, "~")
		consumed, err := parseFootprintRecord(kind, fields, ox, oy, f)
		if err != nil {
			return nil, err
		}
		if consumed {
			continue
		}
		if intentionallySkippedGraphic(kind, fields) {
			continue
		}
		g, include, e := parseFootprintGraphic(fields, ox, oy)
		if e != nil {
			return nil, e
		}
		if include {
			if kind == "SOLIDREGION" && fields[1] == "99" && fields[4] == "solid" {
				includeCourtyard(f, g)
			} else {
				f.Graphics = append(f.Graphics, g)
				includeGraphicBounds(f, g)
			}
		} else if !knownNonGraphic(kind) && !intentionallySkippedGraphic(kind, fields) {
			counts[footprintWarningKind(kind, fields)]++
		}
	}
	return warningList("unknown footprint", counts), nil
}

func intentionallySkippedGraphic(kind string, fields []string) bool {
	if kind == "SOLIDREGION" {
		if len(fields) > 4 && fields[4] == "cutout" {
			return true
		}
		if len(fields) > 4 && fields[1] == "99" && fields[4] == "solid" {
			return false
		}
	}
	index := footprintLayerIndex(kind)
	return index >= 0 && index < len(fields) && (fields[index] == "99" || fields[index] == "100")
}

func footprintLayerIndex(kind string) int {
	switch kind {
	case "SOLIDREGION":
		return 1
	case "TRACK", "ARC":
		return 2
	case "CIRCLE", "RECT":
		return 5
	default:
		return -1
	}
}

func footprintWarningKind(kind string, fields []string) string {
	index := -1
	switch kind {
	case "SOLIDREGION":
		index = 1
	case "TRACK", "ARC":
		index = 2
	case "CIRCLE", "RECT":
		index = 5
	}
	if index < 0 {
		return "shape " + kind
	}
	if index >= len(fields) {
		return "shape " + kind
	}
	layer := fields[index]
	if layer == "99" || layer == "100" {
		return "shape " + kind
	}
	if _, ok := layers[layer]; !ok {
		return "layer " + layer
	}
	return "shape " + kind
}

func parseFootprintRecord(kind string, fields []string, ox, oy float64, f *model.Footprint) (bool, error) {
	switch kind {
	case "PAD":
		pad, err := parsePad(fields, ox, oy)
		if err == nil {
			f.Pads = append(f.Pads, pad)
		}
		return true, err
	case "HOLE":
		hole, err := parseHole(fields, ox, oy)
		if err == nil {
			f.NPTHoles = append(f.NPTHoles, hole)
		}
		return true, err
	case "VIA":
		pad, err := parseVia(fields, ox, oy)
		if err == nil {
			f.Pads = append(f.Pads, pad)
		}
		return true, err
	case "SVGNODE":
		ref, err := parseModel(fields, ox, oy)
		if err == nil && ref.UUID != "" {
			f.Models = append(f.Models, ref)
		}
		return true, err
	case "TEXT":
		return true, nil
	default:
		return false, nil
	}
}

func parsePad(f []string, ox, oy float64) (model.Pad, error) {
	d, err := readPadData(f)
	if err != nil {
		return model.Pad{}, err
	}
	typ, err := padType(d.layer, d.hole, f[15])
	if err != nil {
		return model.Pad{}, err
	}
	pad := model.Pad{Number: f[8], Shape: padShape(f[1], d.width, d.height), Type: typ, X: (d.cx - ox) * unitMM, Y: (d.cy - oy) * unitMM, Width: d.width * unitMM, Height: d.height * unitMM, Drill: 2 * d.hole * unitMM, Slot: d.slot * unitMM, Rotation: d.angle, Layer: d.layer}
	pad.Points, err = parsePadPoints(f[10], d.cx, d.cy)
	return pad, err
}

type padData struct {
	cx, cy, width, height, hole, angle, slot float64
	layer                                    int
}

func readPadData(f []string) (padData, error) {
	if len(f) < 16 {
		return padData{}, fmt.Errorf("malformed PAD record")
	}
	values := make([]float64, 7)
	indices := []int{2, 3, 4, 5, 9, 11, 13}
	for i, index := range indices {
		if strings.TrimSpace(f[index]) == "" {
			continue
		}
		n, e := strconv.ParseFloat(strings.TrimSpace(f[index]), 64)
		if e != nil {
			return padData{}, e
		}
		values[i] = n
	}
	layer, e := strconv.Atoi(f[6])
	if e != nil {
		return padData{}, e
	}
	return padData{cx: values[0], cy: values[1], width: values[2], height: values[3], hole: values[4], angle: values[5], slot: values[6], layer: layer}, nil
}

func padType(layer int, hole float64, plating string) (string, error) {
	if layer == 11 && hole > 0 {
		if strings.EqualFold(plating, "N") {
			return "np_thru_hole", nil
		}
		return "thru_hole", nil
	}
	if layer == 1 || layer == 2 {
		return "smd", nil
	}
	return "", fmt.Errorf("unsupported pad layer %d", layer)
}

func padShape(shape string, width, height float64) string {
	shape = strings.ToLower(shape)
	if shape == "ellipse" {
		if math.Abs(width-height) < 1e-9 {
			return "circle"
		}
		return "oval"
	}
	if shape == "polygon" {
		return "custom"
	}
	return shape
}

func parsePadPoints(value string, cx, cy float64) ([]model.Point, error) {
	coords := strings.Fields(strings.ReplaceAll(value, ",", " "))
	points := make([]model.Point, 0, len(coords)/2)
	for i := 0; i+1 < len(coords); i += 2 {
		x, e := strconv.ParseFloat(coords[i], 64)
		if e != nil {
			return nil, e
		}
		y, e := strconv.ParseFloat(coords[i+1], 64)
		if e != nil {
			return nil, e
		}
		points = append(points, model.Point{X: (x - cx) * unitMM, Y: (y - cy) * unitMM})
	}
	return points, nil
}

func parseHole(f []string, ox, oy float64) (model.Hole, error) {
	if len(f) < 4 {
		return model.Hole{}, fmt.Errorf("malformed HOLE")
	}
	x, e := strconv.ParseFloat(f[1], 64)
	if e != nil {
		return model.Hole{}, e
	}
	y, e := strconv.ParseFloat(f[2], 64)
	if e != nil {
		return model.Hole{}, e
	}
	r, e := strconv.ParseFloat(f[3], 64)
	return model.Hole{X: (x - ox) * unitMM, Y: (y - oy) * unitMM, Diameter: 2 * r * unitMM}, e
}

func parseVia(f []string, ox, oy float64) (model.Pad, error) {
	if len(f) < 6 {
		return model.Pad{}, fmt.Errorf("malformed VIA")
	}
	x, e := strconv.ParseFloat(f[1], 64)
	if e != nil {
		return model.Pad{}, e
	}
	y, e := strconv.ParseFloat(f[2], 64)
	if e != nil {
		return model.Pad{}, e
	}
	diameter, e := strconv.ParseFloat(f[3], 64)
	if e != nil {
		return model.Pad{}, e
	}
	hole, e := strconv.ParseFloat(f[5], 64)
	if e != nil {
		return model.Pad{}, e
	}
	size := diameter * unitMM
	return model.Pad{Shape: "circle", Type: "thru_hole", X: (x - ox) * unitMM, Y: (y - oy) * unitMM, Width: size, Height: size, Drill: 2 * hole * unitMM, Layer: 11}, nil
}

func parseModel(f []string, ox, oy float64) (model.Model3DRef, error) {
	if len(f) < 2 {
		return model.Model3DRef{}, nil
	}
	var root map[string]any
	if e := json.Unmarshal([]byte(f[1]), &root); e != nil {
		return model.Model3DRef{}, e
	}
	a := obj(root["attrs"])
	if str(a["c_etype"]) != "outline3D" {
		return model.Model3DRef{}, nil
	}
	origin := strings.Split(str(a["c_origin"]), ",")
	rot := strings.Split(str(a["c_rotation"]), ",")
	position, e := numbersAt(origin, 0, 1)
	if e != nil {
		return model.Model3DRef{}, e
	}
	rotation, e := numbersAt(rot, 0, 1, 2)
	if e != nil {
		return model.Model3DRef{}, e
	}
	x, y, z := position[0], position[1], 0.0
	if n, e := num(a["z"]); e != nil {
		return model.Model3DRef{}, e
	} else {
		z = n
	}
	r := [3]float64{rotation[0], rotation[1], rotation[2]}
	originMM := model.Point{X: (x - ox) * unitMM, Y: (y - oy) * unitMM}
	center, e := modelOutlineCenter(root, ox, oy, originMM)
	if e != nil {
		return model.Model3DRef{}, e
	}
	return model.Model3DRef{UUID: str(a["uuid"]), Title: str(a["title"]), Origin: originMM, OutlineCenter: center, Z: z, Rotation: r}, nil
}

func modelOutlineCenter(root map[string]any, ox, oy float64, fallback model.Point) (model.Point, error) {
	minX, minY, maxX, maxY, found := 0.0, 0.0, 0.0, 0.0, false
	for _, child := range list(root["childNodes"]) {
		node := obj(child)
		if str(node["nodeName"]) != "polyline" {
			continue
		}
		coords := strings.Fields(strings.ReplaceAll(str(obj(node["attrs"])["points"]), ",", " "))
		if len(coords)%2 != 0 {
			return model.Point{}, fmt.Errorf("malformed 3D outline points")
		}
		for i := 0; i < len(coords); i += 2 {
			x, e := strconv.ParseFloat(coords[i], 64)
			if e != nil {
				return model.Point{}, e
			}
			y, e := strconv.ParseFloat(coords[i+1], 64)
			if e != nil {
				return model.Point{}, e
			}
			if !found {
				minX, maxX, minY, maxY, found = x, x, y, y, true
				continue
			}
			minX, minY = math.Min(minX, x), math.Min(minY, y)
			maxX, maxY = math.Max(maxX, x), math.Max(maxY, y)
		}
	}
	if !found {
		return fallback, nil
	}
	return model.Point{X: ((minX+maxX)/2 - ox) * unitMM, Y: ((minY+maxY)/2 - oy) * unitMM}, nil
}

func parseFootprintGraphic(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	if len(f) == 0 {
		return model.FootprintGraphic{}, false, nil
	}
	switch f[0] {
	case "SOLIDREGION":
		return solidRegion(f, ox, oy)
	case "TRACK":
		return footprintTrack(f, ox, oy)
	case "CIRCLE":
		return footprintCircle(f, ox, oy)
	case "RECT":
		return footprintRect(f, ox, oy)
	case "ARC":
		return footprintArc(f, ox, oy)
	default:
		return model.FootprintGraphic{Kind: f[0]}, false, nil
	}
}

func knownNonGraphic(k string) bool {
	switch k {
	case "PAD", "HOLE", "SVGNODE", "TEXT", "VIA":
		return true
	}
	return false
}
func includeCourtyard(f *model.Footprint, g model.FootprintGraphic) {
	for _, p := range g.Points {
		addCourtyardBounds(f, p.X, p.Y)
		addOverallBounds(f, p.X, p.Y)
	}
}
func includePadBounds(f *model.Footprint) {
	for _, p := range f.Pads {
		a := p.Rotation * math.Pi / 180
		for _, q := range []model.Point{{X: -p.Width / 2, Y: -p.Height / 2}, {X: p.Width / 2, Y: -p.Height / 2}, {X: p.Width / 2, Y: p.Height / 2}, {X: -p.Width / 2, Y: p.Height / 2}} {
			x := p.X + q.X*math.Cos(a) - q.Y*math.Sin(a)
			y := p.Y + q.X*math.Sin(a) + q.Y*math.Cos(a)
			addCourtyardBounds(f, x, y)
			addOverallBounds(f, x, y)
		}
	}
	for _, h := range f.NPTHoles {
		r := h.Diameter / 2
		addCourtyardBounds(f, h.X-r, h.Y-r)
		addCourtyardBounds(f, h.X+r, h.Y+r)
		addOverallBounds(f, h.X-r, h.Y-r)
		addOverallBounds(f, h.X+r, h.Y+r)
	}
}

func includeGraphicBounds(f *model.Footprint, g model.FootprintGraphic) {
	for _, p := range g.Points {
		addOverallBounds(f, p.X, p.Y)
	}
	if g.Kind == "circle" {
		addOverallBounds(f, g.Center.X-g.Radius, g.Center.Y-g.Radius)
		addOverallBounds(f, g.Center.X+g.Radius, g.Center.Y+g.Radius)
	}
}

func addCourtyardBounds(f *model.Footprint, x, y float64) {
	if !f.HasCourtyard {
		f.Courtyard = [4]float64{x, y, x, y}
		f.HasCourtyard = true
		return
	}
	if x < f.Courtyard[0] {
		f.Courtyard[0] = x
	}
	if y < f.Courtyard[1] {
		f.Courtyard[1] = y
	}
	if x > f.Courtyard[2] {
		f.Courtyard[2] = x
	}
	if y > f.Courtyard[3] {
		f.Courtyard[3] = y
	}
}

func addOverallBounds(f *model.Footprint, x, y float64) {
	if !f.HasBounds {
		f.Bounds = [4]float64{x, y, x, y}
		f.HasBounds = true
		return
	}
	if x < f.Bounds[0] {
		f.Bounds[0] = x
	}
	if y < f.Bounds[1] {
		f.Bounds[1] = y
	}
	if x > f.Bounds[2] {
		f.Bounds[2] = x
	}
	if y > f.Bounds[3] {
		f.Bounds[3] = y
	}
}
func expandCourtyard(f *model.Footprint, d float64) {
	for i, v := range f.Courtyard {
		if i < 2 {
			f.Courtyard[i] = math.Round((v-d)*100) / 100
		} else {
			f.Courtyard[i] = math.Round((v+d)*100) / 100
		}
	}
}
