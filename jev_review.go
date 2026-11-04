package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	jevOpenRouterModel = "typesafe/jev-1.14"
	jevDecisionsURL    = "https://openrouter.ai/api/alpha/decisions"
)

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevReviewRequest struct {
	Model     string                 `json:"model"`
	State     string                 `json:"state"`
	Questions map[string]jevQuestion `json:"questions"`
}

func (s *YouTubeService) ReviewClip(ctx context.Context, videoPath, videoTitle string, clipIndex int) (DownloadedChapter, error) {
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
	if chapter.SubtitleStatus != "ready" || chapter.Analysis == nil {
		return DownloadedChapter{}, fmt.Errorf("generate subtitles and AI suggestions before reviewing a clip")
	}
	if clipIndex < 0 || clipIndex >= len(chapter.Analysis.Clips) {
		return DownloadedChapter{}, fmt.Errorf("clip candidate no longer exists")
	}
	if videoTitle == "" {
		videoTitle = chapter.VideoTitle
	}
	token, err := keyring.Get(credentialService, credentialName)
	if errors.Is(err, keyring.ErrNotFound) {
		return DownloadedChapter{}, fmt.Errorf("add an OpenRouter API key in Settings before reviewing clips")
	}
	if err != nil {
		return DownloadedChapter{}, fmt.Errorf("read OpenRouter API key: %w", err)
	}
	info, err := os.Stat(chapter.SubtitlePath)
	if err != nil {
		return DownloadedChapter{}, fmt.Errorf("find chapter subtitles: %w", err)
	}
	if info.Size() > maxSubtitleSourceBytes {
		return DownloadedChapter{}, fmt.Errorf("subtitle is too large to review (maximum 1 MB)")
	}
	srt, err := os.ReadFile(chapter.SubtitlePath)
	if err != nil {
		return DownloadedChapter{}, fmt.Errorf("read chapter subtitles: %w", err)
	}
	clip := chapter.Analysis.Clips[clipIndex]
	clip.Review = nil
	evidence := subtitleExcerptsForClip(compactSRT(string(srt)), clip.Segments)
	state, err := json.Marshal(struct {
		Task          string        `json:"task"`
		VideoTitle    string        `json:"videoTitle"`
		Chapter       string        `json:"chapter"`
		ChapterLength float64       `json:"chapterLengthSeconds"`
		Clip          ClipCandidate `json:"clipCandidate"`
		SRTExcerpts   string        `json:"srtExcerpts"`
		Limitations   string        `json:"limitations"`
	}{
		Task:          "Review whether this specific proposed short-form edit is worthwhile to produce.",
		VideoTitle:    videoTitle,
		Chapter:       chapter.Name,
		ChapterLength: chapter.DurationSeconds,
		Clip:          clip,
		SRTExcerpts:   evidence,
		Limitations:   "Judge only the supplied SRT text, timestamps, and edit plan. Visuals, audio delivery, and actual audience response are unavailable; do not guarantee virality.",
	})
	if err != nil {
		return DownloadedChapter{}, fmt.Errorf("encode clip review state: %w", err)
	}
	review, err := requestJevClipReview(ctx, &http.Client{Timeout: 60 * time.Second}, jevDecisionsURL, token, string(state))
	if err != nil {
		return DownloadedChapter{}, err
	}

	analysis := *chapter.Analysis
	analysis.Clips = append([]ClipCandidate(nil), analysis.Clips...)
	analysis.Clips[clipIndex].Review = &review
	chapter.Analysis = &analysis
	chapter.AnalysisError = ""
	if err := s.saveChapterAnalysis(chapter, videoTitle, analysis); err != nil {
		chapter.AnalysisError = "Review completed but could not be saved: " + err.Error()
	}
	s.notifyIfUnfocused(
		fmt.Sprintf("clip-review-%d", time.Now().UnixNano()),
		"Clip review complete",
		fmt.Sprintf("Jev reviewed %q: %s", clip.Title, review.Verdict),
	)
	return chapter, nil
}

