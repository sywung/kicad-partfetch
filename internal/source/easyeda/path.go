package easyeda

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

var pathToken = regexp.MustCompile(`[A-Za-z]|[-+]?(?:\d*\.)?\d+(?:[eE][-+]?\d+)?`)

type pathState struct {
	tokens               []string
	index                int
	command              byte
	x, y, startX, startY float64
	points               []model.Point
}

func ParseSVGPath(path string) ([]model.Point, error) {
	tokens := pathToken.FindAllString(path, -1)
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty SVG path")
	}
	s := pathState{tokens: tokens}
	for s.index < len(s.tokens) {
		if err := s.step(); err != nil {
			return nil, err
		}
	}
	return s.points, nil
}

func (s *pathState) step() error {
	if len(s.tokens[s.index]) == 1 && isCommand(s.tokens[s.index][0]) {
		s.command = s.tokens[s.index][0]
		s.index++
		if s.command == 'Z' || s.command == 'z' {
			s.x, s.y = s.startX, s.startY
			s.add(s.x, s.y)
			return nil
		}
	}
	if s.command == 0 {
		return fmt.Errorf("SVG path missing command")
	}
	relative := s.command >= 'a' && s.command <= 'z'
	upper := s.command
	if relative {
		upper -= 32
	}
	switch upper {
	case 'M', 'L':
		return s.pair(upper, relative)
	case 'H', 'V':
		return s.axis(upper, relative)
	case 'A':
		return s.arc(relative)
	case 'C':
		return s.cubic(relative)
	default:
		return fmt.Errorf("unsupported SVG command %c", s.command)
	}
}

func (s *pathState) pair(command byte, relative bool) error {
	values, e := s.readNumbers(2)
	if e != nil {
		return e
	}
	x, y := values[0], values[1]
	if relative {
		x += s.x
		y += s.y
	}
	s.x, s.y = x, y
	if command == 'M' {
		s.startX, s.startY = x, y
		if relative {
			s.command = 'l'
		} else {
			s.command = 'L'
		}
	}
	s.add(x, y)
	return nil
}

func (s *pathState) axis(command byte, relative bool) error {
	values, e := s.readNumbers(1)
	if e != nil {
		return e
	}
	if command == 'H' {
		if relative {
			s.x += values[0]
		} else {
			s.x = values[0]
		}
	} else if relative {
		s.y += values[0]
	} else {
		s.y = values[0]
	}
	s.add(s.x, s.y)
	return nil
}

func (s *pathState) arc(relative bool) error {
	v, e := s.readNumbers(7)
	if e != nil {
		return e
	}
	x, y := v[5], v[6]
	if relative {
		x += s.x
		y += s.y
	}
	points, e := arcPoints(s.x, s.y, v[0], v[1], v[2], v[3] == 1, v[4] == 1, x, y)
	if e != nil {
		return e
	}
	s.points = append(s.points, points[1:]...)
	s.x, s.y = x, y
	return nil
}

func (s *pathState) cubic(relative bool) error {
	v, e := s.readNumbers(6)
	if e != nil {
		return e
	}
	originX, originY := s.x, s.y
	endX, endY := v[4], v[5]
	if relative {
		for i := range v {
			v[i] += originAxis(i, originX, originY)
		}
		endX, endY = v[4], v[5]
	}
	for i := 1; i <= 12; i++ {
		t := float64(i) / 12
		u := 1 - t
		x := u*u*u*originX + 3*u*u*t*v[0] + 3*u*t*t*v[2] + t*t*t*endX
		y := u*u*u*originY + 3*u*u*t*v[1] + 3*u*t*t*v[3] + t*t*t*endY
		s.add(x, y)
	}
	s.x, s.y = endX, endY
	return nil
}

