package kicad

import (
	"fmt"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func Footprint(component *model.Component, library, modelBase string) []byte {
	f := component.Footprint
	name := Sanitize(f.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "(footprint %s (version 20240108) (generator \"kicad-partfetch\") (generator_version \"0.1\") (layer \"F.Cu\")\n", Quote(name))
	through := false
	for _, p := range f.Pads {
		if p.Type == "thru_hole" || p.Type == "np_thru_hole" {
			through = true
		}
	}
	if through {
		b.WriteString("  (attr through_hole)\n")
	} else {
		b.WriteString("  (attr smd)\n")
	}
	_, minY, _, maxY := footprintBounds(f)
	fmt.Fprintf(&b, "  (property \"Reference\" \"REF**\" (at 0 %s 0) (layer \"F.SilkS\") (effects (font (size 1 1) (thickness 0.15))))\n", Number(minY-1))
	fmt.Fprintf(&b, "  (property \"Value\" %s (at 0 %s 0) (layer \"F.Fab\") (effects (font (size 1 1) (thickness 0.15))))\n", Quote(name), Number(maxY+1))
	for _, g := range f.Graphics {
		footprintGraphic(&b, g)
	}
	for _, p := range f.Pads {
		footprintPad(&b, p)
	}
	for _, h := range f.NPTHoles {
		footprintHole(&b, h)
	}
	if f.HasCourtyard {
		fmt.Fprintf(&b, "  (fp_rect (start %s) (end %s) (stroke (width 0.05) (type default)) (fill none) (layer \"F.CrtYd\"))\n", Point(f.Courtyard[0], f.Courtyard[1]), Point(f.Courtyard[2], f.Courtyard[3]))
	}
	for _, ref := range f.Models {
		offset, rot := ref.Placement, ref.StepRotation
		if !ref.Placed {
			offset, rot = easyeda.StepPlacement(ref, nil)
		}
		path := strings.TrimRight(modelBase, "/\\") + "/" + library + ".3dshapes/" + Sanitize(ref.Title) + ".step"
		fmt.Fprintf(&b, "  (model %s (offset (xyz %s %s %s)) (scale (xyz 1 1 1)) (rotate (xyz %s %s %s)))\n", Quote(path), Number(offset.X), Number(offset.Y), Number(offset.Z), Number(rot[0]), Number(rot[1]), Number(rot[2]))
	}
	b.WriteString(")\n")
	return []byte(b.String())
}

func footprintGraphic(b *strings.Builder, g model.FootprintGraphic) {
	switch g.Kind {
	case "polyline":
		if len(g.Points) < 2 {
			return
		}
		b.WriteString("  (fp_poly (pts")
		for _, p := range g.Points {
			fmt.Fprintf(b, " (xy %s)", Point(p.X, p.Y))
		}
		fill := "none"
		if g.Fill {
			fill = "solid"
		}
		fmt.Fprintf(b, ") %s (fill %s) (layer %s))\n", Stroke(g.Width), fill, Quote(g.Layer))
	case "track":
		for i := 0; i+1 < len(g.Points); i++ {
			fmt.Fprintf(b, "  (fp_line (start %s) (end %s) %s (layer %s))\n", Point(g.Points[i].X, g.Points[i].Y), Point(g.Points[i+1].X, g.Points[i+1].Y), Stroke(g.Width), Quote(g.Layer))
		}
	case "circle":
		fmt.Fprintf(b, "  (fp_circle (center %s) (end %s) %s (fill none) (layer %s))\n", Point(g.Center.X, g.Center.Y), Point(g.Center.X+g.Radius, g.Center.Y), Stroke(g.Width), Quote(g.Layer))
	case "rectangle":
		if len(g.Points) > 2 {
			fmt.Fprintf(b, "  (fp_rect (start %s) (end %s) %s (fill none) (layer %s))\n", Point(g.Points[0].X, g.Points[0].Y), Point(g.Points[2].X, g.Points[2].Y), Stroke(g.Width), Quote(g.Layer))
		}
	case "arc":
		if len(g.Points) >= 3 {
			fmt.Fprintf(b, "  (fp_arc (start %s) (mid %s) (end %s) %s (layer %s))\n", Point(g.Points[0].X, g.Points[0].Y), Point(g.Points[len(g.Points)/2].X, g.Points[len(g.Points)/2].Y), Point(g.Points[len(g.Points)-1].X, g.Points[len(g.Points)-1].Y), Stroke(g.Width), Quote(g.Layer))
		}
	}
}

func footprintPad(b *strings.Builder, p model.Pad) {
	layers := "\"F.Cu\" \"F.Paste\" \"F.Mask\""
	if p.Layer == 2 {
		layers = "\"B.Cu\" \"B.Paste\" \"B.Mask\""
	}
	if p.Type == "thru_hole" || p.Type == "np_thru_hole" {
		layers = "\"*.Cu\" \"*.Mask\""
	}
	width, height := p.Width, p.Height
	shape := p.Shape
	if shape == "custom" {
		width, height = .2, .2
		shape = "custom"
	}
	fmt.Fprintf(b, "  (pad %s %s %s (at %s %s", Quote(p.Number), p.Type, shape, Number(p.X), Number(p.Y))
	if p.Rotation != 0 {
		fmt.Fprintf(b, " %s", Number(p.Rotation))
	}
	fmt.Fprintf(b, ") (size %s %s) ", Number(width), Number(height))
	if p.Drill > 0 {
		if p.Slot > 0 {
			long := p.Slot
			if long < p.Drill {
				long = p.Drill
			}
			short := p.Drill
			if p.Slot < short {
				short = p.Slot
			}
			if p.Width >= p.Height {
				fmt.Fprintf(b, "(drill oval %s %s) ", Number(long), Number(short))
			} else {
				fmt.Fprintf(b, "(drill oval %s %s) ", Number(short), Number(long))
			}
		} else {
			fmt.Fprintf(b, "(drill %s) ", Number(p.Drill))
		}
	}
	fmt.Fprintf(b, "(layers %s)", layers)
	if shape == "custom" {
		b.WriteString(" (options (clearance outline) (anchor rect)) (primitives (gr_poly (pts")
		for _, pt := range p.Points {
			fmt.Fprintf(b, " (xy %s)", Point(pt.X, pt.Y))
		}
		b.WriteString(") (width 0) (fill yes)))")
	}
	b.WriteString(")\n")
}

func footprintHole(b *strings.Builder, h model.Hole) {
	fmt.Fprintf(b, "  (pad \"\" np_thru_hole circle (at %s %s) (size %s %s) (drill %s) (layers \"*.Cu\" \"*.Mask\"))\n", Number(h.X), Number(h.Y), Number(h.Diameter), Number(h.Diameter), Number(h.Diameter))
}

func footprintBounds(f model.Footprint) (float64, float64, float64, float64) {
	if f.HasBounds {
		return f.Bounds[0], f.Bounds[1], f.Bounds[2], f.Bounds[3]
	}
	minX, minY, maxX, maxY := 0.0, 0.0, 0.0, 0.0
	first := true
	add := func(x, y float64) {
		if first {
			minX, maxX, minY, maxY = x, x, y, y
			first = false
			return
		}
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	for _, p := range f.Pads {
		add(p.X-p.Width/2, p.Y-p.Height/2)
		add(p.X+p.Width/2, p.Y+p.Height/2)
	}
	for _, h := range f.NPTHoles {
		add(h.X-h.Diameter/2, h.Y-h.Diameter/2)
		add(h.X+h.Diameter/2, h.Y+h.Diameter/2)
	}
	for _, g := range f.Graphics {
		for _, p := range g.Points {
			add(p.X, p.Y)
		}
		if g.Kind == "circle" {
			add(g.Center.X-g.Radius, g.Center.Y-g.Radius)
			add(g.Center.X+g.Radius, g.Center.Y+g.Radius)
		}
	}
	if first {
		return 0, 0, 0, 0
	}
	return minX, minY, maxX, maxY
}
