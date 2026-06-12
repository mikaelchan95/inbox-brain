package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/config"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// personalMarker is a string that only ever appears in a personal chat; it
// must never leak into the home page or default search.
const personalMarker = "Secret dinner surprise for grandma"

// fixture seeds one temp store with the spec §26 cast:
//
//	Mrs Tan       — business 94, unreviewed        → suggested
//	Mum           — personal 5                      → ignored (holds personalMarker)
//	Alex          — mixed 68 (dinner + quote msgs)  → mixed
//	Unknown +65…  — unknown 50                      → needs_review
//	Design Refs   — business 91, reviewed           → approved (stale quote leak)
type fixture struct {
	t   *testing.T
	s   *store.Store
	h   http.Handler
	cfg *config.Config

	ws   model.Workspace
	conn model.Connector

	mrsTan, mum, alex, unknownLead, referrals model.Conversation

	msgTrial, msgMum, msgAlexDinner, msgAlexQuote, msgLogo, msgReferrals model.Message

	actBooking, actStaleQuote, actDone model.Action
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	f := &fixture{t: t, s: s, cfg: config.Default(t.TempDir())}
	f.h = NewServer(s, f.cfg)

	f.ws, err = s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	f.conn, err = s.UpsertConnector(model.Connector{
		WorkspaceID: f.ws.ID,
		Channel:     model.ChannelDemo,
		Provider:    model.ProviderManualDemo,
		Name:        "Demo Seed",
		Status:      model.ConnectorActive,
	})
	if err != nil {
		t.Fatalf("connector: %v", err)
	}

	now := time.Now()

	f.mrsTan = f.addConv("Mrs Tan", "ext-mrstan", now.Add(-2*time.Hour))
	f.msgTrial = f.addMsg(f.mrsTan, "Mrs Tan", "Can I book a Saturday trial class for my son?", now.Add(-2*time.Hour))
	f.classify(f.mrsTan, model.ConvBusiness, 94, "Mentions trial class, pricing, Saturday availability", false)

	f.mum = f.addConv("Mum", "ext-mum", now.Add(-3*time.Hour))
	f.msgMum = f.addMsg(f.mum, "Mum", personalMarker, now.Add(-3*time.Hour))
	f.classify(f.mum, model.ConvPersonal, 5, "Family contact, personal chatter", false)

	f.alex = f.addConv("Alex", "ext-alex", now.Add(-4*time.Hour))
	f.msgAlexDinner = f.addMsg(f.alex, "Alex", "Dinner on Friday?", now.Add(-5*time.Hour))
	f.msgAlexQuote = f.addMsg(f.alex, "Alex", "Can you quote me for a landing page?", now.Add(-4*time.Hour))
	f.classify(f.alex, model.ConvMixed, 68, "Mentions quote and landing page; also dinner plans", false)
	f.classifyMsg(f.msgAlexDinner, model.MsgPersonal, 10)
	f.classifyMsg(f.msgAlexQuote, model.MsgBusiness, 80)

	f.unknownLead = f.addConv("Unknown +65 9123 4567", "ext-unknown", now.Add(-6*time.Hour))
	f.msgLogo = f.addMsg(f.unknownLead, "Unknown", "Are you available for logo design?", now.Add(-6*time.Hour))
	f.classify(f.unknownLead, model.ConvUnknown, 50, "Some business words, little context", false)

	f.referrals = f.addConv("Design Referrals", "ext-referrals", now.Add(-72*time.Hour))
	f.msgReferrals = f.addMsg(f.referrals, "Janet", "Need a quote for brochure design", now.Add(-72*time.Hour))
	f.classify(f.referrals, model.ConvBusiness, 91, "Business referral group", true)

	f.actBooking = f.addAction(model.Action{
		ConversationID: f.mrsTan.ID,
		MessageID:      f.msgTrial.ID,
		Type:           model.ActionBookingRequest,
		Title:          "Booking request from Mrs Tan",
		Summary:        "Asked for a Saturday trial class.",
		SuggestedReply: "Hi Mrs Tan, yes - Saturday 10am is available for a trial class.",
		Status:         model.StatusOpen,
		CreatedAt:      now.Add(-2 * time.Hour),
	})
	f.actStaleQuote = f.addAction(model.Action{
		ConversationID: f.referrals.ID,
		MessageID:      f.msgReferrals.ID,
		Type:           model.ActionQuoteRequest,
		Title:          "Quote request from Design Referrals",
		Summary:        "Brochure design quote requested.",
		Status:         model.StatusOpen,
		CreatedAt:      now.Add(-72 * time.Hour),
	})
	f.actDone = f.addAction(model.Action{
		ConversationID: f.referrals.ID,
		Type:           model.ActionFollowUp,
		Title:          "Follow up with old client",
		Status:         model.StatusDone,
	})

	if _, err := s.UpsertLead(model.Lead{
		WorkspaceID:    f.ws.ID,
		ConversationID: f.unknownLead.ID,
		Status:         model.LeadOpen,
		Summary:        "Logo design enquiry",
	}); err != nil {
		t.Fatalf("lead: %v", err)
	}
	return f
}

