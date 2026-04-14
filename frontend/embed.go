// Package frontend provides the embedded frontend assets for the Wails application.
package frontend

import "embed"

// Assets contains the frontend files served by the Wails asset server.
// We embed the root index.html and the entire src/ tree (stylesheets,
// scripts, vendor libs and static assets) so that `go build` works
// without a separate npm build step.
//
//go:embed index.html
//go:embed all:src
var Assets embed.FS
