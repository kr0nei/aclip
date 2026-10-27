package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type videoMetadata struct {
	DurationSeconds float64 `json:"durationSeconds,omitempty"`
	SizeBytes       int64   `json:"sizeBytes"`
	Width           int     `json:"width,omitempty"`
	Height          int     `json:"height,omitempty"`
	VideoCodec      string  `json:"videoCodec,omitempty"`
	AudioCodec      string  `json:"audioCodec,omitempty"`
}

func applyVideoMetadata(chapter *DownloadedChapter, metadata videoMetadata) {
	chapter.DurationSeconds = metadata.DurationSeconds
	chapter.SizeBytes = metadata.SizeBytes
	chapter.Width = metadata.Width
	chapter.Height = metadata.Height
	chapter.VideoCodec = metadata.VideoCodec
	chapter.AudioCodec = metadata.AudioCodec
}

func inspectVideo(ctx context.Context, path string) (videoMetadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return videoMetadata{}, err
	}
	metadata := videoMetadata{SizeBytes: info.Size()}
	executable, err := exec.LookPath("ffprobe.exe")
	if err != nil {
		return metadata, nil
	}
	cmd := exec.CommandContext(ctx, executable,
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type,codec_name,width,height",
		"-of", "json", path,
	)
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return metadata, ctx.Err()
		}
		return metadata, nil
	}
	return parseVideoMetadata(output, metadata.SizeBytes), nil
}

func parseVideoMetadata(output []byte, sizeBytes int64) videoMetadata {
	var probe struct {
		Streams []struct {
			Type   string `json:"codec_type"`
			Codec  string `json:"codec_name"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	metadata := videoMetadata{SizeBytes: sizeBytes}
	if json.Unmarshal(output, &probe) != nil {
		return metadata
	}
	metadata.DurationSeconds, _ = strconv.ParseFloat(strings.TrimSpace(probe.Format.Duration), 64)
	for _, stream := range probe.Streams {
		switch stream.Type {
		case "video":
			metadata.VideoCodec = stream.Codec
			metadata.Width = stream.Width
			metadata.Height = stream.Height
		case "audio":
			metadata.AudioCodec = stream.Codec
		}
	}
	return metadata
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "Length unavailable"
	}
	total := int(seconds + 0.5)
	hours := total / 3600
	minutes := (total % 3600) / 60
	remaining := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, remaining)
	}
	return fmt.Sprintf("%d:%02d", minutes, remaining)
}

func formatFileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(size)
	for _, unit := range units {
		value /= 1024
		if value < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%d B", size)
}
