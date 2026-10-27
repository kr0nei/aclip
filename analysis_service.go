package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

func (s *YouTubeService) analyzeDownloadedChapter(ctx context.Context, videoTitle string, chapters []DownloadedChapter, index int) error {
	chapter := &chapters[index]
	token, err := keyring.Get(credentialService, credentialName)
	if errors.Is(err, keyring.ErrNotFound) {
		chapter.AnalysisStatus = "not_configured"
		s.emit(DownloadUpdate{Status: "analysis", Title: videoTitle, Message: "Add an OpenRouter key in Settings to get clip suggestions", OutputDir: filepath.Dir(chapter.VideoPath), Chapters: copyChapters(chapters)})
		return nil
	}
	if err != nil {
		return s.failChapterAnalysis(videoTitle, chapters, index, fmt.Errorf("read OpenRouter API key: %w", err))
	}
	settings, err := s.GetSettings()
	if err != nil {
		return s.failChapterAnalysis(videoTitle, chapters, index, err)
	}

	chapter.AnalysisStatus = "working"
	chapter.AnalysisError = ""
	s.emit(DownloadUpdate{
		Status:    "analysis",
		Title:     videoTitle,
		Message:   "Analyzing chapter for short-form clips…",
		OutputDir: filepath.Dir(chapter.VideoPath),
		Chapters:  copyChapters(chapters),
	})

	info, err := os.Stat(chapter.SubtitlePath)
	if err != nil {
		return s.failChapterAnalysis(videoTitle, chapters, index, fmt.Errorf("find chapter subtitles: %w", err))
	}
	if info.Size() > 350_000 {
		return s.failChapterAnalysis(videoTitle, chapters, index, fmt.Errorf("subtitle is too large to analyze (maximum 350 KB)"))
	}
	subtitle, err := os.ReadFile(chapter.SubtitlePath)
	if err != nil {
		return s.failChapterAnalysis(videoTitle, chapters, index, fmt.Errorf("read chapter subtitles: %w", err))
	}
	analysis, err := analyzeSubtitles(ctx, token, settings.OpenRouterModel, videoTitle, chapter.Name, chapter.DurationSeconds, string(subtitle))
	if err != nil {
		return s.failChapterAnalysis(videoTitle, chapters, index, err)
	}
	chapter.Analysis = &analysis
	chapter.AnalysisStatus = "ready"
	chapter.AnalysisError = ""
	if err := s.saveChapterAnalysis(*chapter, videoTitle, analysis); err != nil {
		chapter.AnalysisError = "Suggestions were generated but could not be saved: " + err.Error()
	}
	s.notifyIfUnfocused(
		fmt.Sprintf("analysis-ready-%d", time.Now().UnixNano()),
		"Clip ideas ready",
		"AI found clip candidates for "+chapter.Name,
	)
	s.emit(DownloadUpdate{
		Status:    "analysis",
		Title:     videoTitle,
		Message:   "Clip suggestions are ready",
		OutputDir: filepath.Dir(chapter.VideoPath),
		Chapters:  copyChapters(chapters),
	})
	return nil
}

func (s *YouTubeService) failChapterAnalysis(videoTitle string, chapters []DownloadedChapter, index int, err error) error {
	chapter := &chapters[index]
	chapter.AnalysisStatus = "failed"
	chapter.AnalysisError = err.Error()
	s.emit(DownloadUpdate{
		Status:    "analysis",
		Title:     videoTitle,
		Message:   "AI clip analysis failed for " + chapter.Name,
		OutputDir: filepath.Dir(chapter.VideoPath),
		Chapters:  copyChapters(chapters),
	})
	return err
}

func (s *YouTubeService) AnalyzeChapter(ctx context.Context, videoPath, videoTitle string) (DownloadedChapter, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return DownloadedChapter{}, fmt.Errorf("another download or analysis is already in progress")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	chapter, err := s.loadChapter(videoPath)
	if err != nil {
		return DownloadedChapter{}, err
	}
	if chapter.SubtitleStatus != "ready" {
		return chapter, fmt.Errorf("generate subtitles before requesting clip suggestions")
	}
	if videoTitle == "" {
		videoTitle = chapter.VideoTitle
	}
	chapters := []DownloadedChapter{chapter}
	if err := s.analyzeDownloadedChapter(ctx, videoTitle, chapters, 0); err != nil {
		return chapters[0], err
	}
	if chapters[0].AnalysisStatus == "not_configured" {
		return chapters[0], fmt.Errorf("add an OpenRouter API key in Settings to analyze clips")
	}
	return chapters[0], nil
}