func (f *fixture) addConv(title, externalID string, last time.Time) model.Conversation {
	f.t.Helper()
	c, err := f.s.UpsertConversation(model.Conversation{
		WorkspaceID:   f.ws.ID,
		ConnectorID:   f.conn.ID,
		Channel:       model.ChannelDemo,
		ExternalID:    externalID,
		Title:         title,
		LastMessageAt: last,
	})
	if err != nil {
		f.t.Fatalf("conversation %s: %v", title, err)
	}
	return c
}

func (f *fixture) addMsg(conv model.Conversation, sender, body string, at time.Time) model.Message {
	f.t.Helper()
	m := model.Message{
		ID:             model.NewID("msg"),
		WorkspaceID:    f.ws.ID,
		ConversationID: conv.ID,
		Channel:        model.ChannelDemo,
		Provider:       model.ProviderManualDemo,
		ConnectorID:    f.conn.ID,
		SenderName:     sender,
		Body:           body,
		Direction:      model.DirectionInbound,
		OccurredAt:     at,
		DedupeKey:      model.NewID("dedupe"),
	}
	if _, err := f.s.InsertMessage(m); err != nil {
		f.t.Fatalf("message %q: %v", body, err)
	}
	return m
}

func (f *fixture) classify(conv model.Conversation, label string, confidence float64, reason string, reviewed bool) {
	f.t.Helper()
	if err := f.s.SaveConversationClassification(model.ConversationClassification{
		ConversationID:     conv.ID,
		Classification:     label,
		BusinessConfidence: confidence,
		Source:             model.SourceRules,
		Reason:             reason,
		ReviewedByUser:     reviewed,
	}); err != nil {
		f.t.Fatalf("classify %s: %v", conv.Title, err)
	}
}

func (f *fixture) classifyMsg(m model.Message, label string, confidence float64) {
	f.t.Helper()
	if err := f.s.SaveMessageClassification(model.MessageClassification{
		MessageID:          m.ID,
		Classification:     label,
		BusinessConfidence: confidence,
		Source:             model.SourceRules,
	}); err != nil {
		f.t.Fatalf("classify message: %v", err)
	}
}

func (f *fixture) addAction(a model.Action) model.Action {
	f.t.Helper()
	a.WorkspaceID = f.ws.ID
	created, err := f.s.CreateAction(a)
	if err != nil {
		f.t.Fatalf("action %s: %v", a.Title, err)
	}
	return created
}

func (f *fixture) get(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func (f *fixture) post(path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(http.MethodPost, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func wantContains(t *testing.T, html string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(html, sub) {
			t.Errorf("response does not contain %q", sub)
		}
	}
}

func wantNotContains(t *testing.T, html string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(html, sub) {
			t.Errorf("response must not contain %q", sub)
		}
	}
}

// ---------------------------------------------------------------------------
// Dashboard pages
// ---------------------------------------------------------------------------

func TestHomePage(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/")
	wantStatus(t, rec, http.StatusOK)
	html := rec.Body.String()
	wantContains(t, html,
		"Today's Actions",
		"Booking Requests",
		"Booking request from Mrs Tan",
		"Quote Requests",
		"Quote request from Design Referrals",
		"Hi Mrs Tan, yes - Saturday 10am is available", // suggested reply textarea
		"<textarea class=\"reply\" readonly",
		"Revenue Leaks",
		"Connector Health",
		"Demo Seed",
	)
	// Done actions are not "today's actions".
	wantNotContains(t, html, "Follow up with old client")
	// Privacy: personal conversation bodies never appear on the home page.
	wantNotContains(t, html, personalMarker)
}

func TestReviewPage(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/review")
	wantStatus(t, rec, http.StatusOK)
	html := rec.Body.String()
	wantContains(t, html,
		"Suggested Business Chats",
		"Needs Review",
		"Mixed Chats",
		"Ignored as Personal",
		"Mrs Tan",
		"Saturday trial class", // snippet for a suggested chat
		"Mum",                  // ignored card is listed...
		"Alex",
		"landing page",
		"Unknown &#43;65 9123 4567", // html/template escapes the "+"
		"/review/"+f.mrsTan.ID+"/approve",
		"/review/"+f.mrsTan.ID+"/always-ignore",
	)
	// ...but ignored/personal cards never show snippets.
	wantNotContains(t, html, personalMarker)
	// Reviewed conversations are no longer part of the review queue.
	wantNotContains(t, html, "Design Referrals")
}

func TestConversationPages(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/conversations")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec.Body.String(), "Mrs Tan", "Mum", "Alex", "Design Referrals")

	rec = f.get("/conversations/" + f.alex.ID)
	wantStatus(t, rec, http.StatusOK)
	html := rec.Body.String()
	// Mixed chat: thread plus per-message classification chips.
	wantContains(t, html,
		"Dinner on Friday?",
		"Can you quote me for a landing page?",
		"chip-personal",
		"chip-business",
	)

	rec = f.get("/conversations/conv_missing")
	wantStatus(t, rec, http.StatusNotFound)
}

func TestActionsPage(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/actions")
	wantStatus(t, rec, http.StatusOK)
	html := rec.Body.String()
	wantContains(t, html,
		"Booking request from Mrs Tan",
		"Follow up with old client",
		"/actions/"+f.actBooking.ID+"/done",
		"/actions/"+f.actBooking.ID+"/snooze",
		"/actions/"+f.actDone.ID+"/reopen",
	)
}

func TestLeaksPage(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/leaks")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec.Body.String(), "Design Referrals", "Quote request from Design Referrals")
}

func TestSearchPage(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/search?q=quote")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec.Body.String(),
		"landing page",                        // message hit in the mixed chat
		"Quote request from Design Referrals", // action hit
	)

	// Ignored/personal chats stay out of default search.
	rec = f.get("/search?q=grandma")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec.Body.String(), personalMarker)
}

