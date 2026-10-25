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

	"github.com/wailsapp/wails/v3/pkg/application"
)

const downloadEvent = "download:update"

var (
	youtubeIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)
	chapterNamePattern = regexp.MustCompile(`^\d+ - `)
	progressPattern    = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
)

type DownloadedChapter struct {
	Name           string `json:"name"`
	VideoPath      string `json:"videoPath"`
	SubtitlePath   string `json:"subtitlePath"`
	SubtitleStatus string `json:"subtitleStatus"`
	SubtitleError  string `json:"subtitleError,omitempty"`
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
	app  *application.App
	mu   sync.Mutex
	busy bool
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

	ytDLP, err := exec.LookPath("yt-dlp.exe")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("yt-dlp.exe was not found in PATH")
	}
	autosubs, err := exec.LookPath("autosubs.exe")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("autosubs.exe was not found in PATH")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return DownloadResult{}, fmt.Errorf("find the user home directory: %w", err)
	}
	root := filepath.Join(home, "Downloads", "aclip")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return DownloadResult{}, fmt.Errorf("create download folder: %w", err)
	}
	outputDir, err := os.MkdirTemp(root, videoID+"-")
	if err != nil {
		return DownloadResult{}, fmt.Errorf("create video folder: %w", err)
	}

	s.emit(DownloadUpdate{Status: "starting", Message: "Finding video details…", OutputDir: outputDir})
	var title string
	var titleMu sync.Mutex
	err = runYtDLP(ctx, ytDLP, rawURL, outputDir, func(found string) {
		titleMu.Lock()
		title = found
		titleMu.Unlock()
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
	if err != nil {
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

	s.emit(DownloadUpdate{
		Status:    "subtitles",
		Title:     title,
		Message:   "Video downloaded. Preparing chapter subtitles…",
		OutputDir: outputDir,
		Chapters:  copyChapters(chapters),
	})
	failedSubtitles := 0
	for i := range chapters {
		if err := ctx.Err(); err != nil {
			s.emit(DownloadUpdate{Status: "error", Title: title, Message: "Download canceled", OutputDir: outputDir, Chapters: copyChapters(chapters)})
			return DownloadResult{}, err
		}

		chapters[i].SubtitleStatus = "working"
		s.emit(DownloadUpdate{
			Status:    "subtitles",
			Title:     title,
			Message:   fmt.Sprintf("Generating subtitles for chapter %d of %d…", i+1, len(chapters)),
			OutputDir: outputDir,
			Chapters:  copyChapters(chapters),
		})

		cmd := exec.CommandContext(ctx, autosubs, chapters[i].VideoPath, "--model", "parakeet", "-o", chapters[i].SubtitlePath)
		cmd.Dir = outputDir
		output, subtitleErr := cmd.CombinedOutput()
		if subtitleErr != nil {
			if ctx.Err() != nil {
				s.emit(DownloadUpdate{Status: "error", Title: title, Message: "Subtitle generation canceled", OutputDir: outputDir, Chapters: copyChapters(chapters)})
				return DownloadResult{}, ctx.Err()
			}
			chapters[i].SubtitleStatus = "failed"
			chapters[i].SubtitleError = summarizeProcessOutput(output)
			failedSubtitles++
		} else {
			chapters[i].SubtitleStatus = "ready"
		}
		s.emit(DownloadUpdate{
			Status:    "subtitles",
			Title:     title,
			Message:   fmt.Sprintf("Generated subtitles for chapter %d of %d", i+1, len(chapters)),
			OutputDir: outputDir,
			Chapters:  copyChapters(chapters),
		})
	}

	message := "Download and subtitles complete"
	if failedSubtitles > 0 {
		message = fmt.Sprintf("Download complete; subtitle generation failed for %d chapter(s)", failedSubtitles)
	}
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