func originAxis(index int, x, y float64) float64 {
	if index%2 == 0 {
		return x
	}
	return y
}
func (s *pathState) readNumbers(count int) ([]float64, error) {
	if s.index+count > len(s.tokens) {
		return nil, fmt.Errorf("incomplete %c command", s.command)
	}
	out := make([]float64, count)
	for i := range out {
		n, e := pathNum(s.tokens[s.index+i])
		if e != nil {
			return nil, e
		}
		out[i] = n
	}
	s.index += count
	return out, nil
}
func (s *pathState) add(x, y float64) { s.points = append(s.points, mmPoint(x, y)) }

func arcPoints(x1, y1, rx, ry, rotation float64, large, sweep bool, x2, y2 float64) ([]model.Point, error) {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 || x1 == x2 && y1 == y2 {
		return []model.Point{mmPoint(x1, y1), mmPoint(x2, y2)}, nil
	}
	centerX, centerY, rx, ry, err := arcCenter(x1, y1, x2, y2, rx, ry, rotation, large == sweep)
	if err != nil {
		return nil, err
	}
	start, delta := arcAngles(x1, y1, x2, y2, centerX, centerY, rx, ry, rotation, sweep)
	steps := int(math.Ceil(math.Abs(delta) / (math.Pi / 18)))
	if steps < 2 {
		steps = 2
	}
	if steps%2 != 0 {
		steps++
	}
	points := make([]model.Point, 0, steps+1)
	for i := 0; i <= steps; i++ {
		a := start + delta*float64(i)/float64(steps)
		px, py := arcCoordinate(centerX, centerY, rx, ry, rotation, a)
		points = append(points, mmPoint(px, py))
	}
	return points, nil
}

func arcCenter(x1, y1, x2, y2, rx, ry, rotation float64, negate bool) (float64, float64, float64, float64, error) {
	phi := rotation * math.Pi / 180
	cp, sp := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	xp, yp := cp*dx+sp*dy, -sp*dx+cp*dy
	lambda := xp*xp/(rx*rx) + yp*yp/(ry*ry)
	if lambda > 1 {
		scale := math.Sqrt(lambda)
		rx *= scale
		ry *= scale
	}
	num := rx*rx*ry*ry - rx*rx*yp*yp - ry*ry*xp*xp
	den := rx*rx*yp*yp + ry*ry*xp*xp
	if den == 0 {
		return 0, 0, rx, ry, fmt.Errorf("invalid SVG arc")
	}
	coef := math.Sqrt(math.Max(0, num/den))
	if negate {
		coef = -coef
	}
	cxp := coef * rx * yp / ry
	cyp := -coef * ry * xp / rx
	cx := cp*cxp - sp*cyp + (x1+x2)/2
	cy := sp*cxp + cp*cyp + (y1+y2)/2
	return cx, cy, rx, ry, nil
}

func arcAngles(x1, y1, x2, y2, cx, cy, rx, ry, rotation float64, sweep bool) (float64, float64) {
	phi := rotation * math.Pi / 180
	cp, sp := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	xp, yp := cp*dx+sp*dy, -sp*dx+cp*dy
	cxp, cyp := cp*(cx-(x1+x2)/2)+sp*(cy-(y1+y2)/2), -sp*(cx-(x1+x2)/2)+cp*(cy-(y1+y2)/2)
	ux, uy := (xp-cxp)/rx, (yp-cyp)/ry
	vx, vy := (-xp-cxp)/rx, (-yp-cyp)/ry
	start := math.Atan2(uy, ux)
	delta := math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	}
	if sweep && delta < 0 {
		delta += 2 * math.Pi
	}
	return start, delta
}

func arcCoordinate(cx, cy, rx, ry, rotation, angle float64) (float64, float64) {
	phi := rotation * math.Pi / 180
	return cx + math.Cos(phi)*rx*math.Cos(angle) - math.Sin(phi)*ry*math.Sin(angle), cy + math.Sin(phi)*rx*math.Cos(angle) + math.Cos(phi)*ry*math.Sin(angle)
}
func mmPoint(x, y float64) model.Point  { return model.Point{X: x * unitMM, Y: y * unitMM} }
func pathNum(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
func isCommand(c byte) bool             { return strings.ContainsRune("MmLlHhVvAaCcZz", rune(c)) }
