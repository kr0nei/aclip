package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LogOptions struct {
	DownloadEvents      bool `json:"downloadEvents"`
	SubtitleEvents      bool `json:"subtitleEvents"`
	OpenRouterCalls     bool `json:"openRouterCalls"`
	OpenRouterResponses bool `json:"openRouterResponses"`
	JevCalls            bool `json:"jevCalls"`
	JevResponses        bool `json:"jevResponses"`
	Errors              bool `json:"errors"`
}

func defaultLogOptions() LogOptions {
	return LogOptions{
		DownloadEvents:      true,
		SubtitleEvents:      true,
		OpenRouterCalls:     true,
		OpenRouterResponses: true,
		JevCalls:            true,
		JevResponses:        true,
		Errors:              true,
	}
}

func effectiveLogOptions(options *LogOptions) LogOptions {
	if options == nil {
		return defaultLogOptions()
	}
	return *options
}

func currentLogFilePath() string {
	path, err := logFilePath()
	if err != nil {
		return ""
	}
	return path
}

func logFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the log folder: %w", err)
	}
	return filepath.Join(configDir, "aclip", "logs", "aclip.log"), nil
}

func (s *YouTubeService) logEvent(category, event string, fields map[string]string) {
	if !s.loggingEnabled(category) {
		return
	}
	path, err := logFilePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}

	s.logMu.Lock()
	defer s.logMu.Unlock()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "%s [%s] %s\n", time.Now().Format(time.RFC3339), strings.ToUpper(category), event); err != nil {
		return
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := strings.TrimSpace(fields[key])
		if value == "" {
			continue
		}
		lines := strings.Split(value, "\n")
		_, _ = fmt.Fprintf(file, "  %s: %s\n", key, strings.TrimSpace(lines[0]))
		for _, line := range lines[1:] {
			_, _ = fmt.Fprintf(file, "    %s\n", strings.TrimSpace(line))
		}
	}
	_, _ = fmt.Fprintln(file)
}

func (s *YouTubeService) loggingEnabled(category string) bool {
	s.settingsMu.Lock()
	stored, err := readSavedSettings()
	s.settingsMu.Unlock()
	if err != nil {
		return false
	}
	options := effectiveLogOptions(stored.Logs)
	switch category {
	case "downloads":
		return options.DownloadEvents
	case "subtitles":
		return options.SubtitleEvents
	case "openrouter_calls":
		return options.OpenRouterCalls
	case "openrouter_responses":
		return options.OpenRouterResponses
	case "jev_calls":
		return options.JevCalls
	case "jev_responses":
		return options.JevResponses
	case "errors":
		return options.Errors
	default:
		return false
	}
}

func (s *YouTubeService) OpenLogsFolder() error {
	path, err := logFilePath()
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create logs folder: %w", err)
	}
	return exec.Command("explorer.exe", directory).Start()
}
