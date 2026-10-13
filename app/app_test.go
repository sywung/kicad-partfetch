package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sywung/kicad-partfetch/internal/model"
	"github.com/sywung/kicad-partfetch/internal/search"
)

type fakeSource struct {
	component *model.Component
	fetchErr  error
	fetched   []string
}

func (f *fakeSource) ID() string { return "fake" }
func (f *fakeSource) Fetch(_ context.Context, id string) (*model.Component, error) {
	f.fetched = append(f.fetched, id)
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	copy := *f.component
	copy.ID = id
	return &copy, nil
}
func (f *fakeSource) FetchModel(context.Context, model.Model3DRef, string) ([]byte, error) {
	return nil, errors.New("unexpected model request")
}

type fakeSearch struct{ calls int }

func (f *fakeSearch) Search(context.Context, string, int) (search.Result, error) {
	f.calls++
	return search.Result{Items: []search.Item{{PartID: "C5"}}, Total: 1}, nil
}

func testComponent() *model.Component {
	return &model.Component{Value: "Widget", Reference: "U", Footprint: model.Footprint{Name: "SOT-23", Pads: []model.Pad{{Number: "1", Shape: "rect", Width: 1, Height: .5}}}, Units: []model.SymbolUnit{{Pins: []model.Pin{{Number: "1", Name: "IN", X: 0, Y: 0, Length: 2}}, Graphics: []model.Graphic{{Kind: "rectangle", Points: []model.Point{{X: 0, Y: 0}, {X: 2, Y: 0}, {X: 2, Y: 2}, {X: 0, Y: 2}}}}}}}
}

