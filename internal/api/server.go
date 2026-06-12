// Package api serves Inbox Brain's JSON API (spec §22) and the
// server-rendered dashboard (spec §11, §17). All templates and CSS are
// embedded; the dashboard needs no JavaScript — review and action buttons are
// plain HTML forms posting to fallback routes that redirect back.
package api

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/config"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

//go:embed templates/*.tmpl
var templateFS embed.FS

//go:embed static/style.css
var styleCSS []byte

// Review queue buckets (spec §10, §11).
const (
	bucketSuggested   = "suggested"
	bucketNeedsReview = "needs_review"
	bucketMixed       = "mixed"
	bucketIgnored     = "ignored"
	bucketReviewed    = "reviewed"
)

var reviewVerbs = map[string]bool{
	"approve":        true,
	"ignore":         true,
	"mark-mixed":     true,
	"always-include": true,
	"always-ignore":  true,
}

var actionVerbs = map[string]bool{
	"done":    true,
	"dismiss": true,
	"reopen":  true,
	"snooze":  true,
}

type server struct {
	store *store.Store
	cfg   *config.Config
	tmpl  map[string]*template.Template
}

// NewServer returns the HTTP handler serving both the JSON API and the
// dashboard, wired with Go 1.22+ ServeMux method+path patterns.
func NewServer(s *store.Store, cfg *config.Config) http.Handler {
	sv := &server{store: s, cfg: cfg, tmpl: parseTemplates()}
	mux := http.NewServeMux()

	// Dashboard.
	mux.HandleFunc("GET /{$}", sv.pageHome)
	mux.HandleFunc("GET /review", sv.pageReview)
	mux.HandleFunc("POST /review/{id}/{verb}", sv.formReview)
	mux.HandleFunc("GET /conversations", sv.pageConversations)
	mux.HandleFunc("GET /conversations/{id}", sv.pageConversation)
	mux.HandleFunc("GET /actions", sv.pageActions)
	mux.HandleFunc("POST /actions/{id}/{verb}", sv.formAction)
	mux.HandleFunc("GET /leaks", sv.pageLeaks)
	mux.HandleFunc("GET /search", sv.pageSearch)
	mux.HandleFunc("GET /static/style.css", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Write(styleCSS)
	})

	// JSON API (spec §22).
	mux.HandleFunc("GET /api/classification/conversations", sv.apiClassificationList)
	mux.HandleFunc("POST /api/classification/conversations/{id}/{verb}", sv.apiReviewVerb)
	mux.HandleFunc("POST /api/classification/messages/{id}/override", sv.apiMessageOverride)
	mux.HandleFunc("GET /api/actions", sv.apiActionsList)
	mux.HandleFunc("POST /api/actions/{id}/{verb}", sv.apiActionVerb)
	mux.HandleFunc("GET /api/conversations", sv.apiConversationsList)
	mux.HandleFunc("GET /api/conversations/{id}", sv.apiConversation)
	mux.HandleFunc("GET /api/conversations/{id}/messages", sv.apiConversationMessages)
	mux.HandleFunc("GET /api/conversations/{id}/actions", sv.apiConversationActions)
	mux.HandleFunc("GET /api/connectors", sv.apiConnectorsList)
	mux.HandleFunc("POST /api/connectors/{id}/sync", sv.apiConnectorSync)
	mux.HandleFunc("POST /api/connectors/telegram/webhook", sv.apiWebhookStub)
	mux.HandleFunc("POST /api/connectors/wacli/webhook", sv.apiWebhookStub)
	mux.HandleFunc("GET /api/search", sv.apiSearch)
	mux.HandleFunc("GET /api/leaks", sv.apiLeaks)

	return mux
}

// ---------------------------------------------------------------------------
// Shared classification logic
// ---------------------------------------------------------------------------

