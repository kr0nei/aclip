package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	videoService := &YouTubeService{}
	app := application.New(application.Options{
		Name:        "aclip",
		Description: "Download YouTube videos and chapter subtitles",
		Services: []application.Service{
			application.NewService(videoService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	videoService.app = app

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "aclip",
		Width:            900,
		Height:           600,
		BackgroundColour: application.NewRGB(244, 246, 250),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
