//go:build windows

// Kordo GUI. Runs without administrator rights and talks to the service
// over the named pipe. Build: see build/build.ps1 (tags desktop,production).
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

var frontendFS fs.FS

func main() {
	frontend, err := fs.Sub(assets, "frontend")
	if err != nil {
		log.Fatal(err)
	}
	frontendFS = frontend
	app := NewApp()
	hidden := false
	for _, a := range os.Args[1:] {
		if a == "--minimized" { // started by the login autostart entry: tray only
			hidden = true
		}
	}
	err = wails.Run(&options.App{
		StartHidden:       hidden,
		HideWindowOnClose: true, // the tray icon keeps Kordo reachable; "Выход" quits
		Title:             "Kordo",
		Width:             1180,
		Height:            760,
		MinWidth:          900,
		MinHeight:         600,
		AssetServer: &assetserver.Options{
			Assets: frontend,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 18, B: 26, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "kordo-gui-5b1f2c",
			OnSecondInstanceLaunch: app.onSecondInstance,
		},
		Bind: []interface{}{app},
		Windows: &windows.Options{
			Theme:                windows.SystemDefault,
			WebviewIsTransparent: false,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
