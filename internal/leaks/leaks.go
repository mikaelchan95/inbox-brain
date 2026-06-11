// Package leaks detects revenue leaks (spec §18): stale open actions, stale
// leads and unanswered customer questions. Only business-approved
// conversations are considered, mirroring the extraction gate (spec §13).
package leaks

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// Leak is one detected revenue leak.
type Leak struct {
	Kind             string // unanswered_question, stale_quote, stale_booking, stale_complaint, stale_lead, payment_unresolved
	Severity         string // low|medium|high
	ConversationID   string
	ConversationName string
	ActionID         string // optional
	Description      string
	Since            time.Time
}

// actionRule maps an open action type to the leak it produces once stale.
type actionRule struct {
	kind     string
	severity string
	maxAge   time.Duration
	noun     string // leading noun of the description, e.g. "Quote request"
}

var actionRules = map[string]actionRule{
	model.ActionQuoteRequest:   {kind: "stale_quote", severity: "high", maxAge: 48 * time.Hour, noun: "Quote request"},
	model.ActionBookingRequest: {kind: "stale_booking", severity: "high", maxAge: 24 * time.Hour, noun: "Booking request"},
	model.ActionComplaint:      {kind: "stale_complaint", severity: "high", maxAge: 12 * time.Hour, noun: "Complaint"},
	model.ActionPaymentIssue:   {kind: "payment_unresolved", severity: "medium", maxAge: 24 * time.Hour, noun: "Payment issue"},
}

const (
	leadMaxAge     = 72 * time.Hour
	questionMaxAge = 24 * time.Hour
)

// Detect scans business-approved conversations for revenue leaks as of now.
// Results are sorted high severity first, then oldest Since first.
func Detect(s *store.Store, now time.Time) ([]Leak, error) {
	approved, err := approvedConversations(s)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(approved))
	for id := range approved {
		conv, err := s.GetConversation(id)
		if err != nil {
			return nil, fmt.Errorf("load conversation %s: %w", id, err)
		}
		if conv != nil {
			names[id] = displayName(*conv)
		}
	}
	convName := func(id string) string {
		if n := names[id]; n != "" {
			return n
		}
		return id
	}

	var out []Leak

	actions, err := s.ListActions(store.ActionFilter{Status: model.StatusOpen})
	if err != nil {
		return nil, fmt.Errorf("list open actions: %w", err)
	}
	for _, a := range actions {
		if !approved[a.ConversationID] {
			continue
		}
		rule, ok := actionRules[a.Type]
		if !ok {
			continue
		}
		openFor := now.Sub(a.CreatedAt)
		if openFor <= rule.maxAge {
			continue
		}
		name := convName(a.ConversationID)
		out = append(out, Leak{
			Kind:             rule.kind,
			Severity:         rule.severity,
			ConversationID:   a.ConversationID,
			ConversationName: name,
			ActionID:         a.ID,
			Description:      fmt.Sprintf("%s from %s open for %s", rule.noun, name, humanAge(openFor)),
			Since:            a.CreatedAt,
		})
	}

	leads, err := s.ListLeads(model.LeadOpen)
	if err != nil {
		return nil, fmt.Errorf("list open leads: %w", err)
	}
	for _, l := range leads {
		if !approved[l.ConversationID] {
			continue
		}
		openFor := now.Sub(l.CreatedAt)
		if openFor <= leadMaxAge {
			continue
		}
		name := convName(l.ConversationID)
		out = append(out, Leak{
			Kind:             "stale_lead",
			Severity:         "medium",
			ConversationID:   l.ConversationID,
			ConversationName: name,
			ActionID:         l.ActionID,
			Description:      fmt.Sprintf("Lead from %s open for %s", name, humanAge(openFor)),
			Since:            l.CreatedAt,
		})
	}

	for id := range approved {
		msgs, err := s.ListMessages(id, 0)
		if err != nil {
			return nil, fmt.Errorf("list messages for %s: %w", id, err)
		}
		since, ok := unansweredQuestionSince(msgs, now)
		if !ok {
			continue
		}
		name := convName(id)
		out = append(out, Leak{
			Kind:             "unanswered_question",
			Severity:         "medium",
			ConversationID:   id,
			ConversationName: name,
			Description:      fmt.Sprintf("Question from %s unanswered for %s", name, humanAge(now.Sub(since))),
			Since:            since,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := severityRank(a.Severity), severityRank(b.Severity); ra != rb {
			return ra < rb
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ConversationID != b.ConversationID {
			return a.ConversationID < b.ConversationID
		}
		return a.ActionID < b.ActionID
	})
	return out, nil
}

// approvedConversations returns the set of conversation ids whose effective
// classification is business-approved.
func approvedConversations(s *store.Store) (map[string]bool, error) {
	all, err := s.ListConversationClassifications()
	if err != nil {
		return nil, fmt.Errorf("list conversation classifications: %w", err)
	}
	approved := make(map[string]bool)
	for _, c := range all {
		if isApproved(c) {
			approved[c.ConversationID] = true
		}
	}
	return approved, nil
}

// isApproved mirrors the extraction gate (spec §13): user_override personal
// always excludes; user_override business or mixed includes; otherwise label
// mixed includes and label business includes only when reviewed by the user.
func isApproved(c model.ConversationClassification) bool {
	switch c.UserOverride {
	case model.ConvPersonal:
		return false
	case model.ConvBusiness, model.ConvMixed:
		return true
	}
	switch c.Classification {
	case model.ConvMixed:
		return true
	case model.ConvBusiness:
		return c.ReviewedByUser
	}
	return false
}

// unansweredQuestionSince reports whether the latest inbound message in msgs
// (chronological) is a question older than 24h with no outbound message after
// it, and returns when it was asked.
func unansweredQuestionSince(msgs []model.Message, now time.Time) (time.Time, bool) {
	var latest *model.Message
	for i := range msgs {
		if msgs[i].Direction == model.DirectionInbound {
			latest = &msgs[i]
		}
	}
	if latest == nil || !looksLikeQuestion(latest.Body) {
		return time.Time{}, false
	}
	if now.Sub(latest.OccurredAt) <= questionMaxAge {
		return time.Time{}, false
	}
	for _, m := range msgs {
		if m.Direction == model.DirectionOutbound && m.OccurredAt.After(latest.OccurredAt) {
			return time.Time{}, false
		}
	}
	return latest.OccurredAt, true
}

// looksLikeQuestion reports whether a message body reads like a customer
// question: a question mark, or how-much / when / can-you phrasing.
func looksLikeQuestion(body string) bool {
	if strings.Contains(body, "?") {
		return true
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "how much") || strings.Contains(lower, "can you") {
		return true
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, w := range words {
		if w == "when" {
			return true
		}
	}
	return false
}

// humanAge renders a duration as "2 days", "1 day" or "13 hours".
func humanAge(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 1 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d hours", int(d.Hours()))
}

func severityRank(s string) int {
	switch s {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

func displayName(c model.Conversation) string {
	if c.Title != "" {
		return c.Title
	}
	if c.ExternalID != "" {
		return c.ExternalID
	}
	return c.ID
}
