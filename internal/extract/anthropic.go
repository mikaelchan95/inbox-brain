package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"
	anthropicMaxTokens      = 1500
	anthropicTimeout        = 60 * time.Second
)

// anthropicProvider calls the Anthropic Messages API over net/http.
type anthropicProvider struct {
	apiKey  string
	model   string
	baseURL string // overridable in tests (httptest)
	client  *http.Client
}

// NewAnthropicProvider returns a Provider backed by the Anthropic Messages
// API using the given API key and model id.
func NewAnthropicProvider(apiKey, model string) Provider {
	return &anthropicProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: defaultAnthropicBaseURL,
		client:  &http.Client{Timeout: anthropicTimeout},
	}
}

func (a *anthropicProvider) Name() string { return "anthropic" }

func (a *anthropicProvider) ExtractActions(ctx context.Context, in ProviderInput) (ProviderResult, error) {
	system := anthropicSystemPrompt(in.Profile)
	transcript := buildTranscript(in.Messages)

	text, err := a.call(ctx, system, transcript)
	if err != nil {
		return ProviderResult{}, err
	}
	res, perr := parseActionsJSON(text)
	if perr != nil {
		// Invalid JSON: retry the HTTP call once, then fail the run
		// (spec §24.2 — failures recorded, ingestion not blocked).
		text, err = a.call(ctx, system, transcript)
		if err != nil {
			return ProviderResult{}, err
		}
		res, perr = parseActionsJSON(text)
		if perr != nil {
			return ProviderResult{}, fmt.Errorf("anthropic returned invalid JSON: %w", perr)
		}
	}
	return ValidateResult(res), nil
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// call performs one POST /v1/messages round trip and returns the
// concatenated text content of the response.
func (a *anthropicProvider) call(ctx context.Context, system, user string) (string, error) {
	payload, err := json.Marshal(anthropicRequest{
		Model:     a.model,
		MaxTokens: anthropicMaxTokens,
		System:    system,
		Messages:  []anthropicMessage{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", fmt.Errorf("encode anthropic request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("build anthropic request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call anthropic: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("anthropic API status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var ar anthropicResponse
	if err := json.Unmarshal(body, &ar); err != nil {
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	var sb strings.Builder
	for _, c := range ar.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	return sb.String(), nil
}

// parseActionsJSON extracts the {"actions":[...]} object from model output,
// stripping markdown fences defensively by slicing from the first "{" to the
// last "}".
func parseActionsJSON(text string) (ProviderResult, error) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return ProviderResult{}, fmt.Errorf("no JSON object found in %q", truncate(text, 80))
	}
	var res ProviderResult
	if err := json.Unmarshal([]byte(text[start:end+1]), &res); err != nil {
		return ProviderResult{}, fmt.Errorf("decode actions: %w", err)
	}
	return res, nil
}

// anthropicSystemPrompt builds the system prompt from the business profile.
func anthropicSystemPrompt(p model.BusinessProfile) string {
	var b strings.Builder
	b.WriteString("You extract business actions from a customer-chat transcript for this business:\n")
	fmt.Fprintf(&b, "- Business name: %s\n", p.BusinessName)
	fmt.Fprintf(&b, "- Business type: %s\n", p.BusinessType)
	fmt.Fprintf(&b, "- Services: %s\n", strings.Join(p.Services, ", "))
	fmt.Fprintf(&b, "- Reply tone: %s\n", p.Tone)
	fmt.Fprintf(&b, "- Reply language: %s\n", p.ReplyLanguage)
	b.WriteString("\nIdentify business actions in the transcript: leads, bookings, quote requests, payment issues, complaints, follow-ups. Suggested replies are drafts the user sends manually: write them in the configured tone and language and never invent prices.\n")
	b.WriteString("\nRespond with ONLY this JSON object and nothing else (no markdown, no commentary):\n")
	b.WriteString(`{"actions":[{"type":"...","title":"...","summary":"...","suggestedReply":"...","confidence":0,"urgency":"low|normal|high","messageExternalId":"..."}]}` + "\n")
	fmt.Fprintf(&b, "\"type\" must be one of: %s. \"confidence\" is 0-100.\n", strings.Join(model.ActionTypes, ", "))
	return b.String()
}

// buildTranscript renders messages as "[sender — time] text" lines.
func buildTranscript(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		sender := strings.TrimSpace(m.SenderName)
		if sender == "" {
			if m.Direction == model.DirectionOutbound {
				sender = "Me"
			} else {
				sender = "Customer"
			}
		}
		fmt.Fprintf(&b, "[%s — %s] %s\n", sender, m.OccurredAt.Format("2006-01-02 15:04"), m.Body)
	}
	return b.String()
}
