// Alchementary Link – a desktop SSH, Telnet and RAW TCP client built with Wails v2.
// This is the main entry point for the application.
package main

import (
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"github.com/andydixon/link/frontend"
	application "github.com/andydixon/link/internal/app"
)

func buildAppMenu(app *application.App) *menu.Menu {
	mainMenu := menu.NewMenu()

	appMenu := menu.NewMenu()
	appMenu.AddText("About Alchementary Link", nil, func(_ *menu.CallbackData) {
		app.EmitOpenAbout()
	})
	appMenu.AddSeparator()
	appMenu.AddText("Quit Alchementary Link", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
		app.RequestQuit()
	})

	helpMenu := menu.NewMenu()
	helpMenu.AddText("About Alchementary Link", nil, func(_ *menu.CallbackData) {
		app.EmitOpenAbout()
	})

	mainMenu.Append(menu.SubMenu("Alchementary Link", appMenu))
	mainMenu.Append(menu.EditMenu())
	mainMenu.Append(menu.WindowMenu())
	mainMenu.Append(menu.SubMenu("Help", helpMenu))

	return mainMenu
}

func main() {
	link := application.NewApp()

	// The API service is created during Startup once storage is initialised.
	// We bind the App itself so Wails can call lifecycle hooks, and use
	// OnStartup to wire everything, then bind the API service separately.
	err := wails.Run(&options.App{
		Title:     "Alchementary Link",
		Width:     1400,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: frontend.Assets,
		},
		BackgroundColour: &options.RGBA{R: 26, G: 26, B: 46, A: 255}, // #1a1a2e
		OnStartup:        link.Startup,
		OnShutdown:       link.Shutdown,
		OnDomReady:       link.DomReady,
		Menu:             buildAppMenu(link),
		// macOS: enable the green fullscreen traffic-light button.
		Mac: &mac.Options{},
		// Windows: default decorations include maximise; fullscreen via F11.
		Windows: &windows.Options{},
		// Linux: use the default window manager decorations (fullscreen via WM).
		Linux: &linux.Options{},
		Bind: []interface{}{
			link.APIService(),
		},
	})
	if err != nil {
		log.Fatalf("failed to start Alchementary Link: %v", err)
	}
}
