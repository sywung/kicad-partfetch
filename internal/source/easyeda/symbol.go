package easyeda

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

func parseSymbolGraphic(s string, ox, oy float64) (model.Graphic, bool, error) {
	f := strings.Split(s, "~")
	if len(f) == 0 {
		return model.Graphic{}, false, nil
	}
	switch f[0] {
	case "R":
		return symbolRectangle(f, ox, oy)
	case "C":
		return symbolCircle(f, ox, oy)
	case "E":
		return symbolEllipse(f, ox, oy)
	case "PL", "PG":
		return symbolPolygon(f, ox, oy)
	case "A", "PT":
		return symbolPath(f, ox, oy)
	case "T":
		return symbolText(f, ox, oy)
	default:
		return model.Graphic{}, false, nil
	}
}

func symbolRectangle(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 9 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol rectangle")
	}
	v, err := numbersAt(f, 1, 2, 5, 6, 8)
	if err != nil {
		return model.Graphic{}, false, err
	}
	x, y, w, h := v[0], v[1], v[2], v[3]
	g := model.Graphic{Kind: "rectangle", Fill: "background", Width: v[4] * unitMM}
	g.Points = transformPoints([]model.Point{{X: x, Y: y}, {X: x + w, Y: y}, {X: x + w, Y: y + h}, {X: x, Y: y + h}}, ox, oy, true)
	return g, true, nil
}

func symbolCircle(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 6 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol circle")
	}
	v, err := numbersAt(f, 1, 2, 3, 5)
	if err != nil {
		return model.Graphic{}, false, err
	}
	r := v[2] * unitMM
	return model.Graphic{Kind: "circle", Center: transform(v[0], v[1], ox, oy, true), RadiusX: r, RadiusY: r, Width: v[3] * unitMM}, true, nil
}

func symbolEllipse(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 7 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol ellipse")
	}
	v, err := numbersAt(f, 1, 2, 3, 4, 6)
	if err != nil {
		return model.Graphic{}, false, err
	}
	cx, cy, rx, ry := v[0], v[1], v[2], v[3]
	g := model.Graphic{Center: transform(cx, cy, ox, oy, true), RadiusX: rx * unitMM, RadiusY: ry * unitMM, Width: v[4] * unitMM}
	if rx == ry {
		g.Kind = "circle"
		return g, true, nil
	}
	g.Kind = "polyline"
	for i := 0; i < 32; i++ {
		a := float64(i) * 2 * math.Pi / 32
		g.Points = append(g.Points, transform(cx+rx*math.Cos(a), cy+ry*math.Sin(a), ox, oy, true))
	}
	return g, true, nil
}

func symbolPolygon(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 2 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol polygon")
	}
	coords := strings.Fields(f[1])
	g := model.Graphic{Kind: "polyline"}
	for i := 0; i+1 < len(coords); i += 2 {
		x, e := strconv.ParseFloat(coords[i], 64)
		if e != nil {
			return g, false, e
		}
		y, e := strconv.ParseFloat(coords[i+1], 64)
		if e != nil {
			return g, false, e
		}
		g.Points = append(g.Points, transform(x, y, ox, oy, true))
	}
	width, err := shapeNumber(f, 3)
	if err != nil {
		return g, false, err
	}
	g.Width = width * unitMM
	if f[0] == "PG" {
		g.Fill = "outline"
		closePoints(&g.Points)
	}
	return g, true, nil
}

func symbolPath(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 2 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol path")
	}
	points, e := ParseSVGPath(f[1])
	if e != nil {
		return model.Graphic{}, false, fmt.Errorf("%s shape path: %w", f[0], e)
	}
	width, err := shapeNumber(f, 3)
	if err != nil {
		return model.Graphic{}, false, err
	}
	g := model.Graphic{Kind: "polyline", Points: translatePath(points, ox, oy, true), Width: width * unitMM}
	if f[0] == "A" && len(points) >= 3 {
		g.Kind = "arc"
	}
	return g, true, nil
}

func symbolText(f []string, ox, oy float64) (model.Graphic, bool, error) {
	if len(f) < 14 {
		return model.Graphic{}, false, fmt.Errorf("malformed symbol text")
	}
	if f[13] != "1" {
		return model.Graphic{}, true, nil
	}
	v, err := numbersAt(f, 2, 3)
	if err != nil {
		return model.Graphic{}, false, err
	}
	return model.Graphic{Kind: "text", Center: transform(v[0], v[1], ox, oy, true), Text: f[12], Width: 1.27}, true, nil
}

func shapeNumber(fields []string, index int) (float64, error) { return numberAt(fields, index) }

func closePoints(points *[]model.Point) {
	if len(*points) > 0 && (*points)[0] != (*points)[len(*points)-1] {
		*points = append(*points, (*points)[0])
	}
}
func transform(x, y, ox, oy float64, flipY bool) model.Point {
	yy := (y - oy) * unitMM
	if flipY {
		yy = -yy
	}
	return model.Point{X: (x - ox) * unitMM, Y: yy}
}
func transformPoints(in []model.Point, ox, oy float64, flip bool) []model.Point {
	out := make([]model.Point, len(in))
	for i, p := range in {
		out[i] = transform(p.X, p.Y, ox, oy, flip)
	}
	return out
}
func translatePath(in []model.Point, ox, oy float64, flip bool) []model.Point {
	out := make([]model.Point, len(in))
	for i, p := range in {
		out[i] = transform(p.X/unitMM, p.Y/unitMM, ox, oy, flip)
	}
	return out
}
