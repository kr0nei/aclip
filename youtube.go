package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

const downloadEvent = "download:update"

var (
	youtubeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
	chapterNamePattern = regexp.MustCompile(`^\d+ - `)
	progressPattern    = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
)

type DownloadedChapter struct {
	Name            string           `json:"name"`
	VideoTitle      string           `json:"videoTitle,omitempty"`
	VideoPath       string           `json:"videoPath"`
	SubtitlePath    string           `json:"subtitlePath"`
	DurationSeconds float64          `json:"durationSeconds,omitempty"`
	SizeBytes       int64            `json:"sizeBytes"`
	Width           int              `json:"width,omitempty"`
	Height          int              `json:"height,omitempty"`
	VideoCodec      string           `json:"videoCodec,omitempty"`
	AudioCodec      string           `json:"audioCodec,omitempty"`
	SubtitleStatus  string           `json:"subtitleStatus"`
	SubtitleError   string           `json:"subtitleError,omitempty"`
	AnalysisStatus  string           `json:"analysisStatus"`
	AnalysisError   string           `json:"analysisError,omitempty"`
	Analysis        *ChapterAnalysis `json:"analysis,omitempty"`
	Done            bool             `json:"done"`
}

type DownloadUpdate struct {
	Status    string              `json:"status"`
	Title     string              `json:"title,omitempty"`
	Message   string              `json:"message,omitempty"`
	Progress  float64             `json:"progress,omitempty"`
	OutputDir string              `json:"outputDir,omitempty"`
	Chapters  []DownloadedChapter `json:"chapters,omitempty"`
}

type DownloadResult struct {
	Title     string              `json:"title"`
	OutputDir string              `json:"outputDir"`
	Chapters  []DownloadedChapter `json:"chapters"`
}

type YouTubeService struct {
	app               *application.App
	window            *application.WebviewWindow
	notifier          *notifications.NotificationService
	mu                sync.Mutex
	settingsMu        sync.Mutex
	suggestionsMu     sync.Mutex
	foldersMu         sync.RWMutex
	logMu             sync.Mutex
	authorizedFolders map[string]struct{}
	busy              bool
}

func init() {
	application.RegisterEvent[DownloadUpdate](downloadEvent)
}

