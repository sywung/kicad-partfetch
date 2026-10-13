package easyeda

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"math"
	"net/http"
	"os"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/model"
)

func TestStepPlacementMatchesFixtureModels(t *testing.T) {
	wants := map[string]struct {
		offset   model.Point3
		rotation [3]float64
	}{
		"C2337":   {model.Point3{X: 49.53}, [3]float64{0, 0, 0}},
		"C134092": {model.Point3{Y: -0.93}, [3]float64{0, 0, 180}},
		"C16214":  {model.Point3{X: -3.65, Y: 2.275}, [3]float64{0, 0, 90}},
		"C6186":   {model.Point3{X: -0.1, Z: 0.0494}, [3]float64{0, 0, 180}},
		"C5446":   {model.Point3{}, [3]float64{0, 0, 270}},
	}
	for id, want := range wants {
		t.Run(id, func(t *testing.T) {
			ref, vertices := loadPlacementFixture(t, id)
			offset, rotation := StepPlacement(ref, vertices)
			assertPoint3Near(t, offset, want.offset)
			for i := range rotation {
				closeField(t, "rotation", rotation[i], want.rotation[i])
			}
		})
	}
}

func loadPlacementFixture(t *testing.T, id string) (model.Model3DRef, []Vertex3D) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + id + ".json")
	if err != nil {
		t.Fatal(err)
	}
	component, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := os.ReadFile("testdata/obj/" + id + ".obj")
	if err != nil {
		t.Fatal(err)
	}
	vertices, err := ParseOBJVertices(obj)
	if err != nil {
		t.Fatal(err)
	}
	return component.Footprint.Models[0], vertices
}

func TestModelOutlineCenterUsesChildPolylineBounds(t *testing.T) {
	data := `{"attrs":{"c_etype":"outline3D","uuid":"u","title":"m","c_origin":"50,60","z":"2","c_rotation":"0,0,0"},"childNodes":[{"nodeName":"polyline","attrs":{"points":"4,10 14,30"}}]}`
	ref, err := parseModel([]string{"SVGNODE", data}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	closeField(t, "outline center x", ref.OutlineCenter.X, 7*unitMM)
	closeField(t, "outline center y", ref.OutlineCenter.Y, 16*unitMM)
	data = `{"attrs":{"c_etype":"outline3D","uuid":"u","title":"m","c_origin":"10,20","z":"0","c_rotation":"0,0,0"}}`
	fallback, err := parseModel([]string{"SVGNODE", data}, 2, 4)
	if err != nil || fallback.OutlineCenter != fallback.Origin {
		t.Fatalf("missing outline fallback=%+v err=%v", fallback, err)
	}
}

func TestParseOBJVerticesAndRejectsMissingVertices(t *testing.T) {
	vertices, err := ParseOBJVertices([]byte("# model\nv 1 2 3\nf 1 2 3\nv -4 5 6\n"))
	if err != nil || len(vertices) != 2 || vertices[1] != (Vertex3D{X: -4, Y: 5, Z: 6}) {
		t.Fatalf("vertices=%v err=%v", vertices, err)
	}
	if _, err := ParseOBJVertices([]byte("# no vertices\nf 1 2 3\n")); err == nil {
		t.Fatal("OBJ without vertices was accepted")
	}
}

func TestFetchModelGunzipByMagicForOBJAndSTEP(t *testing.T) {
	for _, format := range []string{"obj", "step"} {
		t.Run(format, func(t *testing.T) {
			plain := "solid fixture\nendsolid fixture\n"
			if format == "obj" {
				plain = "v 1 2 3\n"
			}
			compressed := gzipBytes(t, []byte(plain))
			client := NewClient(ClientOptions{NoCache: true, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(compressed)), Header: make(http.Header), Request: r}, nil
			})})
			data, err := client.FetchModel(context.Background(), model.Model3DRef{UUID: "fixture"}, format)
			if err != nil || string(data) != plain {
				t.Fatalf("data=%q err=%v", data, err)
			}
			if format == "obj" {
				vertices, err := ParseOBJVertices(data)
				if err != nil || len(vertices) != 1 {
					t.Fatalf("decompressed OBJ not parseable: vertices=%v err=%v", vertices, err)
				}
			}
		})
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func assertPoint3Near(t *testing.T, got, want model.Point3) {
	t.Helper()
	for _, value := range []struct {
		name      string
		got, want float64
	}{{"x", got.X, want.X}, {"y", got.Y, want.Y}, {"z", got.Z, want.Z}} {
		if math.Abs(value.got-value.want) > .01 {
			t.Errorf("offset %s=%.4f, want %.4f", value.name, value.got, value.want)
		}
	}
}

func TestFallbackPlacementUsesOutlineAndInverseRotation(t *testing.T) {
	ref := model.Model3DRef{OutlineCenter: model.Point{X: 1.25, Y: -2}, Z: 3, Rotation: [3]float64{90, 180, -90}}
	offset, rotation := StepPlacement(ref, nil)
	if offset != (model.Point3{X: 1.25, Y: 2}) || rotation != [3]float64{270, 180, 90} {
		t.Fatalf("fallback placement=(%+v,%v)", offset, rotation)
	}
}
