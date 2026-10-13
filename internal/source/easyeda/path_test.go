package easyeda

import (
	"math"
	"testing"
)

func TestSVGPathParsing(t *testing.T) {
	tests := []struct {
		name, path  string
		want        int
		first, last [2]float64
	}{
		{name: "comma and compact relative commands", path: "M0,0h10v10H5V5z", want: 6, first: [2]float64{0, 0}, last: [2]float64{0, 0}},
		{name: "absolute line with compact coordinates", path: "M10 20L30,40", want: 2, first: [2]float64{10, 20}, last: [2]float64{30, 40}},
		{name: "arc start midpoint and end", path: "M0,0 A10,10 0 0 1 10,10", want: 11, first: [2]float64{0, 0}, last: [2]float64{10, 10}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points, e := ParseSVGPath(tt.path)
			if e != nil {
				t.Fatal(e)
			}
			if len(points) != tt.want {
				t.Fatalf("points=%d, want %d", len(points), tt.want)
			}
			closePath(t, points[0].X, tt.first[0]*unitMM)
			closePath(t, points[0].Y, tt.first[1]*unitMM)
			closePath(t, points[len(points)-1].X, tt.last[0]*unitMM)
			closePath(t, points[len(points)-1].Y, tt.last[1]*unitMM)
			if tt.name == "arc start midpoint and end" {
				mid := points[len(points)/2]
				closePath(t, mid.X, 7.0710678*unitMM)
				closePath(t, mid.Y, 2.9289322*unitMM)
			}
		})
	}
}

func closePath(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-5 {
		t.Errorf("got %.8f, want %.8f", got, want)
	}
}
