package main

import (
	_ "embed"

	"github.com/ssabv/123pan-strm-docker/internal/app"
)

//go:embed index.html
var indexHTML []byte

//go:embed settings.yaml
var settingsYAML []byte

func init() {
	app.SetEmbedded(indexHTML, settingsYAML)
}

func main() {
	app.RunServer()
}