// ---------------------------------------------------------------------------
// Form fallbacks (no-JS dashboard)
// ---------------------------------------------------------------------------

func TestReviewFormFallback(t *testing.T) {
	f := newFixture(t)

	rec := f.post("/review/"+f.mrsTan.ID+"/approve", "")
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/review" {
		t.Fatalf("Location = %q, want /review", loc)
	}
	cls, err := f.s.GetConversationClassification(f.mrsTan.ID)
	if err != nil || cls == nil {
		t.Fatalf("classification: %v", err)
	}
	if !cls.ReviewedByUser {
		t.Error("approve via form did not mark the conversation reviewed")
	}

	wantStatus(t, f.post("/review/"+f.mrsTan.ID+"/bogus", ""), http.StatusNotFound)
	wantStatus(t, f.post("/review/conv_missing/approve", ""), http.StatusNotFound)
}

func TestReviewApproveRedirectsBackToReferer(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/review/"+f.mrsTan.ID+"/approve", strings.NewReader(""))
	req.Header.Set("Referer", "http://localhost/conversations/"+f.mrsTan.ID)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc, want := rec.Header().Get("Location"), "/conversations/"+f.mrsTan.ID; loc != want {
		t.Fatalf("Location = %q, want %q", loc, want)
	}
}

// Ignoring a chat from its own page must not redirect back to it: the page
// would re-render the personal messages just hidden (spec §25).
func TestReviewIgnoreAlwaysRedirectsToReview(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/review/"+f.mrsTan.ID+"/ignore", strings.NewReader(""))
	req.Header.Set("Referer", "http://localhost/conversations/"+f.mrsTan.ID)
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/review" {
		t.Fatalf("Location = %q, want /review", loc)
	}
}

func TestRedirectTargetRejectsSchemeRelativePaths(t *testing.T) {
	for ref, want := range map[string]string{
		"http://localhost/conversations/c1": "/conversations/c1",
		"https://evil.com//phish.example/x": "/fallback",
	} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Referer", ref)
		if got := redirectTarget(req, "/fallback"); got != want {
			t.Errorf("redirectTarget(Referer=%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestActionFormFallbacks(t *testing.T) {
	f := newFixture(t)

	rec := f.post("/actions/"+f.actBooking.ID+"/done", "")
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/actions" {
		t.Fatalf("Location = %q, want /actions", loc)
	}
	a, err := f.s.GetAction(f.actBooking.ID)
	if err != nil || a == nil {
		t.Fatalf("action: %v", err)
	}
	if a.Status != model.StatusDone {
		t.Errorf("status = %q, want done", a.Status)
	}

	// Snooze via form defaults to ~24h and redirects back to the Referer.
	req := httptest.NewRequest(http.MethodPost, "/actions/"+f.actStaleQuote.ID+"/snooze", strings.NewReader(""))
	req.Header.Set("Referer", "http://localhost/")
	rec = httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Fatalf("Location = %q, want /", loc)
	}
	a, err = f.s.GetAction(f.actStaleQuote.ID)
	if err != nil || a == nil {
		t.Fatalf("action: %v", err)
	}
	if a.Status != model.StatusSnoozed {
		t.Errorf("status = %q, want snoozed", a.Status)
	}
	wantWithin(t, a.SnoozedUntil, time.Now().Add(24*time.Hour), time.Hour)

	wantStatus(t, f.post("/actions/"+f.actBooking.ID+"/bogus", ""), http.StatusNotFound)
	wantStatus(t, f.post("/actions/act_missing/done", ""), http.StatusNotFound)
}

func wantWithin(t *testing.T, got, want time.Time, tolerance time.Duration) {
	t.Helper()
	diff := got.Sub(want)
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		t.Errorf("time = %v, want within %v of %v", got, tolerance, want)
	}
}

func TestStylesheet(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/static/style.css")
	wantStatus(t, rec, http.StatusOK)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	wantContains(t, rec.Body.String(), "#4f46e5")
}
