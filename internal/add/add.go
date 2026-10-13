// Package add coordinates fetching models and writing a component to a KiCad library.
package add

import (
	"context"
	"fmt"

	"github.com/sywung/kicad-partfetch/internal/library"
	"github.com/sywung/kicad-partfetch/internal/source"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

type Result struct {
	ID, Title     string
	Library       library.Result
	Err           error
	UserWarnings  []string
	WarningErrors []error
}

func Process(ctx context.Context, id string, src source.Source, opts library.Options, no3d bool) Result {
	r := Result{ID: id}
	component, err := src.Fetch(ctx, id)
	if err != nil {
		r.Err = err
		return r
	}
	r.Title = component.Title
	userWarnings := append([]string{}, component.Warnings...)
	originalWarnings := len(component.Warnings)
	models := map[string][]byte{}
	if !no3d {
		for i := range component.Footprint.Models {
			ref := component.Footprint.Models[i]
			step, stepErr := src.FetchModel(ctx, ref, "step")
			obj, objErr := src.FetchModel(ctx, ref, "obj")
			var vertices []easyeda.Vertex3D
			if objErr == nil {
				vertices, objErr = easyeda.ParseOBJVertices(obj)
				if objErr != nil {
					objErr = fmt.Errorf("%w: %w", easyeda.ErrBadData, objErr)
				}
			}
			if objErr != nil {
				component.Warnings = append(component.Warnings, "3D 位置未經校正: "+objErr.Error())
				userWarnings = append(userWarnings, "3D 位置未經校正")
				r.WarningErrors = append(r.WarningErrors, objErr)
			} else {
				models[ref.UUID+".obj"] = obj
			}
			placement, rotation := easyeda.StepPlacement(ref, vertices)
			ref.Placement, ref.StepRotation, ref.Placed = placement, rotation, true
			component.Footprint.Models[i] = ref
			if stepErr != nil {
				component.Warnings = append(component.Warnings, "3D model download failed: "+stepErr.Error())
				userWarnings = append(userWarnings, "3D 模型下載失敗")
				r.WarningErrors = append(r.WarningErrors, stepErr)
				continue
			}
			models[ref.UUID+".step"] = step
		}
	}
	if no3d {
		copyComponent := *component
		fp := component.Footprint
		fp.Models = nil
		copyComponent.Footprint = fp
		component = &copyComponent
	}
	r.Library, r.Err = library.Write(opts, component, models)
	for _, warning := range r.Library.Warnings[originalWarnings+len(r.WarningErrors):] {
		userWarnings = append(userWarnings, warning)
	}
	r.UserWarnings = userWarnings
	return r
}

func FriendlyError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("無法加入元件：%s", err.Error())
}
