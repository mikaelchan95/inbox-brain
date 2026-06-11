package extract

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

const validActionsJSON = `{"actions":[{"type":"booking_request","title":"Booking request from Mrs Tan","summary":"Wants a Saturday trial class","suggestedReply":"Hi Mrs Tan!","confidence":90,"urgency":"normal","messageExternalId":"tg-1"}]}`

// anthropicTextResponse wraps text in an Anthropic Messages API response body.
func anthropicTextResponse(text string) string {
	b, _ := json.Marshal(map[string]any{
		"content": []map[string]string{{"type": "text", "text": text}},
	})
	return string(b)
}

func anthropicTestProvider(t *testing.T, handler http.HandlerFunc) *anthropicProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewAnthropicProvider("test-key", "claude-test-model").(*anthropicProvider)
	p.baseURL = srv.URL
	return p
}

func sampleInput() ProviderInput {
	return ProviderInput{
		Profile:      testProfile,
		Conversation: model.Conversation{ID: "conv_1", Title: "Mrs Tan"},
		Messages: []model.Message{{
			SenderName: "Mrs Tan",
			Body:       "Can I book a trial class on Saturday?",
			Direction:  model.DirectionInbound,
			OccurredAt: time.Date(2026, 6, 1, 10, 30, 0, 0, time.UTC),
		}},
	}
}

func TestAnthropicHappyPath(t *testing.T) {
	type capturedRequest struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	var got capturedRequest
	var gotMethod, gotPath, gotKey, gotVersion, gotContentType string

	p := anthropicTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		io.WriteString(w, anthropicTextResponse(validActionsJSON))
	})

	res, err := p.ExtractActions(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/v1/messages" {
		t.Errorf("request = %s %s, want POST /v1/messages", gotMethod, gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", gotKey)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want 2023-06-01", gotVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if got.Model != "claude-test-model" {
		t.Errorf("model = %q, want claude-test-model", got.Model)
	}
	if got.MaxTokens != 1500 {
		t.Errorf("max_tokens = %d, want 1500", got.MaxTokens)
	}

	for _, want := range []string{
		"Alex Design Studio", "freelance designer", "logo design", "landing pages",
		"friendly", "English", `{"actions":`, model.ActionBookingRequest,
	} {
		if !strings.Contains(got.System, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}

	if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", got.Messages)
	}
	if want := "[Mrs Tan — 2026-06-01 10:30] Can I book a trial class on Saturday?"; !strings.Contains(got.Messages[0].Content, want) {
		t.Errorf("transcript = %q, want it to contain %q", got.Messages[0].Content, want)
	}

	if len(res.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(res.Actions))
	}
	a := res.Actions[0]
	if a.Type != model.ActionBookingRequest || a.Title != "Booking request from Mrs Tan" ||
		a.Confidence != 90 || a.Urgency != "normal" || a.MessageExternalID != "tg-1" {
		t.Errorf("unexpected action: %+v", a)
	}
}

func TestAnthropicStripsMarkdownFences(t *testing.T) {
	p := anthropicTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, anthropicTextResponse("```json\n"+validActionsJSON+"\n```"))
	})
	res, err := p.ExtractActions(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 1 || res.Actions[0].Type != model.ActionBookingRequest {
		t.Errorf("fenced JSON not parsed: %+v", res.Actions)
	}
}

func TestAnthropicRetriesOnceOnInvalidJSON(t *testing.T) {
	calls := 0
	p := anthropicTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			io.WriteString(w, anthropicTextResponse("Sorry, I could not find any actions!"))
			return
		}
		io.WriteString(w, anthropicTextResponse(validActionsJSON))
	})
	res, err := p.ExtractActions(context.Background(), sampleInput())
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if calls != 2 {
		t.Errorf("HTTP calls = %d, want 2 (one retry)", calls)
	}
	if len(res.Actions) != 1 {
		t.Errorf("actions = %d, want 1", len(res.Actions))
	}
}

func TestAnthropicFailsAfterTwoInvalidResponses(t *testing.T) {
	calls := 0
	p := anthropicTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		io.WriteString(w, anthropicTextResponse("still not json"))
	})
	_, err := p.ExtractActions(context.Background(), sampleInput())
	if err == nil {
		t.Fatal("ExtractActions = nil error, want invalid-JSON error")
	}
	if calls != 2 {
		t.Errorf("HTTP calls = %d, want 2 (exactly one retry)", calls)
	}
}

func TestAnthropicAPIError(t *testing.T) {
	calls := 0
	p := anthropicTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error"}}`)
	})
	_, err := p.ExtractActions(context.Background(), sampleInput())
	if err == nil {
		t.Fatal("ExtractActions = nil error, want API error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want it to mention status 500", err)
	}
	if calls != 1 {
		t.Errorf("HTTP calls = %d, want 1 (no retry on HTTP errors)", calls)
	}
}

func TestAnthropicProviderName(t *testing.T) {
	if got := NewAnthropicProvider("k", "m").Name(); got != "anthropic" {
		t.Errorf("Name() = %q, want anthropic", got)
	}
}
