package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/sywung/kicad-partfetch/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	application := app.New()
	err := wails.Run(&options.App{
		Title:       "kicad-partfetch",
		Width:       1360,
		Height:      860,
		MinWidth:    1100,
		MinHeight:   700,
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   application.Startup,
		Bind:        []interface{}{application},
	})
	if err != nil {
		log.Fatal(err)
	}
}
