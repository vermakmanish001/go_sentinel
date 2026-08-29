// Package web embeds the built dashboard so the API ships as a single binary.
//
// dist/ always contains at least a placeholder page, so `go build ./...` works
// without Node installed. `make ui` replaces it with the real Vite build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built frontend rooted at dist/.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
