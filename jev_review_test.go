package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestJevClipReviewUsesStateAndNoulQuestion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/alpha/decisions" {
			t.Fatalf("unexpected Jev endpoint: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var request struct {
			Model     string                 `json:"model"`
			State     string                 `json:"state"`
			Questions map[string]jevQuestion `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		question, ok := request.Questions["clip_worthwhile"]
		if request.Model != jevOpenRouterModel || !strings.Contains(request.State, "strong opening hook") || !ok || question.Type != "noul" {
			t.Fatalf("unexpected Jev request: %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"clip_worthwhile":{"type":"noul","noul":0.84}}}`))
	}))
	defer server.Close()

	state := `{"clipCandidate":{"hook":"strong opening hook"},"srtExcerpts":"0-10 | hook"}`
	review, err := requestJevClipReview(context.Background(), server.Client(), server.URL+"/api/alpha/decisions", "test-token", state)
	if err != nil {
		t.Fatal(err)
	}
	if !review.Worthwhile || !review.HasProbability || review.Probability != 0.84 || review.Verdict != "Worth a test" {
		t.Fatalf("unexpected Jev review: %#v", review)
	}
	if review.Rationale != "" {
		t.Fatalf("Jev's typed answer should not invent a text rationale: %q", review.Rationale)
	}
}