func (s *YouTubeService) DownloadYouTube(ctx context.Context, rawURL string) (DownloadResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	videoID, err := youtubeVideoID(rawURL)
	if err != nil {
		return DownloadResult{}, err
	}

	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return DownloadResult{}, fmt.Errorf("a download is already in progress")
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
		return DownloadResult{}, err
	}

	ytDLP, err := exec.LookPath("yt-dlp.exe")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("yt-dlp.exe was not found in PATH")
	}
	root := settings.DownloadDirectory
	if err := os.MkdirAll(root, 0o755); err != nil {
		return DownloadResult{}, fmt.Errorf("create download folder: %w", err)
	}
	outputDir, err := os.MkdirTemp(root, videoID+"-")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create video folder: %w", err)
	}
	s.authorizeFolder(outputDir)
	s.logEvent("downloads", "YouTube download started", map[string]string{
		"video_id":      videoID,
		"output_folder": outputDir,
	})

	s.emit(DownloadUpdate{Status: "starting", Message: "Finding video details…", OutputDir: outputDir})
	var title string
	var titleMu sync.Mutex
	var notifiedChapters sync.Map
	onChapterReady := func(chapter DownloadedChapter) {
		if _, alreadyNotified := notifiedChapters.LoadOrStore(chapter.VideoPath, struct{}{}); alreadyNotified {
			return
		}
		titleMu.Lock()
		currentTitle := title
		titleMu.Unlock()
		if currentTitle == "" {
			currentTitle = chapter.Name
		}
		chapter.VideoTitle = currentTitle
		chapter.SubtitleStatus = "missing"
		chapter.AnalysisStatus = "missing"
		s.notifyIfUnfocused(
			fmt.Sprintf("chapter-downloaded-%d", time.Now().UnixNano()),
			"Chapter downloaded",
			fmt.Sprintf("%s · %s · %s", chapter.Name, formatDuration(chapter.DurationSeconds), formatFileSize(chapter.SizeBytes)),
		)
		s.logEvent("downloads", "Chapter downloaded", map[string]string{
			"video_title": currentTitle,
			"chapter":     chapter.Name,
			"video_file":  chapter.VideoPath,
			"duration":    formatDuration(chapter.DurationSeconds),
			"size":        formatFileSize(chapter.SizeBytes),
		})
		s.emit(DownloadUpdate{
			Status:    "chapter_downloaded",
			Title:     currentTitle,
			Message:   "Chapter downloaded",
			OutputDir: outputDir,
			Chapters:  []DownloadedChapter{chapter},
		})
	}
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		s.watchSplitChapters(ctx, watchStop, outputDir, onChapterReady)
	}()
	err = runYtDLP(ctx, ytDLP, rawURL, outputDir, func(found string) {
		titleMu.Lock()
		title = found
		titleMu.Unlock()
		s.logEvent("downloads", "Video title identified", map[string]string{"title": found, "video_id": videoID})
		s.emit(DownloadUpdate{
			Status:    "downloading",
			Title:     found,
			Message:   "Downloading video",
			OutputDir: outputDir,
		})
	}, func(progress float64) {
		titleMu.Lock()
		currentTitle := title
		titleMu.Unlock()
		s.emit(DownloadUpdate{
			Status:    "downloading",
			Title:     currentTitle,
			Message:   "Downloading video",
			Progress:  progress,
			OutputDir: outputDir,
		})
	})
	close(watchStop)
	<-watchDone
	if err != nil {
		s.logEvent("errors", "YouTube download failed", map[string]string{"video_id": videoID, "error": err.Error()})
		s.emit(DownloadUpdate{Status: "error", Title: title, Message: err.Error(), OutputDir: outputDir})
		return DownloadResult{}, err
	}

	chapters, err := listMP4Chapters(outputDir)
	if err != nil {
		s.emit(DownloadUpdate{Status: "error", Title: title, Message: err.Error(), OutputDir: outputDir})
		return DownloadResult{}, err
	}
	if len(chapters) == 0 {
		err = fmt.Errorf("yt-dlp finished but no MP4 files were found")
		s.emit(DownloadUpdate{Status: "error", Title: title, Message: err.Error(), OutputDir: outputDir})
		return DownloadResult{}, err
	}
	if title == "" {
		title = chapters[0].Name
	}
	if err := writeVideoManifest(outputDir, title, rawURL); err != nil {
		s.emit(DownloadUpdate{Status: "error", Title: title, Message: err.Error(), OutputDir: outputDir})
		return DownloadResult{}, err
	}
	if err := s.rememberRecentFolder(outputDir); err != nil && s.app != nil {
		s.app.Logger.Warn("could not update recent folders", "error", err)
	}
	for i := range chapters {
		chapters[i].VideoTitle = title
		chapters[i].SubtitleStatus = "missing"
		chapters[i].AnalysisStatus = "missing"
		if metadata, metadataErr := inspectVideo(ctx, chapters[i].VideoPath); metadataErr == nil {
			chapters[i].DurationSeconds = metadata.DurationSeconds
			chapters[i].SizeBytes = metadata.SizeBytes
			chapters[i].Width = metadata.Width
			chapters[i].Height = metadata.Height
			chapters[i].VideoCodec = metadata.VideoCodec
			chapters[i].AudioCodec = metadata.AudioCodec
		}
		onChapterReady(chapters[i])
	}

	s.emit(DownloadUpdate{
		Status:    "chapters",
		Title:     title,
		Message:   "Chapters downloaded. Use a chapter menu to generate subtitles or clip suggestions.",
		OutputDir: outputDir,
		Chapters:  copyChapters(chapters),
	})
	s.logEvent("downloads", "Chapter extraction complete", map[string]string{
		"video_title":   title,
		"chapter_count": strconv.Itoa(len(chapters)),
		"output_folder": outputDir,
	})
	message := "Download complete. Generate subtitles and clip suggestions from each chapter's actions."
	result := DownloadResult{Title: title, OutputDir: outputDir, Chapters: chapters}
	s.emit(DownloadUpdate{
		Status:    "complete",
		Title:     title,
		Message:   message,
		OutputDir: outputDir,
		Chapters:  copyChapters(chapters),
	})
	return result, nil
}