// effectiveLabel is the user override when set, otherwise the classifier
// label; unclassified conversations are unknown.
func effectiveLabel(c *model.ConversationClassification) string {
	if c == nil {
		return model.ConvUnknown
	}
	if c.UserOverride != "" {
		return c.UserOverride
	}
	return c.Classification
}

// bucketFor places a conversation in a review bucket: ignored (effective
// personal), mixed, suggested (label business, unreviewed, confidence >= 65),
// reviewed (already handled by the user), or needs_review (40–64 / unknown /
// never classified).
func bucketFor(c *model.ConversationClassification) string {
	if c == nil {
		return bucketNeedsReview
	}
	switch effectiveLabel(c) {
	case model.ConvPersonal:
		return bucketIgnored
	case model.ConvMixed:
		return bucketMixed
	}
	if c.ReviewedByUser {
		return bucketReviewed
	}
	if effectiveLabel(c) == model.ConvBusiness && c.BusinessConfidence >= model.ThresholdSuggest {
		return bucketSuggested
	}
	return bucketNeedsReview
}

// classifiedConv joins a conversation with its classification for the review
// queue and the classification API.
type classifiedConv struct {
	Conv         model.Conversation
	Cls          *model.ConversationClassification
	MessageCount int
	Bucket       string
}

func (sv *server) classifiedConversations() ([]classifiedConv, error) {
	convs, err := sv.store.ListConversations(store.ConversationFilter{})
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	out := make([]classifiedConv, 0, len(convs))
	for _, conv := range convs {
		cls, err := sv.store.GetConversationClassification(conv.ID)
		if err != nil {
			return nil, fmt.Errorf("load classification for %s: %w", conv.ID, err)
		}
		n, err := sv.store.CountMessages(conv.ID)
		if err != nil {
			return nil, fmt.Errorf("count messages for %s: %w", conv.ID, err)
		}
		out = append(out, classifiedConv{Conv: conv, Cls: cls, MessageCount: n, Bucket: bucketFor(cls)})
	}
	return out, nil
}

// applyReviewAction performs one review verb on a conversation and writes an
// audit event. It returns found=false when the conversation does not exist.
// Callers must validate verb against reviewVerbs first.
func (sv *server) applyReviewAction(conversationID, verb string) (found bool, err error) {
	conv, err := sv.store.GetConversation(conversationID)
	if err != nil {
		return false, err
	}
	if conv == nil {
		return false, nil
	}
	ws, err := sv.store.EnsureDefaultWorkspace()
	if err != nil {
		return true, fmt.Errorf("ensure workspace: %w", err)
	}
	switch verb {
	case "approve":
		err = sv.approveConversation(conversationID)
	case "ignore":
		err = sv.ignoreConversation(conversationID)
	case "mark-mixed":
		err = sv.store.SetUserOverride(conversationID, model.ConvMixed)
	case "always-include":
		if err = sv.approveConversation(conversationID); err == nil {
			_, err = sv.store.AddRule(model.ClassificationRule{
				WorkspaceID: ws.ID,
				RuleType:    model.RuleChatName,
				Pattern:     displayName(*conv),
				Action:      model.RuleAlwaysInclude,
			})
		}
	case "always-ignore":
		if err = sv.ignoreConversation(conversationID); err == nil {
			_, err = sv.store.AddRule(model.ClassificationRule{
				WorkspaceID: ws.ID,
				RuleType:    model.RuleChatName,
				Pattern:     displayName(*conv),
				Action:      model.RuleAlwaysIgnore,
			})
		}
	}
	if err != nil {
		return true, fmt.Errorf("review %s %s: %w", verb, conversationID, err)
	}
	if err := sv.store.AddAuditEvent(model.AuditEvent{
		WorkspaceID: ws.ID,
		EventType:   "user_override",
		Subject:     conversationID,
		Detail:      fmt.Sprintf("%s %q", verb, displayName(*conv)),
	}); err != nil {
		return true, fmt.Errorf("audit review %s %s: %w", verb, conversationID, err)
	}
	return true, nil
}

