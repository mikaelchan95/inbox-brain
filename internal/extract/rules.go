package extract

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// rulesProvider is the deterministic offline fallback provider. It runs
// keyword heuristics over the (already privacy-filtered) input messages and
// never makes network calls.
type rulesProvider struct{}

// NewRulesProvider returns the deterministic keyword-heuristic provider that
// works offline.
func NewRulesProvider() Provider { return rulesProvider{} }

func (rulesProvider) Name() string { return "rules" }

// maxRuleActions caps how many actions the rules provider emits per
// conversation.
const maxRuleActions = 2

var (
	bookingWords        = []string{"book", "booking", "appointment", "trial", "slot", "reschedule"}
	quoteWords          = []string{"quote", "quotation", "how much", "price", "pricing", "rate"}
	paymentWords        = []string{"invoice", "payment", "deposit", "refund", "paid"}
	paymentProblemWords = []string{"chase", "chasing", "unpaid", "wrong", "overdue", "missing", "incorrect"}
	complaintWords      = []string{"unhappy", "wrong", "broken", "disappointed", "not working", "looks off"}
	availabilityWords   = []string{"available", "availability", "are you free", "do you do", "do you offer", "looking for"}
)

func (rp rulesProvider) ExtractActions(_ context.Context, in ProviderInput) (ProviderResult, error) {
	var res ProviderResult
	seen := map[string]bool{}
	for _, m := range in.Messages {
		if m.Direction == model.DirectionOutbound {
			continue
		}
		if len(res.Actions) >= maxRuleActions {
			break
		}
		typ := detectActionType(m.Body)
		if typ == "" || seen[typ] {
			continue
		}
		seen[typ] = true
		res.Actions = append(res.Actions, buildRuleAction(in.Profile, in.Conversation, m, typ))
	}
	// No other action found: if the conversation asks about the business's
	// services or availability, treat it as a new lead.
	if len(res.Actions) == 0 {
		if m, ok := latestServiceAsk(in); ok {
			res.Actions = append(res.Actions, buildRuleAction(in.Profile, in.Conversation, m, model.ActionNewLead))
		}
	}
	return res, nil
}

// detectActionType maps one message body to an action type ("" = none).
// Payment wording wins over complaint wording ("the invoice is wrong" is a
// payment issue), complaints win over booking/quote asks.
func detectActionType(body string) string {
	text := strings.ToLower(body)
	switch {
	case containsAny(text, paymentWords):
		if containsAny(text, paymentProblemWords) {
			return model.ActionPaymentIssue
		}
		return model.ActionFollowUp
	case containsAny(text, complaintWords):
		return model.ActionComplaint
	case containsAny(text, bookingWords):
		return model.ActionBookingRequest
	case containsAny(text, quoteWords):
		return model.ActionQuoteRequest
	}
	return ""
}

// latestServiceAsk returns the newest non-outbound message that asks about
// the profile's services or about availability.
func latestServiceAsk(in ProviderInput) (model.Message, bool) {
	for i := len(in.Messages) - 1; i >= 0; i-- {
		m := in.Messages[i]
		if m.Direction == model.DirectionOutbound {
			continue
		}
		text := strings.ToLower(m.Body)
		if containsAny(text, availabilityWords) || mentionsService(text, in.Profile.Services) {
			return m, true
		}
	}
	return model.Message{}, false
}

func mentionsService(text string, services []string) bool {
	for _, s := range services {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && containsPhrase(text, s) {
			return true
		}
	}
	return false
}

func buildRuleAction(profile model.BusinessProfile, conv model.Conversation, m model.Message, typ string) ExtractedAction {
	name := strings.TrimSpace(m.SenderName)
	if name == "" {
		name = strings.TrimSpace(conv.Title)
	}
	if name == "" {
		name = "customer"
	}
	urgency := "normal"
	if typ == model.ActionComplaint {
		urgency = "high"
	}
	return ExtractedAction{
		Type:              typ,
		Title:             titleFromType(typ) + " from " + name,
		Summary:           fmt.Sprintf("%s wrote: %q", name, truncate(m.Body, 140)),
		SuggestedReply:    ruleReply(profile, name, typ),
		Confidence:        70,
		Urgency:           urgency,
		MessageExternalID: m.MessageExternalID,
	}
}

// ruleReply drafts a short reply (1–3 sentences) in the profile's tone that
// acknowledges the ask, names the business, proposes a next step, and never
// invents prices.
func ruleReply(profile model.BusinessProfile, name, typ string) string {
	greeting := "Hi"
	if strings.EqualFold(strings.TrimSpace(profile.Tone), "formal") {
		greeting = "Hello"
	}
	business := strings.TrimSpace(profile.BusinessName)
	if business == "" {
		business = "our team"
	}
	if name == "" || name == "customer" {
		name = "there"
	}
	switch typ {
	case model.ActionBookingRequest:
		return fmt.Sprintf("%s %s! Thanks for reaching out to %s — happy to help with a booking. Could you share a couple of times that work for you, and I'll confirm a slot?", greeting, name, business)
	case model.ActionQuoteRequest:
		return fmt.Sprintf("%s %s! Thanks for your interest in %s. Could you share a few more details about what you need, and I'll put together a quote for you?", greeting, name, business)
	case model.ActionPaymentIssue:
		return fmt.Sprintf("%s %s, thanks for flagging this — sorry for the trouble. I'll check the payment records at %s right away and get back to you with an update.", greeting, name, business)
	case model.ActionComplaint:
		return fmt.Sprintf("%s %s, I'm really sorry to hear that. We take this seriously at %s — let me look into it right away and come back to you with a fix.", greeting, name, business)
	case model.ActionNewLead:
		return fmt.Sprintf("%s %s! Thanks for reaching out to %s. I'd love to help — could you tell me a bit more about what you're looking for?", greeting, name, business)
	default: // follow_up and anything else
		return fmt.Sprintf("%s %s, thanks for your message! I'll follow up from %s shortly with the next steps.", greeting, name, business)
	}
}

func containsAny(text string, phrases []string) bool {
	for _, p := range phrases {
		if containsPhrase(text, p) {
			return true
		}
	}
	return false
}

// containsPhrase reports whether lowercase text contains the lowercase phrase
// with word boundaries on both ends ("rate" must not match inside
// "celebrate").
func containsPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	start := 0
	for {
		i := strings.Index(text[start:], phrase)
		if i < 0 {
			return false
		}
		i += start
		if wordBoundaryBefore(text, i) && wordBoundaryAfter(text, i+len(phrase)) {
			return true
		}
		start = i + 1
	}
}

func wordBoundaryBefore(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return !isWordRune(r)
}

func wordBoundaryAfter(text string, j int) bool {
	if j >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[j:])
	return !isWordRune(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
