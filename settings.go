package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	credentialService = "aclip"
	credentialName    = "openrouter-api-key"
	defaultModel      = "openai/gpt-4.1-mini"
)

type AppSettings struct {
	DownloadDirectory    string `json:"downloadDirectory"`
	OpenRouterModel      string `json:"openRouterModel"`
	OpenRouterConfigured bool   `json:"openRouterConfigured"`
	NotificationsEnabled bool   `json:"notificationsEnabled"`
}

type SettingsInput struct {
	DownloadDirectory    string `json:"downloadDirectory"`
	OpenRouterModel      string `json:"openRouterModel"`
	NotificationsEnabled bool   `json:"notificationsEnabled"`
}

type savedSettings struct {
	DownloadDirectory    string `json:"downloadDirectory"`
	OpenRouterModel      string `json:"openRouterModel"`
	NotificationsEnabled bool   `json:"notificationsEnabled"`
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
	stored := savedSettings{
		DownloadDirectory:    filepath.Clean(directory),
		OpenRouterModel:      model,
		NotificationsEnabled: input.NotificationsEnabled,
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
	return AppSettings{
		DownloadDirectory:    directory,
		OpenRouterModel:      model,
		OpenRouterConfigured: configured,
		NotificationsEnabled: stored.NotificationsEnabled,
	}
}

func readSavedSettings() (savedSettings, error) {
	path, err := settingsFilePath()
	if err != nil {
		return savedSettings{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return savedSettings{OpenRouterModel: defaultModel, NotificationsEnabled: true}, nil
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