func requestJevClipReview(ctx context.Context, client *http.Client, endpoint, token, state string) (ClipReview, error) {
	request := jevReviewRequest{
		Model: jevOpenRouterModel,
		State: state,
		Questions: map[string]jevQuestion{
			"clip_worthwhile": {
				Type:         "noul",
				Instructions: "Is this clip candidate worthwhile to produce as an Instagram Reel or TikTok? Return the probability that it is worth producing, based only on the supplied evidence.",
				Criteria: map[string]string{
					"true":  "A clear hook, coherent stitched edit, faithful transcript excerpts, satisfying payoff, 30-45 second total, and a usable caption make it a promising candidate.",
					"false": "The hook is weak or confusing, the edit loses context, excerpts do not support the claim, the payoff is missing, the timing is invalid, or the caption adds little.",
				},
			},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		return ClipReview{}, fmt.Errorf("encode Jev review request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return ClipReview{}, fmt.Errorf("create Jev review request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OpenRouter-Title", "aclip")
	response, err := client.Do(req)
	if err != nil {
		return ClipReview{}, fmt.Errorf("call Jev through OpenRouter: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ClipReview{}, fmt.Errorf("read Jev response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ClipReview{}, fmt.Errorf("OpenRouter Jev review returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return parseJevClipReview(responseBody)
}

func parseJevClipReview(content []byte) (ClipReview, error) {
	var response any
	if err := json.Unmarshal(content, &response); err != nil {
		return ClipReview{}, fmt.Errorf("parse Jev response: %w", err)
	}
	node := findJevQuestion(response, "clip_worthwhile")
	if node == nil {
		node = response
	}
	probability, hasProbability := findJevProbability(node)
	answer, hasAnswer := findJevAnswer(node)
	if hasProbability && probability > 1 && probability <= 100 {
		probability /= 100
	}
	if !hasProbability && hasAnswer {
		if answer {
			probability = 1
		} else {
			probability = 0
		}
	}
	if !hasProbability && !hasAnswer {
		return ClipReview{}, fmt.Errorf("Jev response did not contain a clip-worthwhile judgment")
	}
	if probability < 0 || probability > 1 {
		return ClipReview{}, fmt.Errorf("Jev returned an invalid probability")
	}
	if hasProbability {
		answer = probability >= 0.5
	}
	verdict := "Borderline"
	if probability >= 0.75 {
		verdict = "Worth a test"
	} else if probability <= 0.35 {
		verdict = "Weak candidate"
	}
	rationale := findJevText(node, map[string]bool{"rationale": true, "reason": true, "explanation": true, "summary": true})
	return ClipReview{
		Worthwhile:     answer,
		HasProbability: hasProbability,
		Probability:    probability,
		Verdict:        verdict,
		Rationale:      rationale,
	}, nil
}

func findJevQuestion(value any, question string) any {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if strings.EqualFold(key, question) {
				return child
			}
		}
		for _, child := range current {
			if found := findJevQuestion(child, question); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range current {
			if found := findJevQuestion(child, question); found != nil {
				return found
			}
		}
	}
	return nil
}

func findJevProbability(value any) (float64, bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "probability" || normalized == "noul" || normalized == "ptrue" || normalized == "probabilitytrue" || normalized == "trueprobability" {
				if number, ok := child.(float64); ok {
					return number, true
				}
			}
			if normalized == "distribution" || normalized == "probabilities" {
				if distribution, ok := child.(map[string]any); ok {
					for label, probability := range distribution {
						if strings.EqualFold(label, "true") || strings.EqualFold(label, "yes") || strings.EqualFold(label, "worthwhile") {
							if number, ok := probability.(float64); ok {
								return number, true
							}
						}
					}
				}
			}
		}
		for _, child := range current {
			if number, ok := findJevProbability(child); ok {
				return number, true
			}
		}
	case []any:
		for _, child := range current {
			if number, ok := findJevProbability(child); ok {
				return number, true
			}
		}
	}
	return 0, false
}

func findJevAnswer(value any) (bool, bool) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			normalized := strings.ToLower(strings.ReplaceAll(key, "_", ""))
			if normalized == "answer" || normalized == "value" || normalized == "worthwhile" || normalized == "truth" || normalized == "istrue" {
				if answer, ok := child.(bool); ok {
					return answer, true
				}
				if label, ok := child.(string); ok {
					if answer, valid := parseJevVerdict(label); valid {
						return answer, true
					}
				}
			}
			if normalized == "verdict" || normalized == "label" || normalized == "classification" {
				if label, ok := child.(string); ok {
					if answer, valid := parseJevVerdict(label); valid {
						return answer, true
					}
				}
			}
		}
		for _, child := range current {
			if answer, ok := findJevAnswer(child); ok {
				return answer, true
			}
		}
	case []any:
		for _, child := range current {
			if answer, ok := findJevAnswer(child); ok {
				return answer, true
			}
		}
	}
	return false, false
}

func parseJevVerdict(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "likely", "worthwhile", "recommended", "promising":
		return true, true
	case "false", "no", "unlikely", "not worthwhile", "not recommended", "weak":
		return false, true
	default:
		return false, false
	}
}

func findJevText(value any, keys map[string]bool) string {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if keys[strings.ToLower(strings.ReplaceAll(key, "_", ""))] {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
		for _, child := range current {
			if text := findJevText(child, keys); text != "" {
				return text
			}
		}
	case []any:
		for _, child := range current {
			if text := findJevText(child, keys); text != "" {
				return text
			}
		}
	}
	return ""
}

func subtitleExcerptsForClip(compactSubtitle string, segments []ClipSegment) string {
	var excerpts []string
	for _, line := range strings.Split(compactSubtitle, "\n") {
		span, _, found := strings.Cut(line, " | ")
		if !found {
			continue
		}
		startText, endText, found := strings.Cut(span, "-")
		if !found {
			continue
		}
		start, startErr := strconv.ParseFloat(startText, 64)
		end, endErr := strconv.ParseFloat(endText, 64)
		if startErr != nil || endErr != nil {
			continue
		}
		for _, segment := range segments {
			if start < segment.EndSeconds && end > segment.StartSeconds {
				excerpts = append(excerpts, line)
				break
			}
		}
	}
	joined := strings.Join(excerpts, "\n")
	if len(joined) <= 16_000 {
		return joined
	}
	return string([]rune(joined)[:min(len([]rune(joined)), 16_000)])
}
