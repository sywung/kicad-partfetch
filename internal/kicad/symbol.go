package kicad

import (
	"fmt"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

func Symbol(component *model.Component, library, footprint string) []byte {
	name := Sanitize(component.Value)
	if name == "" {
		name = Sanitize(component.Title)
	}
	var b strings.Builder
	b.WriteString("(symbol " + Quote(name) + "\n")
	writePinVisibility(&b, component)
	writeSymbolProperties(&b, component, library, footprint)
	writeSymbolUnits(&b, component, name)
	b.WriteString(")")
	return []byte(b.String())
}

func writePinVisibility(b *strings.Builder, component *model.Component) {
	allNames, allNumbers := true, true
	for _, u := range component.Units {
		for _, p := range u.Pins {
			allNames = allNames && !p.NameVisible
			allNumbers = allNumbers && !p.NumberVisible
		}
	}
	if allNames {
		b.WriteString("  (pin_names (offset 1.016) hide)\n")
	}
	if allNumbers {
		b.WriteString("  (pin_numbers hide)\n")
	}
}

func writeSymbolProperties(b *strings.Builder, component *model.Component, library, footprint string) {
	_, minY, _, maxY := symbolBounds(component)
	refY := maxY + 1.27
	valueY := minY - 1.27
	property(b, "Reference", component.Reference, 0, refY, 0, false)
	property(b, "Value", component.Value, 0, valueY, 0, false)
	property(b, "Footprint", library+":"+footprint, 0, valueY-1.27, 0, true)
	property(b, "Datasheet", component.Datasheet, 0, valueY-2.54, 0, true)
	lcscID, easyedaID := component.ID, ""
	if strings.HasPrefix(component.ID, "easyeda:") {
		lcscID = ""
		easyedaID = strings.TrimPrefix(component.ID, "easyeda:")
	}
	optionalProperty(b, "LCSC", lcscID, 0, valueY-3.81)
	optionalProperty(b, "EasyEDA", easyedaID, 0, valueY-3.81)
	optionalProperty(b, "Manufacturer", component.Manufacturer, 0, valueY-5.08)
	optionalProperty(b, "MPN", component.MPN, 0, valueY-6.35)
	optionalProperty(b, "JLCPCB Class", component.JLCPCBClass, 0, valueY-7.62)
}

func writeSymbolUnits(b *strings.Builder, component *model.Component, symbol string) {
	for i, u := range component.Units {
		unitName := symbol + "_0_1"
		if len(component.Units) > 1 {
			unitName = fmt.Sprintf("%s_%d_1", symbol, i+1)
		}
		b.WriteString("  (symbol " + Quote(unitName) + "\n")
		for _, g := range u.Graphics {
			graphic(b, g)
		}
		for _, p := range u.Pins {
			pin(b, p)
		}
		b.WriteString("  )\n")
	}
}

func optionalProperty(b *strings.Builder, name, value string, x, y float64) {
	if value != "" {
		property(b, name, value, x, y, 0, true)
	}
}

func property(b *strings.Builder, name, value string, x, y, angle float64, hide bool) {
	fmt.Fprintf(b, "  (property %s %s (at %s %s %s) (effects (font (size 1.27 1.27))", Quote(name), Quote(value), Number(x), Number(y), Number(angle))
	if hide {
		b.WriteString(" (hide)")
	}
	b.WriteString("))\n")
}

func pin(b *strings.Builder, p model.Pin) {
	fmt.Fprintf(b, "    (pin %s %s (at %s %s %s) (length %s)", p.Electrical, p.Shape, Number(p.X), Number(p.Y), Number(p.Angle), Number(p.Length))
	writePinLabel(b, "name", p.Name, !p.NameVisible)
	writePinLabel(b, "number", p.Number, !p.NumberVisible)
	b.WriteString(")\n")
}

func writePinLabel(b *strings.Builder, kind, text string, hidden bool) {
	fmt.Fprintf(b, " (%s %s (effects (font (size 1.27 1.27))", kind, Quote(text))
	if hidden {
		b.WriteString(" (hide)")
	}
	b.WriteString("))")
}

func graphic(b *strings.Builder, g model.Graphic) {
	f := "none"
	if g.Fill == "outline" {
		f = "outline"
	}
	style := "(stroke (width " + Number(g.Width) + ") (type default)) (fill (type " + f + "))"
	switch g.Kind {
	case "rectangle":
		if len(g.Points) >= 2 {
			fmt.Fprintf(b, "    (rectangle (start %s) (end %s) %s (fill (type background)))\n", Point(g.Points[0].X, g.Points[0].Y), Point(g.Points[2].X, g.Points[2].Y), Stroke(g.Width))
		}
	case "circle":
		fmt.Fprintf(b, "    (circle (center %s) (radius %s) %s %s)\n", Point(g.Center.X, g.Center.Y), Number(g.RadiusX), Stroke(g.Width), style[strings.Index(style, "(fill"):])
	case "polyline":
		polyline(b, "polyline", g.Points, style)
	case "arc":
		if len(g.Points) >= 3 {
			fmt.Fprintf(b, "    (arc (start %s) (mid %s) (end %s) %s (fill (type none)))\n", Point(g.Points[0].X, g.Points[0].Y), Point(g.Points[len(g.Points)/2].X, g.Points[len(g.Points)/2].Y), Point(g.Points[len(g.Points)-1].X, g.Points[len(g.Points)-1].Y), Stroke(g.Width))
		}
	case "text":
		fmt.Fprintf(b, "    (text %s (at %s 0) (effects (font (size 1.27 1.27))))\n", Quote(g.Text), Point(g.Center.X, g.Center.Y))
	}
}

func polyline(b *strings.Builder, kind string, points []model.Point, style string) {
	if len(points) < 2 {
		return
	}
	b.WriteString("    (" + kind + " (pts")
	for _, p := range points {
		b.WriteString(" (xy " + Point(p.X, p.Y) + ")")
	}
	b.WriteString(") " + style + ")\n")
}

func symbolBounds(c *model.Component) (float64, float64, float64, float64) {
	bounds := symbolBoundsAccumulator{first: true}
	for _, u := range c.Units {
		for _, p := range u.Pins {
			addPinBounds(&bounds, p)
		}
		for _, g := range u.Graphics {
			addGraphicBounds(&bounds, g)
		}
	}
	return bounds.minX, bounds.minY, bounds.maxX, bounds.maxY
}

type symbolBoundsAccumulator struct {
	minX, minY, maxX, maxY float64
	first                  bool
}

func (b *symbolBoundsAccumulator) add(x, y float64) {
	if b.first {
		b.minX, b.maxX, b.minY, b.maxY = x, x, y, y
		b.first = false
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y > b.maxY {
		b.maxY = y
	}
}
func addPinBounds(b *symbolBoundsAccumulator, p model.Pin) {
	b.add(p.X, p.Y)
	switch p.Angle {
	case 0:
		b.add(p.X+p.Length, p.Y)
	case 90:
		b.add(p.X, p.Y+p.Length)
	case 180:
		b.add(p.X-p.Length, p.Y)
	case 270:
		b.add(p.X, p.Y-p.Length)
	}
}
func addGraphicBounds(b *symbolBoundsAccumulator, g model.Graphic) {
	for _, p := range g.Points {
		b.add(p.X, p.Y)
	}
	if g.Kind == "circle" {
		b.add(g.Center.X-g.RadiusX, g.Center.Y-g.RadiusY)
		b.add(g.Center.X+g.RadiusX, g.Center.Y+g.RadiusY)
	}
	if g.Kind == "text" {
		b.add(g.Center.X-g.Width/2, g.Center.Y-g.Width/2)
		b.add(g.Center.X+g.Width/2, g.Center.Y+g.Width/2)
	}
}
