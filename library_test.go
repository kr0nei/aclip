package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadVideoFolderRestoresSubtitlesAndCombinedSuggestions(t *testing.T) {
	directory := t.TempDir()
	writeTestMedia(t, directory, "01 - Intro.mp4", "01 - Intro.srt")
	if err := writeVideoManifest(directory, "A Sample Video", "https://youtu.be/abcdef12345"); err != nil {
		t.Fatal(err)
	}
	saved := folderSuggestions{
		Version:    1,
		VideoTitle: "A Sample Video",
		Chapters: []savedChapterAnalysis{{
			VideoFile: "01 - Intro.mp4",
			Analysis:  sampleChapterAnalysis("Edited caption"),
		}},
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, clipSuggestionsFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}

	contents, err := (&YouTubeService{}).loadVideoFolder(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if contents.Title != "A Sample Video" || len(contents.Chapters) != 1 {
		t.Fatalf("unexpected folder contents: %#v", contents)
	}
	chapter := contents.Chapters[0]
	if chapter.SubtitleStatus != "ready" || chapter.AnalysisStatus != "ready" || chapter.Analysis == nil {
		t.Fatalf("SRT or saved analysis was not restored: %#v", chapter)
	}
	if chapter.Analysis.Clips[0].Caption != "Edited caption" {
		t.Fatalf("restored caption = %q", chapter.Analysis.Clips[0].Caption)
	}
}

func TestSaveChapterAnalysisKeepsAllChaptersAndUpdatesCaption(t *testing.T) {
	directory := t.TempDir()
	writeTestMedia(t, directory, "01 - Intro.mp4", "01 - Intro.srt")
	writeTestMedia(t, directory, "02 - Payoff.mp4", "02 - Payoff.srt")
	if err := writeVideoManifest(directory, "A Sample Video", "https://youtu.be/abcdef12345"); err != nil {
		t.Fatal(err)
	}
	service := &YouTubeService{}
	service.authorizeFolder(directory)
	firstPath := filepath.Join(directory, "01 - Intro.mp4")
	secondPath := filepath.Join(directory, "02 - Payoff.mp4")
	if _, err := service.SaveChapterAnalysis(firstPath, "A Sample Video", sampleChapterAnalysis("First caption")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveChapterAnalysis(secondPath, "A Sample Video", sampleChapterAnalysis("Second caption")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveChapterAnalysis(firstPath, "A Sample Video", sampleChapterAnalysis("Edited first caption")); err != nil {
		t.Fatal(err)
	}

	saved, err := readFolderSuggestions(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Chapters) != 2 {
		t.Fatalf("combined suggestions has %d chapters, want 2", len(saved.Chapters))
	}
	if saved.Chapters[0].Analysis.Clips[0].Caption != "Edited first caption" {
		t.Fatalf("edited caption was not saved: %#v", saved.Chapters[0])
	}
}

func writeTestMedia(t *testing.T, directory, videoName, subtitleName string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, videoName), []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, subtitleName), []byte("1\n00:00:00,000 --> 00:00:35,000\nTest subtitles\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sampleChapterAnalysis(caption string) ChapterAnalysis {
	return ChapterAnalysis{
		Summary: "A sample chapter",
		Clips: []ClipCandidate{{
			Title:          "Sample clip",
			Hook:           "A strong hook",
			WhyItCouldWork: "It has a payoff",
			Caption:        caption,
			PotentialScore: 82,
			Segments: []ClipSegment{
				{StartSeconds: 0, EndSeconds: 12, EditNote: "Hook"},
				{StartSeconds: 15, EndSeconds: 27, EditNote: "Insight"},
				{StartSeconds: 30, EndSeconds: 41, EditNote: "Payoff"},
			},
		}},
	}
}
