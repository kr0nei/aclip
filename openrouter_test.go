package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnalyzeSubtitlesUsesOpenRouterAndParsesClipSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/chat/completions" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var request struct {
			Model          string `json:"model"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []openRouterMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "openai/test-model" || request.ResponseFormat.Type != "json_object" {
			t.Fatalf("unexpected request model/format: %#v", request)
		}
		if !strings.Contains(request.Messages[1].Content, "30 to 45 seconds") || !strings.Contains(request.Messages[1].Content, "00:00:00,000 --> 00:00:35,000") {
			t.Fatalf("request prompt did not contain clip constraints and SRT: %s", request.Messages[1].Content)
		}
		w.Header().Set("Content-Type", "application/json")
		analysisJSON := `{"summary":"A useful insight","clips":[{"title":"The surprising trick","hook":"Nobody tells you this","whyItCouldWork":"Curiosity followed by a clear payoff","caption":"Try this next time","hashtags":["#tips"],"potentialScore":87,"segments":[{"startSeconds":0,"endSeconds":35,"editNote":"Keep the opening and payoff together"}]}]}`
		responseBody, err := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": analysisJSON}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()

	analysis, err := analyzeSubtitlesWithClient(
		context.Background(), server.Client(), server.URL+"/api/v1/chat/completions",
		"test-token", "openai/test-model", "Video", "01 - Chapter", 50,
		"1\n00:00:00,000 --> 00:00:35,000\nNobody tells you this\n",
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Clips) != 1 || analysis.Clips[0].DurationSeconds != 35 {
		t.Fatalf("unexpected clip analysis: %#v", analysis)
	}
}

func TestNormalizeClipAnalysisRejectsInvalidDurationAndOverlappingSegments(t *testing.T) {
	tests := []ChapterAnalysis{
		{Clips: []ClipCandidate{{Title: "Too short", Hook: "Hook", Segments: []ClipSegment{{StartSeconds: 0, EndSeconds: 29.9}}}}},
		{Clips: []ClipCandidate{{Title: "Overlap", Hook: "Hook", Segments: []ClipSegment{{StartSeconds: 0, EndSeconds: 20}, {StartSeconds: 19, EndSeconds: 35}}}}},
	}
	for i := range tests {
		if err := normalizeClipAnalysis(&tests[i], 60); err == nil {
			t.Fatalf("case %d accepted invalid clip data", i)
		}
	}
}

func TestFormatVideoMetadata(t *testing.T) {
	metadata := parseVideoMetadata([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264","width":1920,"height":1080},{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"42.5"}}`), 2_097_152)
	if metadata.DurationSeconds != 42.5 || metadata.Width != 1920 || metadata.Height != 1080 || metadata.SizeBytes != 2_097_152 || metadata.VideoCodec != "h264" || metadata.AudioCodec != "aac" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if got := formatDuration(metadata.DurationSeconds); got != "0:43" {
		t.Fatalf("formatDuration = %q", got)
	}
	if got := formatFileSize(metadata.SizeBytes); got != "2.0 MB" {
		t.Fatalf("formatFileSize = %q", got)
	}
}
