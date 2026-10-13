// Package source provides component data providers.
package source

import (
	"context"

	"github.com/sywung/kicad-partfetch/internal/model"
)

type Source interface {
	ID() string
	Fetch(ctx context.Context, partID string) (*model.Component, error)
	FetchModel(ctx context.Context, ref model.Model3DRef, format string) ([]byte, error)
}
