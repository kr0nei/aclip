package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zalando/go-keyring"
)

const (
	credentialService = "aclip"
	credentialName    = "openrouter-api-key"
	defaultModel      = "openai/gpt-4.1-mini"
	defaultTheme      = "light"
	defaultClipPrompt = ``
	defaultCustomTags = ``
)

type AppSettings struct {
	DownloadDirectory    string     `json:"downloadDirectory"`
	OpenRouterModel      string     `json:"openRouterModel"`
	ClipPrompt           string     `json:"clipPrompt"`
	CustomTags           string     `json:"customTags"`
	OpenRouterConfigured bool       `json:"openRouterConfigured"`
	NotificationsEnabled bool       `json:"notificationsEnabled"`
	Theme                string     `json:"theme"`
	RecentFolders        []string   `json:"recentFolders,omitempty"`
	Logs                 LogOptions `json:"logs"`
	LogFilePath          string     `json:"logFilePath"`
}

type SettingsInput struct {
	DownloadDirectory    string     `json:"downloadDirectory"`
	OpenRouterModel      string     `json:"openRouterModel"`
	ClipPrompt           string     `json:"clipPrompt"`
	CustomTags           string     `json:"customTags"`
	NotificationsEnabled bool       `json:"notificationsEnabled"`
	Theme                string     `json:"theme"`
	Logs                 LogOptions `json:"logs"`
}

type savedSettings struct {
	DownloadDirectory    string      `json:"downloadDirectory"`
	OpenRouterModel      string      `json:"openRouterModel"`
	ClipPrompt           string      `json:"clipPrompt"`
	CustomTags           string      `json:"customTags"`
	NotificationsEnabled bool        `json:"notificationsEnabled"`
	Theme                string      `json:"theme"`
	RecentFolders        []string    `json:"recentFolders,omitempty"`
	Logs                 *LogOptions `json:"logs,omitempty"`
}

func (s *YouTubeService) GetSettings() (AppSettings, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()

	stored, err := readSavedSettings()
	if err != nil {
		return AppSettings{}, err
	}
	configured := false
	if _, err := keyring.Get(credentialService, credentialName); err == nil {
		configured = true
	} else if !errors.Is(err, keyring.ErrNotFound) {
		return AppSettings{}, fmt.Errorf("read OpenRouter credential status: %w", err)
	}
	return publicSettings(stored, configured), nil
}

func (s *YouTubeService) SaveSettings(input SettingsInput) (AppSettings, error) {
	model := strings.TrimSpace(input.OpenRouterModel)
	if model == "" {
		model = defaultModel
	}
	if len(model) > 200 || strings.ContainsAny(model, "\r\n") {
		return AppSettings{}, fmt.Errorf("enter a valid OpenRouter model ID")
	}
	clipPrompt := strings.TrimSpace(input.ClipPrompt)
	if clipPrompt == "" {
		clipPrompt = defaultClipPrompt
	}
	if utf8.RuneCountInString(clipPrompt) > 12_000 || strings.ContainsRune(clipPrompt, 0) {
		return AppSettings{}, fmt.Errorf("the clip analysis prompt must be 12,000 characters or fewer")
	}
	theme := strings.TrimSpace(input.Theme)
	if theme == "" {
		theme = defaultTheme
	}
	if theme != "light" && theme != "dark" {
		return AppSettings{}, fmt.Errorf("theme must be light or dark")
	}

	directory := strings.TrimSpace(input.DownloadDirectory)
	if directory == "" {
		var err error
		directory, err = defaultDownloadDirectory()
		if err != nil {
			return AppSettings{}, err
		}
	}
	if !filepath.IsAbs(directory) {
		return AppSettings{}, fmt.Errorf("the download folder must be an absolute path")
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	current, err := readSavedSettings()
	if err != nil {
		return AppSettings{}, err
	}
	stored := savedSettings{
		DownloadDirectory:    filepath.Clean(directory),
		OpenRouterModel:      model,
		ClipPrompt:           clipPrompt,
		NotificationsEnabled: input.NotificationsEnabled,
		Theme:                theme,
		RecentFolders:        current.RecentFolders,
		Logs:                 &input.Logs,
	}
	if err := writeSavedSettings(stored); err != nil {
		return AppSettings{}, err
	}
	configured := false
	if _, err := keyring.Get(credentialService, credentialName); err == nil {
		configured = true
	} else if !errors.Is(err, keyring.ErrNotFound) {
		return AppSettings{}, fmt.Errorf("read OpenRouter credential status: %w", err)
	}
	return publicSettings(stored, configured), nil
}

func (s *YouTubeService) SaveOpenRouterToken(token string) (AppSettings, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return AppSettings{}, fmt.Errorf("enter an OpenRouter API key")
	}
	if len(token) > 4096 || strings.ContainsAny(token, "\r\n") {
		return AppSettings{}, fmt.Errorf("the OpenRouter API key is invalid")
	}
	if err := keyring.Set(credentialService, credentialName, token); err != nil {
		return AppSettings{}, fmt.Errorf("store OpenRouter API key in Windows Credential Manager: %w", err)
	}
	return s.GetSettings()
}

