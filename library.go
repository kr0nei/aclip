package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	videoManifestFilename   = "video-info.json"
	clipSuggestionsFilename = "clip-suggestions.json"
)

type VideoFolderContents struct {
	FolderPath string              `json:"folderPath"`
	Title      string              `json:"title"`
	Chapters   []DownloadedChapter `json:"chapters"`
}

type videoManifest struct {
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	DownloadedAt time.Time `json:"downloadedAt"`
}

type folderSuggestions struct {
	Version    int                    `json:"version"`
	VideoTitle string                 `json:"videoTitle"`
	UpdatedAt  time.Time              `json:"updatedAt"`
	Chapters   []savedChapterAnalysis `json:"chapters"`
}

type savedChapterAnalysis struct {
	VideoFile string           `json:"videoFile"`
	Analysis  *ChapterAnalysis `json:"analysis,omitempty"`
	Done      bool             `json:"done,omitempty"`
}

func writeVideoManifest(directory, title, videoURL string) error {
	data, err := json.MarshalIndent(videoManifest{Title: title, URL: videoURL, DownloadedAt: time.Now()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode video information: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, videoManifestFilename), data, 0o600); err != nil {
		return fmt.Errorf("save video information: %w", err)
	}
	return nil
}

func readVideoManifest(directory string) (videoManifest, error) {
	data, err := os.ReadFile(filepath.Join(directory, videoManifestFilename))
	if err != nil {
		return videoManifest{}, err
	}
	var manifest videoManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return videoManifest{}, fmt.Errorf("parse video information: %w", err)
	}
	return manifest, nil
}

func (s *YouTubeService) OpenVideoFolder(ctx context.Context) (VideoFolderContents, error) {
	if s.app == nil {
		return VideoFolderContents{}, fmt.Errorf("folder picker is unavailable")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return VideoFolderContents{}, fmt.Errorf("another download or chapter action is already in progress")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()
	settings, err := s.GetSettings()
	if err != nil {
		return VideoFolderContents{}, err
	}
	dialog := s.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		SetTitle("Open video folder")
	if info, statErr := os.Stat(settings.DownloadDirectory); statErr == nil && info.IsDir() {
		dialog.SetDirectory(settings.DownloadDirectory)
	}
	folder, err := dialog.PromptForSingleSelection()
	if err != nil {
		return VideoFolderContents{}, fmt.Errorf("choose video folder: %w", err)
	}
	if folder == "" {
		return VideoFolderContents{}, nil
	}
	s.authorizeFolder(folder)
	contents, err := s.loadVideoFolder(ctx, folder)
	if err != nil {
		return VideoFolderContents{}, err
	}
	if err := s.rememberRecentFolder(folder); err != nil && s.app != nil {
		s.app.Logger.Warn("could not update recent folders", "error", err)
	}
	return contents, nil
}

func (s *YouTubeService) OpenRecentFolder(ctx context.Context, folder string) (VideoFolderContents, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return VideoFolderContents{}, fmt.Errorf("another download or chapter action is already in progress")
	}
	s.busy = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
	}()

	folder, err := filepath.Abs(filepath.Clean(folder))
	if err != nil {
		return VideoFolderContents{}, fmt.Errorf("resolve recent folder: %w", err)
	}
	settings, err := s.GetSettings()
	if err != nil {
		return VideoFolderContents{}, err
	}
	known := false
	for _, recent := range settings.RecentFolders {
		if strings.EqualFold(filepath.Clean(recent), folder) {
			known = true
			break
		}
	}
	if !known {
		return VideoFolderContents{}, fmt.Errorf("folder is no longer in recent folders")
	}
	s.authorizeFolder(folder)
	contents, err := s.loadVideoFolder(ctx, folder)
	if err != nil {
		return VideoFolderContents{}, err
	}
	if err := s.rememberRecentFolder(folder); err != nil && s.app != nil {
		s.app.Logger.Warn("could not update recent folders", "error", err)
	}
	return contents, nil
}

