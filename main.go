// Nexus Terminal – a desktop SSH, Telnet and RAW TCP client built with Wails v2.
// This is the main entry point for the application.
package main

import (
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	application "github.com/andydixon/nexus/internal/app"
	"github.com/andydixon/nexus/frontend"
)

func main() {
	nexus := application.NewApp()

	// The API service is created during Startup once storage is initialised.
	// We bind the App itself so Wails can call lifecycle hooks, and use
	// OnStartup to wire everything, then bind the API service separately.
	err := wails.Run(&options.App{
		Title:     "Nexus Terminal",
		Width:     1400,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: frontend.Assets,
		},
		BackgroundColour: &options.RGBA{R: 26, G: 26, B: 46, A: 255}, // #1a1a2e
		OnStartup:        nexus.Startup,
		OnShutdown:       nexus.Shutdown,
		OnDomReady:       nexus.DomReady,
		// macOS: enable the green fullscreen traffic-light button.
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Nexus Terminal",
				Message: "A premium desktop terminal client",
			},
		},
		// Windows: default decorations include maximise; fullscreen via F11.
		Windows: &windows.Options{},
		// Linux: use the default window manager decorations (fullscreen via WM).
		Linux: &linux.Options{},
		Bind: []interface{}{
			nexus.APIService(),
		},
	})
	if err != nil {
		log.Fatalf("failed to start Nexus Terminal: %v", err)
	}
}
