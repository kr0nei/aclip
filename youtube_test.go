package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestYouTubeVideoID(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		want    string
		wantErr bool
	}{
		{name: "watch URL", url: "https://www.youtube.com/watch?v=abc_def-1234", want: "abc_def-1234"},
		{name: "short URL", url: "https://youtu.be/abcdef12345?feature=share", want: "abcdef12345"},
		{name: "shorts URL", url: "https://youtube.com/shorts/abcdef12345", want: "abcdef12345"},
		{name: "foreign host", url: "https://youtube.com.evil.example/watch?v=abcdef12345", wantErr: true},
		{name: "missing ID", url: "https://youtube.com/watch", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := youtubeVideoID(test.url)
			if (err != nil) != test.wantErr {
				t.Fatalf("youtubeVideoID() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("youtubeVideoID() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestListMP4ChaptersPrefersSplitFiles(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"Video.mp4", "01 - Intro.mp4", "02 - The Story.mp4", "captions.srt"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	chapters, err := listMP4Chapters(directory)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"01 - Intro", "02 - The Story"}
	var got []string
	for _, chapter := range chapters {
		got = append(got, chapter.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chapter names = %v, want %v", got, want)
	}
}