func (s *YouTubeService) loadVideoFolder(ctx context.Context, folder string) (VideoFolderContents, error) {
	folder, err := filepath.Abs(filepath.Clean(folder))
	if err != nil {
		return VideoFolderContents{}, fmt.Errorf("resolve video folder: %w", err)
	}
	info, err := os.Stat(folder)
	if err != nil {
		return VideoFolderContents{}, fmt.Errorf("read video folder: %w", err)
	}
	if !info.IsDir() {
		return VideoFolderContents{}, fmt.Errorf("select a folder containing downloaded videos")
	}

	byDirectory := make(map[string][]DownloadedChapter)
	err = filepath.WalkDir(folder, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			return nil
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		byDirectory[filepath.Dir(path)] = append(byDirectory[filepath.Dir(path)], DownloadedChapter{
			Name:           name,
			VideoPath:      path,
			SubtitlePath:   strings.TrimSuffix(path, filepath.Ext(path)) + ".srt",
			SubtitleStatus: "missing",
			AnalysisStatus: "missing",
		})
		return nil
	})
	if err != nil {
		return VideoFolderContents{}, fmt.Errorf("scan video folder: %w", err)
	}
	if len(byDirectory) == 0 {
		return VideoFolderContents{}, fmt.Errorf("no MP4 videos were found in this folder")
	}

	directories := make([]string, 0, len(byDirectory))
	for directory := range byDirectory {
		directories = append(directories, directory)
	}
	sort.Strings(directories)
	contents := VideoFolderContents{FolderPath: folder}
	videoTitles := make(map[string]struct{})
	for _, directory := range directories {
		chapters := preferSplitChapterFiles(byDirectory[directory])
		manifest, manifestErr := readVideoManifest(directory)
		videoTitle := manifest.Title
		if manifestErr != nil || videoTitle == "" {
			videoTitle = filepath.Base(directory)
		}
		videoTitles[videoTitle] = struct{}{}
		suggestions, suggestionsErr := s.readFolderSuggestions(directory)
		if suggestionsErr != nil {
			return VideoFolderContents{}, suggestionsErr
		}
		saved := make(map[string]savedChapterAnalysis, len(suggestions.Chapters))
		for _, item := range suggestions.Chapters {
			saved[filepath.Base(item.VideoFile)] = item
		}
		for i := range chapters {
			chapters[i].VideoTitle = videoTitle
			if metadata, metadataErr := inspectVideo(ctx, chapters[i].VideoPath); metadataErr == nil {
				applyVideoMetadata(&chapters[i], metadata)
			}
			if _, err := os.Stat(chapters[i].SubtitlePath); err == nil {
				chapters[i].SubtitleStatus = "ready"
			}
			if savedChapter, ok := saved[filepath.Base(chapters[i].VideoPath)]; ok {
				chapters[i].Done = savedChapter.Done
				if savedChapter.Analysis != nil {
					chapters[i].Analysis = savedChapter.Analysis
					chapters[i].AnalysisStatus = "ready"
				}
			}
		}
		contents.Chapters = append(contents.Chapters, chapters...)
	}
	sort.Slice(contents.Chapters, func(i, j int) bool {
		return contents.Chapters[i].VideoPath < contents.Chapters[j].VideoPath
	})
	if len(videoTitles) == 1 {
		for videoTitle := range videoTitles {
			contents.Title = videoTitle
		}
	} else {
		contents.Title = filepath.Base(folder)
	}
	return contents, nil
}

func preferSplitChapterFiles(files []DownloadedChapter) []DownloadedChapter {
	var split []DownloadedChapter
	for _, file := range files {
		if chapterNamePattern.MatchString(file.Name) {
			split = append(split, file)
		}
	}
	if len(split) > 0 {
		return split
	}
	return files
}

func (s *YouTubeService) readFolderSuggestions(directory string) (folderSuggestions, error) {
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	return readFolderSuggestions(directory)
}

