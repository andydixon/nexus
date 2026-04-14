// Package frontend provides the embedded frontend assets for the Wails application.
package frontend

import "embed"

// Assets contains the built frontend files from the dist directory.
//
//go:embed all:dist
var Assets embed.FS
