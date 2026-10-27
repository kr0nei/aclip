package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	videoService := &YouTubeService{}
	notifier := notifications.New()
	app := application.New(application.Options{
		Name:        "aclip",
		Description: "Download YouTube videos and find short-form clips",
		Services: []application.Service{
			application.NewService(videoService),
			application.NewService(notifier),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	videoService.app = app
	videoService.notifier = notifier

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "aclip",
		Width:            900,
		Height:           600,
		BackgroundColour: application.NewRGB(244, 246, 250),
		URL:              "/",
	})
	videoService.window = window

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