func readFolderSuggestions(directory string) (folderSuggestions, error) {
	path := filepath.Join(directory, clipSuggestionsFilename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return folderSuggestions{Version: 1}, nil
	}
	if err != nil {
		return folderSuggestions{}, fmt.Errorf("read clip suggestions: %w", err)
	}
	var saved folderSuggestions
	if err := json.Unmarshal(data, &saved); err != nil {
		return folderSuggestions{}, fmt.Errorf("parse clip suggestions: %w", err)
	}
	return saved, nil
}

func (s *YouTubeService) saveChapterAnalysis(chapter DownloadedChapter, videoTitle string, analysis ChapterAnalysis) error {
	if len(analysis.Clips) == 0 {
		return fmt.Errorf("there are no clip suggestions to save")
	}
	if err := normalizeClipAnalysis(&analysis, chapter.DurationSeconds); err != nil {
		return err
	}
	directory := filepath.Dir(chapter.VideoPath)
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	saved, err := readFolderSuggestions(directory)
	if err != nil {
		return err
	}
	saved.Version = 1
	saved.VideoTitle = videoTitle
	saved.UpdatedAt = time.Now()
	fileName := filepath.Base(chapter.VideoPath)
	updated := false
	for i := range saved.Chapters {
		if filepath.Base(saved.Chapters[i].VideoFile) == fileName {
			saved.Chapters[i].Analysis = &analysis
			updated = true
			break
		}
	}
	if !updated {
		saved.Chapters = append(saved.Chapters, savedChapterAnalysis{VideoFile: fileName, Analysis: &analysis})
	}
	sort.Slice(saved.Chapters, func(i, j int) bool { return saved.Chapters[i].VideoFile < saved.Chapters[j].VideoFile })
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode clip suggestions: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, clipSuggestionsFilename), data, 0o600); err != nil {
		return fmt.Errorf("save combined clip suggestions: %w", err)
	}
	return nil
}

func (s *YouTubeService) removeChapterAnalysis(videoPath string) error {
	directory := filepath.Dir(videoPath)
	fileName := filepath.Base(videoPath)
	s.suggestionsMu.Lock()
	defer s.suggestionsMu.Unlock()
	saved, err := readFolderSuggestions(directory)
	if err != nil {
		return err
	}
	filtered := saved.Chapters[:0]
	for _, chapter := range saved.Chapters {
		if filepath.Base(chapter.VideoFile) != fileName {
			filtered = append(filtered, chapter)
		}
	}
	if len(filtered) == len(saved.Chapters) {
		return nil
	}
	saved.Chapters = filtered
	saved.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode clip suggestions: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, clipSuggestionsFilename), data, 0o600); err != nil {
		return fmt.Errorf("remove stale chapter suggestions: %w", err)
	}
	return nil
}

func (s *YouTubeService) SaveChapterAnalysis(videoPath, videoTitle string, analysis ChapterAnalysis) (DownloadedChapter, error) {
	chapter, err := s.loadChapter(videoPath)
	if err != nil {
		return DownloadedChapter{}, err
	}
	if videoTitle == "" {
		videoTitle = chapter.VideoTitle
	}
	if chapter.SubtitleStatus != "ready" {
		return DownloadedChapter{}, fmt.Errorf("generate subtitles before saving clip suggestions")
	}
	if err := s.saveChapterAnalysis(chapter, videoTitle, analysis); err != nil {
		return DownloadedChapter{}, err
	}
	chapter.Analysis = &analysis
	chapter.AnalysisStatus = "ready"
	chapter.VideoTitle = videoTitle
	return chapter, nil
}