func TestSearchCNumberShortCircuitAndPreview(t *testing.T) {
	src := &fakeSource{component: testComponent()}
	lookup := &fakeSearch{}
	a := NewWithOptions(Options{Source: src, Searcher: lookup})
	got, err := a.Search("lcsc", "c12, C34\nC56", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []SearchItem{{PartID: "C12", Source: "lcsc"}, {PartID: "C34", Source: "lcsc"}, {PartID: "C56", Source: "lcsc"}}
	if !reflect.DeepEqual(got.Items, want) || got.Page != 2 || lookup.calls != 0 || len(src.fetched) != 0 {
		t.Fatalf("search=%+v calls=%d fetched=%v", got, lookup.calls, src.fetched)
	}
	pr, err := a.Preview("C99")
	if err != nil {
		t.Fatal(err)
	}
	if pr.PartID != "C99" || pr.Name != "Widget" || pr.Package != "SOT-23" || pr.PinCount != 1 || pr.PadCount != 1 || len(pr.SymbolSVGs) != 1 || pr.HasModel {
		t.Fatalf("preview=%+v", pr)
	}
}

func TestAddProgressEventsAreSequential(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	a := NewWithOptions(Options{Source: &fakeSource{component: testComponent()}, SettingsPath: settingsPath})
	if err := a.SaveSettings(Settings{OutDir: dir, LibName: "fixture", ModelBase: "absolute", With3D: false}); err != nil {
		t.Fatal(err)
	}
	var events []map[string]any
	a.emit = func(name string, payload any) {
		if name != "add:progress" {
			t.Errorf("event=%s", name)
		}
		events = append(events, payload.(map[string]any))
	}
	results, err := a.Add([]string{"C1", "C2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || len(events) != 2 {
		t.Fatalf("results=%d events=%d", len(results), len(events))
	}
	for i, event := range events {
		if event["index"] != i+1 || event["total"] != 2 {
			t.Errorf("event %d=%v", i, event)
		}
		if _, ok := event["result"].(AddResult); !ok {
			t.Errorf("event result has type %T", event["result"])
		}
	}
	if results[0].Symbol != "added" || results[1].Symbol != "exists" {
		t.Fatalf("results=%+v", results)
	}
	installed, err := a.ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Name != "Widget" || installed[0].Footprint != "SOT-23" || installed[0].LCSC != "C1" || installed[0].PartID != "C1" || !installed[0].HasFootprint || installed[0].HasModel {
		t.Fatalf("library entries=%+v", installed)
	}
}

func TestLibraryManagementCases(t *testing.T) {
	t.Run("RemoveFromLibrary empty input returns expected error", testRemoveFromLibraryRejectsEmptyInput)
	t.Run("RemoveFromLibrary result JSON is non-null", testRemoveFromLibraryResultSerializesWithoutNull)
	t.Run("ListLibrary reports artifact presence fields", testListLibraryReportsArtifactPresence)
}

func testRemoveFromLibraryRejectsEmptyInput(t *testing.T) {
	a := NewWithOptions(Options{SettingsPath: filepath.Join(t.TempDir(), "settings.json")})
	results, err := a.RemoveFromLibrary([]string{})
	if err == nil || err.Error() != "請先選擇要移除的項目" {
		t.Fatalf("RemoveFromLibrary() error=%v", err)
	}
	if results == nil || len(results) != 0 {
		t.Fatalf("results=%#v, want non-nil empty slice", results)
	}
}

func testRemoveFromLibraryResultSerializesWithoutNull(t *testing.T) {
	out := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	a := NewWithOptions(Options{SettingsPath: settingsPath})
	if err := a.SaveSettings(Settings{OutDir: out, LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	results, err := a.RemoveFromLibrary([]string{"Absent"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "null") || string(encoded) != `[{"Name":"Absent","Symbol":"missing","Footprint":"none","Model":"none","Error":""}]` {
		t.Fatalf("JSON=%s, want one non-null missing result", encoded)
	}
}

func testListLibraryReportsArtifactPresence(t *testing.T) {
	out := t.TempDir()
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	a := NewWithOptions(Options{SettingsPath: settingsPath})
	if err := a.SaveSettings(Settings{OutDir: out, LibName: "parts", ModelBase: "absolute"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "parts.kicad_sym"), []byte("(kicad_symbol_lib (version 1)\n  (symbol \"Present\" (property \"Footprint\" \"parts:Ready\") (property \"LCSC\" \"C1\"))\n  (symbol \"Missing\" (property \"Footprint\" \"parts:Absent\") (property \"LCSC\" \"C2\"))\n)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "parts.pretty"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "parts.pretty", "Ready.kicad_mod"), []byte(`(footprint "Ready" (model "ready.step"))`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(out, "parts.3dshapes"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "parts.3dshapes", "ready.step"), []byte("step"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := a.ListLibrary()
	if err != nil {
		t.Fatal(err)
	}
	want := []LibraryEntry{{Name: "Present", Footprint: "Ready", LCSC: "C1", PartID: "C1", HasFootprint: true, HasModel: true}, {Name: "Missing", Footprint: "Absent", LCSC: "C2", PartID: "C2"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("ListLibrary()=%+v, want %+v", entries, want)
	}
}

func TestOpenInFileManagerAndOpenURLRestrictions(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "fixture.pretty")
	if err := os.Mkdir(inside, 0755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	var openedPath, openedURL string
	a := NewWithOptions(Options{SettingsPath: settingsPath, OpenPath: func(_ context.Context, p string) error { openedPath = p; return nil }, OpenURL: func(_ context.Context, u string) error { openedURL = u; return nil }})
	if err := a.SaveSettings(Settings{OutDir: root, LibName: "parts", ModelBase: "absolute", With3D: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.OpenInFileManager(inside); err != nil {
		t.Fatal(err)
	}
	resolvedInside, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if openedPath != resolvedInside {
		t.Fatalf("opened=%q", openedPath)
	}
	if err := a.OpenInFileManager(filepath.Join(root, "..", "outside")); err == nil {
		t.Fatal("expected path restriction")
	}
	outside := t.TempDir()
	link := filepath.Join(root, "external-link")
	if err := os.Symlink(outside, link); err == nil {
		if err := a.OpenInFileManager(link); err == nil {
			t.Fatal("expected symlink path restriction")
		}
	}
	if err := a.OpenURL("http://example.com"); err == nil {
		t.Fatal("expected protocol restriction")
	}
	if err := a.OpenURL("https://example.com"); err != nil {
		t.Fatal(err)
	}
	if openedURL != "https://example.com" {
		t.Fatalf("opened URL=%q", openedURL)
	}
}

// The frontend reads these values as JSON; a nil slice would arrive as null and break `.length`.
func TestBoundResultsNeverSerializeNullSlices(t *testing.T) {
	// Arrange
	bare := &model.Component{Value: "Bare", Footprint: model.Footprint{Name: "FP"}}
	a := NewWithOptions(Options{Source: &fakeSource{component: bare}, Searcher: &emptySearch{}})

	// Act
	pr, err := a.Preview("C1")
	if err != nil {
		t.Fatal(err)
	}
	sr, err := a.Search("lcsc", "nothing", 1)
	if err != nil {
		t.Fatal(err)
	}

	// Assert
	for name, v := range map[string]any{"Preview": pr, "SearchResult": sr} {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "null") {
			t.Errorf("%s serializes a null: %s", name, raw)
		}
	}
}

type emptySearch struct{}

func (emptySearch) Search(context.Context, string, int) (search.Result, error) {
	return search.Result{}, nil
}