func (s *YouTubeService) ClearOpenRouterToken() (AppSettings, error) {
	if err := keyring.Delete(credentialService, credentialName); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return AppSettings{}, fmt.Errorf("remove OpenRouter API key: %w", err)
	}
	return s.GetSettings()
}

func publicSettings(stored savedSettings, configured bool) AppSettings {
	directory := stored.DownloadDirectory
	if directory == "" {
		directory, _ = defaultDownloadDirectory()
	}
	model := stored.OpenRouterModel
	if model == "" {
		model = defaultModel
	}
	clipPrompt := stored.ClipPrompt
	if clipPrompt == "" {
		clipPrompt = defaultClipPrompt
	}
	theme := stored.Theme
	if theme == "" {
		theme = defaultTheme
	}
	return AppSettings{
		DownloadDirectory:    directory,
		OpenRouterModel:      model,
		ClipPrompt:           clipPrompt,
		OpenRouterConfigured: configured,
		NotificationsEnabled: stored.NotificationsEnabled,
		Theme:                theme,
		RecentFolders:        append([]string(nil), stored.RecentFolders...),
		Logs:                 effectiveLogOptions(stored.Logs),
		LogFilePath:          currentLogFilePath(),
	}
}

func (s *YouTubeService) rememberRecentFolder(folder string) error {
	folder, err := filepath.Abs(filepath.Clean(folder))
	if err != nil {
		return fmt.Errorf("resolve recent folder: %w", err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("recent path is not a folder")
	}

	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	stored, err := readSavedSettings()
	if err != nil {
		return err
	}
	cleaned := make([]string, 0, len(stored.RecentFolders)+1)
	for _, recent := range stored.RecentFolders {
		if !strings.EqualFold(filepath.Clean(recent), folder) {
			cleaned = append(cleaned, recent)
		}
	}
	stored.RecentFolders = append([]string{folder}, cleaned...)
	if len(stored.RecentFolders) > 8 {
		stored.RecentFolders = stored.RecentFolders[:8]
	}
	return writeSavedSettings(stored)
}

func readSavedSettings() (savedSettings, error) {
	path, err := settingsFilePath()
	if err != nil {
		return savedSettings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		logs := defaultLogOptions()
		return savedSettings{OpenRouterModel: defaultModel, ClipPrompt: defaultClipPrompt, NotificationsEnabled: true, Theme: defaultTheme, Logs: &logs}, nil
	}
	if err != nil {
		return savedSettings{}, fmt.Errorf("read settings: %w", err)
	}
	var stored savedSettings
	if err := json.Unmarshal(data, &stored); err != nil {
		return savedSettings{}, fmt.Errorf("parse settings: %w", err)
	}
	if stored.OpenRouterModel == "" {
		stored.OpenRouterModel = defaultModel
	}
	if stored.ClipPrompt == "" {
		stored.ClipPrompt = defaultClipPrompt
	}
	if stored.Theme == "" {
		stored.Theme = defaultTheme
	}
	if stored.Logs == nil {
		logs := defaultLogOptions()
		stored.Logs = &logs
	}
	return stored, nil
}

func writeSavedSettings(stored savedSettings) error {
	path, err := settingsFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings folder: %w", err)
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

func settingsFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find the user settings directory: %w", err)
	}
	return filepath.Join(configDir, "aclip", "settings.json"), nil
}

func defaultDownloadDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the user home directory: %w", err)
	}
	return filepath.Join(home, "Downloads", "aclip"), nil
}