func (s *YouTubeService) SetChapterDone(videoPath string, done bool) (DownloadedChapter, error) {
	chapter, err := s.loadChapter(videoPath)
	if err != nil {
		return DownloadedChapter{}, err
	}
	chapter.Done = done
	directory := filepath.Dir(chapter.VideoPath)
	s.suggestionsMu.Lock()
	saved, err := readFolderSuggestions(directory)
	if err != nil {
		s.suggestionsMu.Unlock()
		return DownloadedChapter{}, err
	}
	fileName := filepath.Base(chapter.VideoPath)
	found := false
	for i := range saved.Chapters {
		if filepath.Base(saved.Chapters[i].VideoFile) == fileName {
			saved.Chapters[i].Done = done
			found = true
			break
		}
	}
	if !found && done {
		saved.Chapters = append(saved.Chapters, savedChapterAnalysis{VideoFile: fileName, Done: true})
	}
	if found || done {
		saved.Version = 1
		saved.VideoTitle = chapter.VideoTitle
		saved.UpdatedAt = time.Now()
		sort.Slice(saved.Chapters, func(i, j int) bool { return saved.Chapters[i].VideoFile < saved.Chapters[j].VideoFile })
		data, marshalErr := json.MarshalIndent(saved, "", "  ")
		if marshalErr != nil {
			s.suggestionsMu.Unlock()
			return DownloadedChapter{}, fmt.Errorf("encode chapter state: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(directory, clipSuggestionsFilename), data, 0o600); writeErr != nil {
			s.suggestionsMu.Unlock()
			return DownloadedChapter{}, fmt.Errorf("save chapter state: %w", writeErr)
		}
	}
	s.suggestionsMu.Unlock()
	s.emit(DownloadUpdate{
		Status:    "chapter_updated",
		Title:     chapter.VideoTitle,
		Message:   chapter.Name,
		OutputDir: directory,
		Chapters:  []DownloadedChapter{chapter},
	})
	return chapter, nil
}

func (s *YouTubeService) GenerateSubtitles(ctx context.Context, videoPath string) (DownloadedChapter, error) {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return DownloadedChapter{}, fmt.Errorf("another download or chapter action is already in progress")
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
	if chapter.SubtitleStatus == "ready" {
		return chapter, fmt.Errorf("subtitles already exist for this chapter")
	}
	autosubs, err := exec.LookPath("autosubs.exe")
	if err != nil {
		return chapter, fmt.Errorf("autosubs.exe was not found in PATH")
	}
	chapter.SubtitleStatus = "working"
	chapter.SubtitleError = ""
	s.emit(DownloadUpdate{Status: "subtitles", Title: chapter.VideoTitle, Message: "Generating subtitles for " + chapter.Name, OutputDir: filepath.Dir(chapter.VideoPath), Chapters: []DownloadedChapter{chapter}})
	cmd := exec.CommandContext(ctx, autosubs, chapter.VideoPath, "--model", "parakeet", "-o", chapter.SubtitlePath)
	cmd.Dir = filepath.Dir(chapter.VideoPath)
	output, runErr := cmd.CombinedOutput()
	if runErr == nil {
		if _, statErr := os.Stat(chapter.SubtitlePath); statErr != nil {
			runErr = statErr
			output = []byte("AutoSubs exited successfully but the SRT file was not found: " + statErr.Error())
		}
	}
	if runErr != nil {
		chapter.SubtitleStatus = "failed"
		chapter.SubtitleError = summarizeProcessOutput(output)
		if ctx.Err() != nil {
			runErr = ctx.Err()
		}
	} else {
		chapter.SubtitleStatus = "ready"
		chapter.AnalysisStatus = "missing"
		chapter.Analysis = nil
		chapter.AnalysisError = ""
		if err := s.removeChapterAnalysis(chapter.VideoPath); err != nil {
			chapter.AnalysisError = "Subtitles generated but stale clip suggestions could not be removed: " + err.Error()
		}
		s.notifyIfUnfocused("subtitles-ready-"+fmt.Sprint(time.Now().UnixNano()), "Subtitles generated", chapter.Name+" is ready for clip analysis")
	}
	s.emit(DownloadUpdate{Status: "subtitles", Title: chapter.VideoTitle, Message: subtitleMessage(chapter), OutputDir: filepath.Dir(chapter.VideoPath), Chapters: []DownloadedChapter{chapter}})
	return chapter, runErr
}

func subtitleMessage(chapter DownloadedChapter) string {
	if chapter.SubtitleStatus == "ready" {
		return "Subtitles generated"
	}
	return chapter.SubtitleError
}

func (s *YouTubeService) OpenChapterFolder(videoPath string) error {
	if _, err := s.loadChapter(videoPath); err != nil {
		return err
	}
	return exec.Command("explorer.exe", filepath.Dir(videoPath)).Start()
}

func (s *YouTubeService) OpenChapterSubtitle(videoPath string) error {
	chapter, err := s.loadChapter(videoPath)
	if err != nil {
		return err
	}
	if chapter.SubtitleStatus != "ready" {
		return fmt.Errorf("no subtitle file exists for this chapter")
	}
	return exec.Command("explorer.exe", chapter.SubtitlePath).Start()
}

func (s *YouTubeService) RemoveChapter(videoPath string) error {
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("another download or chapter action is already in progress")
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
		return err
	}
	if err := os.Remove(chapter.VideoPath); err != nil {
		return fmt.Errorf("remove chapter video: %w", err)
	}
	var cleanupErr error
	if err := os.Remove(chapter.SubtitlePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErr = fmt.Errorf("remove chapter SRT: %w", err)
	}
	if err := s.removeChapterAnalysis(chapter.VideoPath); err != nil {
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("%v; remove saved suggestions: %w", cleanupErr, err)
		} else {
			cleanupErr = fmt.Errorf("remove saved suggestions: %w", err)
		}
	}
	message := "Chapter, matching SRT, and saved suggestions removed"
	if cleanupErr != nil {
		message += "; cleanup warning: " + cleanupErr.Error()
	}
	s.emit(DownloadUpdate{
		Status:    "chapter_removed",
		Title:     chapter.VideoTitle,
		Message:   message,
		OutputDir: filepath.Dir(chapter.VideoPath),
		Chapters:  []DownloadedChapter{chapter},
	})
	return nil
}

