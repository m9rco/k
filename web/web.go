// Package web embeds the frontend assets into the server binary so the whole
// application ships as a single static executable.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var embedded embed.FS

// FS returns the embedded frontend file system rooted at the static directory.
func FS() (fs.FS, error) {
	return fs.Sub(embedded, "static")
}
