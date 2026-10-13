package main

import (
	"context"
	"os"

	"github.com/sywung/kicad-partfetch/internal/cli"
	"github.com/sywung/kicad-partfetch/internal/source/easyeda"
)

func main() {
	client := easyeda.NewClient(easyeda.ClientOptions{})
	os.Exit(cli.Run(context.Background(), os.Args[1:], client, os.Stdout, os.Stderr))
}
