// Package preview renders source-neutral KiCad models as themed SVG.
package preview

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/sywung/kicad-partfetch/internal/model"
)

type bounds struct {
	minX, minY, maxX, maxY float64
	set                    bool
}

func (b *bounds) add(x, y float64) {
	if !b.set {
		b.minX, b.maxX, b.minY, b.maxY = x, x, y, y
		b.set = true
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if y > b.maxY {
		b.maxY = y
	}
}
func (b bounds) box() (float64, float64, float64, float64) {
	if !b.set {
		return -1, -1, 2, 2
	}
	w, h := b.maxX-b.minX, b.maxY-b.minY
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	px, py := w*.08, h*.08
	return b.minX - px, b.minY - py, w + 2*px, h + 2*py
}

// SymbolSVG returns a single unit preview. Coordinates are flipped to SVG's downward Y axis.
func SymbolSVG(sym model.SymbolUnit, unit int) string {
	b := bounds{}
	for _, p := range sym.Pins {
		addPinBounds(&b, p)
	}
	for _, g := range sym.Graphics {
		for _, p := range g.Points {
			b.add(p.X, p.Y)
		}
		if g.Kind == "circle" {
			b.add(g.Center.X-g.RadiusX, g.Center.Y-g.RadiusY)
			b.add(g.Center.X+g.RadiusX, g.Center.Y+g.RadiusY)
		}
		if g.Kind == "text" {
			addTextBounds(&b, g.Center.X, g.Center.Y, g.Text, math.Max(g.Width, .5))
		}
	}
	x, y, w, h := b.box()
	var s strings.Builder
	fmt.Fprintf(&s, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"%s %s %s %s\" role=\"img\" aria-label=\"符號單元 %d\">", n(x), n(y), n(w), n(h), unit)
	fmt.Fprintf(&s, "<g transform=\"translate(0 %s) scale(1 -1)\">", n(2*y+h))
	for _, g := range sym.Graphics {
		renderSymbolGraphic(&s, g)
	}
	for _, p := range sym.Pins {
		renderPin(&s, p)
	}
	s.WriteString("</g></svg>")
	return s.String()
}

func addTextBounds(b *bounds, x, y float64, text string, size float64) {
	width := float64(utf8.RuneCountInString(text)) * size * .62
	b.add(x-width/2, y-size*.65)
	b.add(x+width/2, y+size*.35)
}
func renderSymbolGraphic(s *strings.Builder, g model.Graphic) {
	switch g.Kind {
	case "rectangle", "polyline", "arc":
		if len(g.Points) < 2 {
			return
		}
		tag := "polyline"
		if g.Kind == "rectangle" { // stored as four corners; close the outline
			tag = "polygon"
		}
		fmt.Fprintf(s, "<%s class=\"sym-body\" points=\"", tag)
		for _, p := range g.Points {
			fmt.Fprintf(s, "%s,%s ", n(p.X), n(p.Y))
		}
		s.WriteString("\" fill=\"none\"/>")
	case "circle":
		fmt.Fprintf(s, "<ellipse class=\"sym-body\" cx=\"%s\" cy=\"%s\" rx=\"%s\" ry=\"%s\" fill=\"none\"/>", n(g.Center.X), n(g.Center.Y), n(g.RadiusX), n(g.RadiusY))
	case "text":
		symbolText(s, "sym-text", g.Center.X, g.Center.Y, false, "middle", g.Text)
	}
}

const (
	pinNameSize = 1.27 // KiCad's default text height, mm
	pinNumSize  = 1.0
	pinNameGap  = 0.6 // distance from the pin's inner end to its name
	pinNumLift  = 0.3 // distance from the pin line to its number
	glyphAspect = 0.62
)

// pinAxis returns the unit vector from the connection point toward the body.
func pinAxis(p model.Pin) (float64, float64) {
	r := p.Angle * math.Pi / 180
	return math.Round(math.Cos(r)), math.Round(math.Sin(r))
}

func addPinBounds(b *bounds, p model.Pin) {
	b.add(p.X, p.Y)
	x, y := pinEnd(p)
	b.add(x, y)
	dx, dy := pinAxis(p)
	if p.NameVisible && p.Name != "" {
		reach := pinNameGap + float64(utf8.RuneCountInString(p.Name))*pinNameSize*glyphAspect
		b.add(x+dx*reach+dy*pinNameSize/2, y+dy*reach+dx*pinNameSize/2)
		b.add(x+dx*reach-dy*pinNameSize/2, y+dy*reach-dx*pinNameSize/2)
	}
	if p.NumberVisible && p.Number != "" {
		b.add(p.X+dy*(pinNumLift+pinNumSize), p.Y+dx*(pinNumLift+pinNumSize))
		b.add(p.X-dy*(pinNumLift+pinNumSize), p.Y-dx*(pinNumLift+pinNumSize))
	}
}

// symbolText writes upright text inside the Y-flipped symbol group; vertical text reads bottom to top.
func symbolText(s *strings.Builder, class string, x, y float64, vertical bool, anchor, text string) {
	rotate := ""
	if vertical {
		rotate = " rotate(-90)"
	}
	fmt.Fprintf(s, "<text class=\"%s\" transform=\"translate(%s %s) scale(1 -1)%s\" style=\"text-anchor:%s\">%s</text>",
		class, n(x), n(y), rotate, anchor, html.EscapeString(text))
}

func renderPin(s *strings.Builder, p model.Pin) {
	x2, y2 := pinEnd(p)
	fmt.Fprintf(s, "<line class=\"sym-pin\" x1=\"%s\" y1=\"%s\" x2=\"%s\" y2=\"%s\"/>", n(p.X), n(p.Y), n(x2), n(y2))
	dx, dy := pinAxis(p)
	vertical := dy != 0
	if p.NameVisible && p.Name != "" {
		anchor := "start"
		if dx < 0 || dy < 0 {
			anchor = "end"
		}
		symbolText(s, "sym-pin-name", x2+dx*pinNameGap, y2+dy*pinNameGap, vertical, anchor, p.Name)
	}
	if p.NumberVisible && p.Number != "" {
		midX, midY := (p.X+x2)/2, (p.Y+y2)/2
		if vertical {
			symbolText(s, "sym-pin-num", midX-pinNumLift-pinNumSize/2, midY, true, "middle", p.Number)
		} else {
			symbolText(s, "sym-pin-num", midX, midY+pinNumLift+pinNumSize/2, false, "middle", p.Number)
		}
	}
}
func pinEnd(p model.Pin) (float64, float64) {
	r := p.Angle * math.Pi / 180
	return p.X + p.Length*math.Cos(r), p.Y + p.Length*math.Sin(r)
}

// FootprintSVG renders copper, mask openings, holes, silkscreen and documentation layers.
func FootprintSVG(fp model.Footprint) string {
	b := bounds{}
	for _, p := range fp.Pads {
		rotation := p.Rotation * math.Pi / 180
		cosine, sine := math.Cos(rotation), math.Sin(rotation)
		for _, corner := range []model.Point{{X: -p.Width / 2, Y: -p.Height / 2}, {X: p.Width / 2, Y: -p.Height / 2}, {X: p.Width / 2, Y: p.Height / 2}, {X: -p.Width / 2, Y: p.Height / 2}} {
			b.add(p.X+corner.X*cosine-corner.Y*sine, p.Y+corner.X*sine+corner.Y*cosine)
		}
		for _, pt := range p.Points {
			b.add(p.X+pt.X*cosine-pt.Y*sine, p.Y+pt.X*sine+pt.Y*cosine)
		}
		if p.Number != "" {
			addTextBounds(&b, p.X, p.Y, p.Number, math.Max(.35, math.Min(.85, math.Min(p.Width, p.Height)*.34)))
		}
	}
	for _, h := range fp.NPTHoles {
		b.add(h.X-h.Diameter/2, h.Y-h.Diameter/2)
		b.add(h.X+h.Diameter/2, h.Y+h.Diameter/2)
	}
	for _, g := range fp.Graphics {
		for _, p := range g.Points {
			b.add(p.X, p.Y)
		}
		if g.Kind == "circle" {
			b.add(g.Center.X-g.Radius, g.Center.Y-g.Radius)
			b.add(g.Center.X+g.Radius, g.Center.Y+g.Radius)
		}
	}
	x, y, w, h := b.box()
	var s strings.Builder
	fmt.Fprintf(&s, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"%s %s %s %s\" role=\"img\" aria-label=\"%s 封裝\">", n(x), n(y), n(w), n(h), html.EscapeString(fp.Name))
	for _, g := range fp.Graphics {
		renderFPGraphic(&s, g)
	}
	for _, p := range fp.Pads {
		renderPad(&s, p)
	}
	for _, hole := range fp.NPTHoles {
		fmt.Fprintf(&s, "<circle class=\"fp-hole\" cx=\"%s\" cy=\"%s\" r=\"%s\"/>", n(hole.X), n(hole.Y), n(hole.Diameter/2))
	}
	s.WriteString("</svg>")
	return s.String()
}
func renderFPGraphic(s *strings.Builder, g model.FootprintGraphic) {
	class := layerClass(g.Layer)
	if len(g.Points) > 1 {
		fmt.Fprintf(s, "<polyline class=\"%s\" points=\"", class)
		for _, p := range g.Points {
			fmt.Fprintf(s, "%s,%s ", n(p.X), n(p.Y))
		}
		s.WriteString("\" fill=\"none\"/>")
		return
	}
	if g.Kind == "circle" {
		fmt.Fprintf(s, "<circle class=\"%s\" cx=\"%s\" cy=\"%s\" r=\"%s\" fill=\"none\"/>", class, n(g.Center.X), n(g.Center.Y), n(g.Radius))
	}
}
func renderPad(s *strings.Builder, p model.Pad) {
	class := "fp-cu-f"
	if p.Type == "thru_hole" || p.Type == "np_thru_hole" {
		class = "fp-cu-th"
	} else if p.Layer == 2 {
		class = "fp-cu-b"
	}
	shape := strings.ToLower(p.Shape)
	if shape == "ellipse" {
		if math.Abs(p.Width-p.Height) < 1e-5 {
			shape = "circle"
		} else {
			shape = "oval"
		}
	}
	transform := ""
	if p.Rotation != 0 {
		transform = fmt.Sprintf(" transform=\"rotate(%s %s %s)\"", n(p.Rotation), n(p.X), n(p.Y))
	}
	switch shape {
	case "circle":
		fmt.Fprintf(s, "<circle class=\"%s\" cx=\"%s\" cy=\"%s\" r=\"%s\"%s/>", class, n(p.X), n(p.Y), n(math.Min(p.Width, p.Height)/2), transform)
	case "custom", "polygon":
		s.WriteString("<polygon class=\"" + class + "\" points=\"")
		for _, pt := range p.Points {
			fmt.Fprintf(s, "%s,%s ", n(p.X+pt.X), n(p.Y+pt.Y))
		}
		fmt.Fprintf(s, "\"%s/>", transform)
	case "oval":
		fmt.Fprintf(s, "<rect class=\"%s\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\"%s/>", class, n(p.X-p.Width/2), n(p.Y-p.Height/2), n(p.Width), n(p.Height), n(math.Min(p.Width, p.Height)/2), transform)
	default:
		fmt.Fprintf(s, "<rect class=\"%s\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\"%s/>", class, n(p.X-p.Width/2), n(p.Y-p.Height/2), n(p.Width), n(p.Height), transform)
	}
	if p.Drill > 0 {
		if p.Slot > 0 {
			long, short := math.Max(p.Drill, p.Slot), math.Min(p.Drill, p.Slot)
			slotWidth, slotHeight := long, short
			if p.Width < p.Height {
				slotWidth, slotHeight = short, long
			}
			fmt.Fprintf(s, "<rect class=\"fp-hole\" x=\"%s\" y=\"%s\" width=\"%s\" height=\"%s\" rx=\"%s\"%s/>", n(p.X-slotWidth/2), n(p.Y-slotHeight/2), n(slotWidth), n(slotHeight), n(short/2), transform)
		} else {
			fmt.Fprintf(s, "<circle class=\"fp-hole\" cx=\"%s\" cy=\"%s\" r=\"%s\"/>", n(p.X), n(p.Y), n(p.Drill/2))
		}
	}
	if p.Number != "" {
		// Fit the label inside the pad: bounded by the short side and by the label's own width.
		glyphs := float64(utf8.RuneCountInString(p.Number))
		across := p.Width // labels are drawn horizontally, so the pad's on-screen width limits them
		if math.Mod(math.Abs(p.Rotation), 180) == 90 {
			across = p.Height
		}
		font := math.Min(math.Min(p.Width, p.Height)*.6, across/(glyphs*glyphAspect+.4))
		font = math.Max(.1, math.Min(.9, font))
		fmt.Fprintf(s, "<text class=\"fp-pad-num\" x=\"%s\" y=\"%s\" style=\"font-size:%spx\" text-anchor=\"middle\" dominant-baseline=\"central\">%s</text>", n(p.X), n(p.Y), n(font), html.EscapeString(p.Number))
	}
}
func layerClass(layer string) string {
	switch layer {
	case "F.Cu":
		return "fp-cu-f"
	case "B.Cu":
		return "fp-cu-b"
	case "F.SilkS", "B.SilkS":
		return "fp-silk"
	case "F.Fab", "B.Fab":
		return "fp-fab"
	case "F.Paste", "B.Paste":
		return "fp-paste"
	case "F.Mask", "B.Mask":
		return "fp-mask"
	case "F.CrtYd", "B.CrtYd":
		return "fp-crtyd"
	case "Edge.Cuts":
		return "fp-edge"
	default:
		return "fp-user"
	}
}
func n(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