func (s *YouTubeService) loadChapter(videoPath string) (DownloadedChapter, error) {
	videoPath, err := filepath.Abs(filepath.Clean(videoPath))
	if err != nil {
		return DownloadedChapter{}, fmt.Errorf("resolve chapter video path: %w", err)
	}
	if !s.isAuthorizedVideo(videoPath) {
		return DownloadedChapter{}, fmt.Errorf("open the containing video folder in aclip before using chapter actions")
	}
	contents, err := s.loadVideoFolder(context.Background(), filepath.Dir(videoPath))
	if err != nil {
		return DownloadedChapter{}, err
	}
	for _, chapter := range contents.Chapters {
		if filepath.Clean(chapter.VideoPath) == filepath.Clean(videoPath) {
			return chapter, nil
		}
	}
	return DownloadedChapter{}, fmt.Errorf("video chapter was not found in its folder")
}

func (s *YouTubeService) authorizeFolder(folder string) {
	folder, err := filepath.Abs(filepath.Clean(folder))
	if err != nil {
		return
	}
	s.foldersMu.Lock()
	defer s.foldersMu.Unlock()
	if s.authorizedFolders == nil {
		s.authorizedFolders = make(map[string]struct{})
	}
	s.authorizedFolders[folder] = struct{}{}
}

func (s *YouTubeService) isAuthorizedVideo(videoPath string) bool {
	s.foldersMu.RLock()
	defer s.foldersMu.RUnlock()
	for folder := range s.authorizedFolders {
		if pathWithinDirectory(folder, videoPath) {
			return true
		}
	}
	return false
}

func pathWithinDirectory(directory, path string) bool {
	relative, err := filepath.Rel(directory, path)
	if err != nil || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}
