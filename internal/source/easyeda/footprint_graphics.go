package easyeda

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

func solidRegion(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	g := model.FootprintGraphic{Kind: "polyline"}
	if len(f) < 5 {
		return g, false, fmt.Errorf("malformed SOLIDREGION")
	}
	if f[4] == "cutout" {
		return g, false, nil
	}
	layer, ok := layers[f[1]]
	if f[1] == "99" && f[4] != "npth" {
		layer, ok = "F.CrtYd", true
	}
	if f[1] == "100" && f[4] != "npth" {
		return g, false, nil
	}
	if f[4] == "npth" {
		layer, ok = "Edge.Cuts", true
	}
	if !ok {
		return g, false, nil
	}
	points, e := ParseSVGPath(f[3])
	if e != nil {
		return g, false, e
	}
	return model.FootprintGraphic{Kind: "polyline", Layer: layer, Points: translatePath(points, ox, oy, false), Fill: f[4] == "solid"}, true, nil
}

func footprintTrack(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	g := model.FootprintGraphic{Kind: "track"}
	if len(f) < 5 {
		return g, false, fmt.Errorf("malformed TRACK")
	}
	layer, ok := layers[f[2]]
	if !ok {
		return g, false, nil
	}
	coords := strings.Fields(f[4])
	for i := 0; i+1 < len(coords); i += 2 {
		x, e := strconv.ParseFloat(coords[i], 64)
		if e != nil {
			return g, false, e
		}
		y, e := strconv.ParseFloat(coords[i+1], 64)
		if e != nil {
			return g, false, e
		}
		g.Points = append(g.Points, transform(x, y, ox, oy, false))
	}
	g.Layer = layer
	width, e := numberAt(f, 1)
	if e != nil {
		return g, false, e
	}
	g.Width = width * unitMM
	return g, true, nil
}

func footprintCircle(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	if len(f) < 7 {
		return model.FootprintGraphic{}, false, fmt.Errorf("malformed CIRCLE")
	}
	layer, ok := layers[f[5]]
	if !ok {
		return model.FootprintGraphic{}, false, nil
	}
	v, e := numbersAt(f, 1, 2, 3, 4)
	if e != nil {
		return model.FootprintGraphic{}, false, e
	}
	return model.FootprintGraphic{Kind: "circle", Layer: layer, Center: transform(v[0], v[1], ox, oy, false), Radius: v[2] * unitMM, Width: v[3] * unitMM}, true, nil
}

func footprintRect(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	if len(f) < 8 {
		return model.FootprintGraphic{}, false, fmt.Errorf("malformed RECT")
	}
	layer, ok := layers[f[5]]
	if !ok {
		return model.FootprintGraphic{}, false, nil
	}
	v, e := numbersAt(f, 1, 2, 3, 4, 7)
	if e != nil {
		return model.FootprintGraphic{}, false, e
	}
	x, y, w, h := v[0], v[1], v[2], v[3]
	points := []model.Point{{X: x, Y: y}, {X: x + w, Y: y}, {X: x + w, Y: y + h}, {X: x, Y: y + h}}
	return model.FootprintGraphic{Kind: "rectangle", Layer: layer, Width: v[4] * unitMM, Points: transformPoints(points, ox, oy, false)}, true, nil
}

func footprintArc(f []string, ox, oy float64) (model.FootprintGraphic, bool, error) {
	if len(f) < 5 {
		return model.FootprintGraphic{}, false, fmt.Errorf("malformed ARC")
	}
	layer, ok := layers[f[2]]
	if !ok {
		return model.FootprintGraphic{}, false, nil
	}
	points, e := ParseSVGPath(f[4])
	if e != nil {
		return model.FootprintGraphic{}, false, e
	}
	width, e := numberAt(f, 1)
	if e != nil {
		return model.FootprintGraphic{}, false, e
	}
	return model.FootprintGraphic{Kind: "arc", Layer: layer, Width: width * unitMM, Points: translatePath(points, ox, oy, false)}, true, nil
}
