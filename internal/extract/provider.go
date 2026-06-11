// Package extract implements the action-extraction pipeline and its AI
// providers. The pipeline enforces the privacy gate from spec §13/§25:
// nothing from conversations classified personal, user-overridden personal,
// or unreviewed unknown is ever passed to a Provider, and mixed chats only
// expose their business-labeled messages.
package extract

import (
	"context"
	"strings"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// ProviderInput is the ONLY data ever sent to an AI provider.
type ProviderInput struct {
	Profile      model.BusinessProfile
	Conversation model.Conversation
	Messages     []model.Message // pre-filtered business-relevant context only
}

// ExtractedAction is one action proposed by a provider, pre-validation.
type ExtractedAction struct {
	Type              string  `json:"type"` // must be in model.ActionTypes
	Title             string  `json:"title"`
	Summary           string  `json:"summary"`
	SuggestedReply    string  `json:"suggestedReply"`
	Confidence        float64 `json:"confidence"`                  // 0–100
	Urgency           string  `json:"urgency"`                     // low|normal|high
	MessageExternalID string  `json:"messageExternalId,omitempty"` // anchor message if known
}

// ProviderResult is a provider's full answer for one conversation.
type ProviderResult struct{ Actions []ExtractedAction }

// Provider extracts business actions from pre-approved conversation context.
type Provider interface {
	Name() string
	ExtractActions(ctx context.Context, in ProviderInput) (ProviderResult, error)
}

// ValidateResult cleans a provider result: actions with an unknown type or
// type no_action are dropped, confidence is clamped to 0–100, empty titles
// are synthesized from the type, and urgency is normalized to low/normal/high
// (default normal).
func ValidateResult(r ProviderResult) ProviderResult {
	var out ProviderResult
	for _, a := range r.Actions {
		if a.Type == model.ActionNoAction || !validActionType(a.Type) {
			continue
		}
		if a.Confidence < 0 {
			a.Confidence = 0
		}
		if a.Confidence > 100 {
			a.Confidence = 100
		}
		if strings.TrimSpace(a.Title) == "" {
			a.Title = titleFromType(a.Type)
		}
		switch strings.ToLower(strings.TrimSpace(a.Urgency)) {
		case "low":
			a.Urgency = "low"
		case "high":
			a.Urgency = "high"
		default:
			a.Urgency = "normal"
		}
		out.Actions = append(out.Actions, a)
	}
	return out
}

func validActionType(t string) bool {
	for _, v := range model.ActionTypes {
		if t == v {
			return true
		}
	}
	return false
}

// titleFromType turns an action type into a short title, e.g.
// "quote_request" → "Quote request".
func titleFromType(t string) string {
	s := strings.ReplaceAll(t, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// truncate trims s and caps it at n runes, appending an ellipsis when cut.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
