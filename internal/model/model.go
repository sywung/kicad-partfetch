// Package model defines the source-neutral, millimetre-based component model.
package model

type Point struct{ X, Y float64 }
type Point3 struct{ X, Y, Z float64 }

type Pin struct {
	Number, Name, Electrical, Shape string
	NameVisible, NumberVisible      bool
	X, Y, Angle, Length             float64
}

type Graphic struct {
	Kind, Fill string
	Points     []Point
	Center     Point
	RadiusX    float64
	RadiusY    float64
	Width      float64
	Text       string
}

type SymbolUnit struct {
	Pins     []Pin
	Graphics []Graphic
}

type Pad struct {
	Number, Shape, Type                        string
	X, Y, Width, Height, Drill, Slot, Rotation float64
	Layer                                      int
	Points                                     []Point
}

type Hole struct{ X, Y, Diameter float64 }

type Model3DRef struct {
	UUID, Title   string
	Origin        Point
	OutlineCenter Point
	Z             float64
	Rotation      [3]float64
	Placement     Point3
	StepRotation  [3]float64
	Placed        bool
}

type FootprintGraphic struct {
	Kind, Layer   string
	Points        []Point
	Center        Point
	Radius, Width float64
	Fill          bool
}

type Footprint struct {
	Name         string
	Pads         []Pad
	NPTHoles     []Hole
	Graphics     []FootprintGraphic
	Courtyard    [4]float64 // xmin, ymin, xmax, ymax
	HasCourtyard bool
	Bounds       [4]float64 // component bounds before courtyard clearance
	HasBounds    bool
	Models       []Model3DRef
}

type Component struct {
	ID, Title, Reference, Value    string
	Datasheet                      string
	Manufacturer, MPN, JLCPCBClass string
	Units                          []SymbolUnit
	Footprint                      Footprint
	Warnings                       []string
}