func (s *YouTubeService) emit(update DownloadUpdate) {
	if s.app != nil {
		s.app.Event.Emit(downloadEvent, update)
	}
}

func (s *YouTubeService) notifyIfUnfocused(id, title, body string) {
	if s.notifier == nil || s.window == nil || s.window.IsFocused() {
		return
	}
	enabled := false
	s.settingsMu.Lock()
	stored, err := readSavedSettings()
	s.settingsMu.Unlock()
	if err == nil {
		enabled = stored.NotificationsEnabled
	} else {
		enabled = true
	}
	if !enabled {
		return
	}
	if err := s.notifier.SendNotification(notifications.NotificationOptions{
		ID:                id,
		Title:             title,
		Body:              body,
		InterruptionLevel: notifications.InterruptionLevelActive,
	}); err != nil && s.app != nil {
		s.app.Logger.Error("background notification failed", "error", err)
	}
}

func (s *YouTubeService) watchSplitChapters(ctx context.Context, stop <-chan struct{}, directory string, onReady func(DownloadedChapter)) {
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()
	type stability struct {
		size   int64
		checks int
		done   bool
	}
	states := make(map[string]stability)
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			chapters, err := listSplitMP4Chapters(directory)
			if err != nil {
				continue
			}
			for _, chapter := range chapters {
				info, err := os.Stat(chapter.VideoPath)
				if err != nil {
					continue
				}
				state := states[chapter.VideoPath]
				if info.Size() == state.size {
					state.checks++
				} else {
					state.size = info.Size()
					state.checks = 0
				}
				if state.done || info.Size() == 0 || state.checks < 2 {
					states[chapter.VideoPath] = state
					continue
				}
				if metadata, metadataErr := inspectVideo(ctx, chapter.VideoPath); metadataErr == nil {
					chapter.DurationSeconds = metadata.DurationSeconds
					chapter.SizeBytes = metadata.SizeBytes
					chapter.Width = metadata.Width
					chapter.Height = metadata.Height
					chapter.VideoCodec = metadata.VideoCodec
					chapter.AudioCodec = metadata.AudioCodec
				}
				state.done = true
				states[chapter.VideoPath] = state
				onReady(chapter)
			}
		}
	}
}

func youtubeVideoID(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("enter a valid YouTube video URL")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("YouTube URLs must use HTTP or HTTPS")
	}

	host := strings.ToLower(parsed.Hostname())
	validHost := host == "youtu.be" || host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "youtube-nocookie.com" || strings.HasSuffix(host, ".youtube-nocookie.com")
	if !validHost {
		return "", fmt.Errorf("enter a URL from YouTube or youtu.be")
	}

	id := parsed.Query().Get("v")
	if host == "youtu.be" && id == "" {
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segments) > 0 {
			id = segments[0]
		}
	}
	if id == "" {
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		for i, segment := range segments {
			if (segment == "shorts" || segment == "live" || segment == "embed" || segment == "v") && i+1 < len(segments) {
				id = segments[i+1]
				break
			}
		}
	}
	if !youtubeIDPattern.MatchString(id) {
		return "", fmt.Errorf("the YouTube URL does not contain a valid video ID")
	}
	return id, nil
}

