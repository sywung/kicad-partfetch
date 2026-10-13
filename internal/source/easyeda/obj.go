package easyeda

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/sywung/kicad-partfetch/internal/model"
)

type Vertex3D struct{ X, Y, Z float64 }

func ParseOBJVertices(data []byte) ([]Vertex3D, error) {
	var vertices []Vertex3D
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "v" {
			continue
		}
		if len(fields) < 4 {
			return nil, fmt.Errorf("OBJ vertex on line %d has fewer than 3 coordinates", line)
		}
		values := [3]float64{}
		for i := range values {
			value, err := strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("OBJ vertex on line %d: %w", line, err)
			}
			values[i] = value
		}
		vertices = append(vertices, Vertex3D{X: values[0], Y: values[1], Z: values[2]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(vertices) == 0 {
		return nil, fmt.Errorf("OBJ contains no vertices")
	}
	return vertices, nil
}

func StepPlacement(ref model.Model3DRef, vertices []Vertex3D) (model.Point3, [3]float64) {
	rotation := inverseRotation(ref.Rotation)
	if len(vertices) == 0 {
		return model.Point3{X: ref.OutlineCenter.X, Y: -ref.OutlineCenter.Y}, rotation
	}
	first := rotateVertex(vertices[0], ref.Rotation)
	minX, maxX, minY, maxY, minZ := first.X, first.X, first.Y, first.Y, first.Z
	for _, vertex := range vertices[1:] {
		v := rotateVertex(vertex, ref.Rotation)
		minX, maxX = math.Min(minX, v.X), math.Max(maxX, v.X)
		minY, maxY = math.Min(minY, v.Y), math.Max(maxY, v.Y)
		minZ = math.Min(minZ, v.Z)
	}
	return model.Point3{X: ref.OutlineCenter.X - (minX+maxX)/2, Y: -ref.OutlineCenter.Y - (minY+maxY)/2, Z: ref.Z*unitMM - minZ}, rotation
}

func rotateVertex(vertex Vertex3D, degrees [3]float64) Vertex3D {
	radians := math.Pi / 180
	rx, ry, rz := degrees[0]*radians, degrees[1]*radians, degrees[2]*radians
	vertex.Y, vertex.Z = vertex.Y*math.Cos(rx)-vertex.Z*math.Sin(rx), vertex.Y*math.Sin(rx)+vertex.Z*math.Cos(rx)
	vertex.X, vertex.Z = vertex.X*math.Cos(ry)+vertex.Z*math.Sin(ry), -vertex.X*math.Sin(ry)+vertex.Z*math.Cos(ry)
	vertex.X, vertex.Y = vertex.X*math.Cos(rz)-vertex.Y*math.Sin(rz), vertex.X*math.Sin(rz)+vertex.Y*math.Cos(rz)
	return vertex
}

func inverseRotation(rotation [3]float64) [3]float64 {
	for i, angle := range rotation {
		rotation[i] = math.Mod(360-math.Mod(angle, 360), 360)
	}
	return rotation
}