// ignoreConversation overrides a conversation to personal and purges its
// derived actions and leads (spec §24.5).
func (sv *server) ignoreConversation(conversationID string) error {
	if err := sv.store.SetUserOverride(conversationID, model.ConvPersonal); err != nil {
		return err
	}
	if _, err := sv.store.DeleteActionsForConversation(conversationID); err != nil {
		return err
	}
	_, err := sv.store.DeleteLeadsForConversation(conversationID)
	return err
}

// approveConversation marks a conversation reviewed and, when its classifier
// label is not already business, records a business user override.
func (sv *server) approveConversation(conversationID string) error {
	if err := sv.store.MarkReviewed(conversationID); err != nil {
		return err
	}
	cls, err := sv.store.GetConversationClassification(conversationID)
	if err != nil {
		return err
	}
	if cls == nil || cls.Classification != model.ConvBusiness {
		return sv.store.SetUserOverride(conversationID, model.ConvBusiness)
	}
	return nil
}

// applyActionVerb performs one action verb. until is only used by snooze.
// It returns found=false when the action does not exist. Callers must
// validate verb against actionVerbs first.
func (sv *server) applyActionVerb(id, verb string, until time.Time) (found bool, err error) {
	a, err := sv.store.GetAction(id)
	if err != nil {
		return false, err
	}
	if a == nil {
		return false, nil
	}
	switch verb {
	case "done":
		err = sv.store.UpdateActionStatus(id, model.StatusDone)
	case "dismiss":
		err = sv.store.UpdateActionStatus(id, model.StatusDismissed)
	case "reopen":
		err = sv.store.UpdateActionStatus(id, model.StatusOpen)
	case "snooze":
		err = sv.store.SnoozeAction(id, until)
	}
	if err != nil {
		return true, fmt.Errorf("action %s %s: %w", verb, id, err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func displayName(c model.Conversation) string {
	if strings.TrimSpace(c.Title) != "" {
		return c.Title
	}
	if c.ExternalID != "" {
		return c.ExternalID
	}
	return c.ID
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// redirectTarget returns the Referer's path+query when present (same-page
// redirect for form fallbacks), otherwise the fallback path. Only local
// absolute paths are accepted: a "//host" path would be treated as a
// scheme-relative URL by browsers (open redirect). Backslashes are already
// percent-encoded by RequestURI.
func redirectTarget(r *http.Request, fallback string) string {
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Path != "" {
			if p := u.RequestURI(); strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
				return p
			}
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Templates
// ---------------------------------------------------------------------------

var templateFuncs = template.FuncMap{
	"pct": func(f float64) int { return int(math.Round(f)) },
	"timefmt": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Local().Format("2 Jan 15:04")
	},
	"actionType": actionTypeLabel,
}

func actionTypeLabel(t string) string {
	switch t {
	case model.ActionNewLead:
		return "New Lead"
	case model.ActionBookingRequest:
		return "Booking Request"
	case model.ActionQuoteRequest:
		return "Quote Request"
	case model.ActionFollowUp:
		return "Follow-up"
	case model.ActionPaymentIssue:
		return "Payment Issue"
	case model.ActionComplaint:
		return "Complaint"
	case model.ActionUrgent:
		return "Urgent"
	case model.ActionGeneralTask:
		return "General Task"
	}
	return t
}

var dashboardPages = []string{"home", "review", "conversations", "conversation", "actions", "leaks", "search"}

func parseTemplates() map[string]*template.Template {
	m := make(map[string]*template.Template, len(dashboardPages))
	for _, p := range dashboardPages {
		m[p] = template.Must(template.New("layout.tmpl").Funcs(templateFuncs).
			ParseFS(templateFS, "templates/layout.tmpl", "templates/"+p+".tmpl"))
	}
	return m
}

func (sv *server) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := sv.tmpl[page].ExecuteTemplate(&buf, "layout.tmpl", data); err != nil {
		http.Error(w, "render "+page+": "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