func runYtDLP(ctx context.Context, executable, videoURL, outputDir string, onTitle func(string), onProgress func(float64)) error {
	const titleMarker = "__ACLIP_TITLE__"
	args := []string{
		"-f", `bv*[vcodec^=avc1][height<=1080]+ba[acodec^=mp4a]/b[ext=mp4][height<=1080]`,
		"--merge-output-format", "mp4",
		"--split-chapters",
		"-o", `chapter:%(section_number)02d - %(section_title)s.%(ext)s`,
		"-o", `%(title)s.%(ext)s`,
		"--windows-filenames",
		"--no-playlist",
		"--progress",
		"--newline",
		"--print", "before_dl:" + titleMarker + "%(title)s",
		videoURL,
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = outputDir
	hideConsole(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open yt-dlp output: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open yt-dlp error output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start yt-dlp: %w", err)
	}

	var logs []string
	readOutput := func(stream io.Reader, captureErrors bool) error {
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			if marker := strings.Index(line, titleMarker); marker >= 0 {
				found := strings.TrimSpace(line[marker+len(titleMarker):])
				if found != "" {
					onTitle(found)
				}
				continue
			}
			if captureErrors {
				if len(logs) == 20 {
					logs = logs[1:]
				}
				logs = append(logs, line)
				if match := progressPattern.FindStringSubmatch(line); len(match) == 2 {
					if progress, parseErr := strconv.ParseFloat(match[1], 64); parseErr == nil && progress >= 0 && progress <= 100 {
						onProgress(progress)
					}
				}
			}
		}
		return scanner.Err()
	}

	readErrors := make(chan error, 2)
	go func() { readErrors <- readOutput(stdout, false) }()
	go func() { readErrors <- readOutput(stderr, true) }()
	stdoutErr := <-readErrors
	stderrErr := <-readErrors
	cmdErr := cmd.Wait()
	if stdoutErr != nil || stderrErr != nil {
		return fmt.Errorf("read yt-dlp output: %v %v", stdoutErr, stderrErr)
	}
	if cmdErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(logs) > 0 {
			return fmt.Errorf("yt-dlp failed: %s", strings.Join(logs, "\n"))
		}
		return fmt.Errorf("yt-dlp failed: %w", cmdErr)
	}
	return nil
}

func listMP4Chapters(directory string) ([]DownloadedChapter, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("list downloaded files: %w", err)
	}
	var files []DownloadedChapter
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			continue
		}
		videoPath := filepath.Join(directory, entry.Name())
		files = append(files, DownloadedChapter{
			Name:           strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())),
			VideoPath:      videoPath,
			SubtitlePath:   strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".srt",
			SubtitleStatus: "pending",
			AnalysisStatus: "waiting",
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	var chapterFiles []DownloadedChapter
	for _, file := range files {
		if chapterNamePattern.MatchString(file.Name) {
			chapterFiles = append(chapterFiles, file)
		}
	}
	if len(chapterFiles) > 0 {
		return chapterFiles, nil
	}
	return files, nil
}

func listSplitMP4Chapters(directory string) ([]DownloadedChapter, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var chapters []DownloadedChapter
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".mp4") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if !chapterNamePattern.MatchString(name) {
			continue
		}
		videoPath := filepath.Join(directory, entry.Name())
		chapters = append(chapters, DownloadedChapter{
			Name:           name,
			VideoPath:      videoPath,
			SubtitlePath:   strings.TrimSuffix(videoPath, filepath.Ext(videoPath)) + ".srt",
			SubtitleStatus: "pending",
			AnalysisStatus: "waiting",
		})
	}
	sort.Slice(chapters, func(i, j int) bool { return chapters[i].Name < chapters[j].Name })
	return chapters, nil
}

func copyChapters(chapters []DownloadedChapter) []DownloadedChapter {
	return append([]DownloadedChapter(nil), chapters...)
}

func summarizeProcessOutput(output []byte) string {
	message := strings.TrimSpace(string(output))
	if len(message) > 400 {
		message = "…" + message[len(message)-399:]
	}
	if message == "" {
		return "Subtitle generator exited with an error"
	}
	return message
}
