package main

import (
	_ "embed"

	"github.com/ssabv/123pan-strm-docker/internal/app"
)

//go:embed index.html
var indexHTML []byte

//go:embed settings.yaml
var settingsYAML []byte

//go:embed archive.html
var archiveHTML []byte

func init() {
	app.SetEmbedded(indexHTML, settingsYAML)
	app.SetArchiveEmbedded(archiveHTML)
}

func main() {
	app.RunServer()
}
