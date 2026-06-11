package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

func decodeJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode JSON: %v; body: %s", err, rec.Body.String())
	}
	return v
}

// ---------------------------------------------------------------------------
// GET /api/classification/conversations
// ---------------------------------------------------------------------------

func TestClassificationListAPI(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/classification/conversations")
	wantStatus(t, rec, http.StatusOK)
	items := decodeJSON[[]classificationItemJSON](t, rec)
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	buckets := map[string]string{}
	counts := map[string]int{}
	for _, it := range items {
		buckets[it.Conversation.ID] = it.Bucket
		counts[it.Conversation.ID] = it.MessageCount
		if it.Classification == nil {
			t.Errorf("conversation %s has nil classification", it.Conversation.ID)
		}
	}
	wantBuckets := map[string]string{
		f.mrsTan.ID:      bucketSuggested,
		f.mum.ID:         bucketIgnored,
		f.alex.ID:        bucketMixed,
		f.unknownLead.ID: bucketNeedsReview,
		f.referrals.ID:   bucketReviewed,
	}
	for id, want := range wantBuckets {
		if buckets[id] != want {
			t.Errorf("bucket[%s] = %q, want %q", id, buckets[id], want)
		}
	}
	if counts[f.alex.ID] != 2 {
		t.Errorf("messageCount[alex] = %d, want 2", counts[f.alex.ID])
	}
}

// ---------------------------------------------------------------------------
// POST /api/classification/conversations/{id}/{verb}
// ---------------------------------------------------------------------------

func TestReviewVerbsAPI(t *testing.T) {
	tests := []struct {
		name         string
		conv         func(*fixture) model.Conversation
		verb         string
		wantOverride string
		wantRule     string // "" = no rule expected
	}{
		{"approve business label keeps rules source", func(f *fixture) model.Conversation { return f.mrsTan }, "approve", "", ""},
		{"approve non-business label sets override", func(f *fixture) model.Conversation { return f.unknownLead }, "approve", model.ConvBusiness, ""},
		{"ignore", func(f *fixture) model.Conversation { return f.mrsTan }, "ignore", model.ConvPersonal, ""},
		{"mark-mixed", func(f *fixture) model.Conversation { return f.alex }, "mark-mixed", model.ConvMixed, ""},
		{"always-include", func(f *fixture) model.Conversation { return f.unknownLead }, "always-include", model.ConvBusiness, model.RuleAlwaysInclude},
		{"always-ignore", func(f *fixture) model.Conversation { return f.mum }, "always-ignore", model.ConvPersonal, model.RuleAlwaysIgnore},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			conv := tt.conv(f)
			rec := f.post("/api/classification/conversations/"+conv.ID+"/"+tt.verb, "")
			wantStatus(t, rec, http.StatusOK)
			body := decodeJSON[map[string]json.RawMessage](t, rec)
			if string(body["status"]) != `"ok"` {
				t.Errorf("status field = %s, want \"ok\"", body["status"])
			}

			cls, err := f.s.GetConversationClassification(conv.ID)
			if err != nil || cls == nil {
				t.Fatalf("classification: %v", err)
			}
			if !cls.ReviewedByUser {
				t.Error("conversation not marked reviewed")
			}
			if cls.UserOverride != tt.wantOverride {
				t.Errorf("user override = %q, want %q", cls.UserOverride, tt.wantOverride)
			}

			rules, err := f.s.ListRules()
			if err != nil {
				t.Fatalf("rules: %v", err)
			}
			if tt.wantRule == "" {
				if len(rules) != 0 {
					t.Errorf("got %d rules, want none", len(rules))
				}
			} else {
				if len(rules) != 1 {
					t.Fatalf("got %d rules, want 1", len(rules))
				}
				r := rules[0]
				if r.RuleType != model.RuleChatName || r.Pattern != conv.Title || r.Action != tt.wantRule {
					t.Errorf("rule = %s/%q/%s, want %s/%q/%s",
						r.RuleType, r.Pattern, r.Action, model.RuleChatName, conv.Title, tt.wantRule)
				}
			}

			events, err := f.s.ListAuditEvents(0)
			if err != nil {
				t.Fatalf("audit events: %v", err)
			}
			found := false
			for _, e := range events {
				if e.Subject == conv.ID && e.EventType == "user_override" {
					found = true
				}
			}
			if !found {
				t.Error("no audit event written for review action")
			}
		})
	}
}

func TestReviewVerbAPIErrors(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		path string
		want int
	}{
		{"/api/classification/conversations/conv_missing/approve", http.StatusNotFound},
		{"/api/classification/conversations/" + f.mrsTan.ID + "/bogus", http.StatusNotFound},
	}
	for _, tt := range tests {
		rec := f.post(tt.path, "")
		wantStatus(t, rec, tt.want)
		body := decodeJSON[map[string]string](t, rec)
		if body["error"] == "" {
			t.Errorf("%s: missing error field", tt.path)
		}
	}
}

// TestApproveUnclassified covers approving a conversation that was never
// classified: the override still takes effect.
func TestApproveUnclassified(t *testing.T) {
	f := newFixture(t)
	conv := f.addConv("Fresh Chat", "ext-fresh", time.Now())
	rec := f.post("/api/classification/conversations/"+conv.ID+"/approve", "")
	wantStatus(t, rec, http.StatusOK)
	cls, err := f.s.GetConversationClassification(conv.ID)
	if err != nil || cls == nil {
		t.Fatalf("classification: %v", err)
	}
	if cls.UserOverride != model.ConvBusiness || !cls.ReviewedByUser {
		t.Errorf("got override=%q reviewed=%v, want business/true", cls.UserOverride, cls.ReviewedByUser)
	}
}

// ---------------------------------------------------------------------------
// Ignore flow: snippets and search hits must disappear (spec §19, §25)
// ---------------------------------------------------------------------------

func TestIgnoreHidesSnippetsAndSearch(t *testing.T) {
	f := newFixture(t)

	// Before: the suggested card shows snippets and search finds the message.
	wantContains(t, f.get("/review").Body.String(), "Saturday trial class")
	res := decodeJSON[searchResponseJSON](t, f.get("/api/search?q=trial"))
	if len(res.Messages) != 1 {
		t.Fatalf("before ignore: got %d message hits, want 1", len(res.Messages))
	}

	wantStatus(t, f.post("/api/classification/conversations/"+f.mrsTan.ID+"/ignore", ""), http.StatusOK)

	cls, err := f.s.GetConversationClassification(f.mrsTan.ID)
	if err != nil || cls == nil {
		t.Fatalf("classification: %v", err)
	}
	if cls.UserOverride != model.ConvPersonal {
		t.Fatalf("override = %q, want personal", cls.UserOverride)
	}

	// After: card moves to Ignored as Personal and loses its snippets.
	html := f.get("/review").Body.String()
	wantContains(t, html, "Mrs Tan")
	wantNotContains(t, html, "Saturday trial class")

	// And the message no longer appears in default search.
	res = decodeJSON[searchResponseJSON](t, f.get("/api/search?q=trial"))
	if len(res.Messages) != 0 {
		t.Errorf("after ignore: got %d message hits, want 0", len(res.Messages))
	}
}

func TestSearchIncludeIgnoredConfig(t *testing.T) {
	f := newFixture(t)

	// Default config: personal chats are invisible to search.
	res := decodeJSON[searchResponseJSON](t, f.get("/api/search?q=grandma"))
	if len(res.Messages) != 0 {
		t.Fatalf("default search: got %d hits, want 0", len(res.Messages))
	}

	// With SearchIncludeIgnored the same query finds the personal message.
	cfg := *f.cfg
	cfg.SearchIncludeIgnored = true
	h := NewServer(f.s, &cfg)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/search?q=grandma", nil))
	wantStatus(t, rec, http.StatusOK)
	res = decodeJSON[searchResponseJSON](t, rec)
	if len(res.Messages) != 1 {
		t.Errorf("includeIgnored search: got %d hits, want 1", len(res.Messages))
	}
}

// ---------------------------------------------------------------------------
// POST /api/classification/messages/{id}/override
// ---------------------------------------------------------------------------

func TestMessageOverrideAPI(t *testing.T) {
	f := newFixture(t)

	rec := f.post("/api/classification/messages/"+f.msgAlexDinner.ID+"/override", `{"classification":"business"}`)
	wantStatus(t, rec, http.StatusOK)
	got := decodeJSON[messageClassificationJSON](t, rec)
	if got.Classification != model.MsgBusiness || got.Source != model.SourceUserOverride {
		t.Errorf("response = %s/%s, want business/user_override", got.Classification, got.Source)
	}
	mc, err := f.s.GetMessageClassification(f.msgAlexDinner.ID)
	if err != nil || mc == nil {
		t.Fatalf("message classification: %v", err)
	}
	if mc.Classification != model.MsgBusiness || mc.Source != model.SourceUserOverride {
		t.Errorf("stored = %s/%s, want business/user_override", mc.Classification, mc.Source)
	}

	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{"invalid label", "/api/classification/messages/" + f.msgAlexDinner.ID + "/override", `{"classification":"spam"}`, http.StatusBadRequest},
		{"invalid JSON", "/api/classification/messages/" + f.msgAlexDinner.ID + "/override", `{`, http.StatusBadRequest},
		{"missing message", "/api/classification/messages/msg_missing/override", `{"classification":"business"}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantStatus(t, f.post(tt.path, tt.body), tt.want)
		})
	}
}

// ---------------------------------------------------------------------------
// Actions API
// ---------------------------------------------------------------------------

func TestActionsListAPI(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		query string
		want  int
	}{
		{"", 3},
		{"?status=open", 2},
		{"?status=done", 1},
		{"?type=booking_request", 1},
		{"?status=open&type=quote_request", 1},
		{"?status=open&type=complaint", 0},
	}
	for _, tt := range tests {
		rec := f.get("/api/actions" + tt.query)
		wantStatus(t, rec, http.StatusOK)
		actions := decodeJSON[[]actionJSON](t, rec)
		if len(actions) != tt.want {
			t.Errorf("GET /api/actions%s: got %d, want %d", tt.query, len(actions), tt.want)
		}
	}
}

func TestActionVerbsAPI(t *testing.T) {
	f := newFixture(t)

	// done → reopen round-trip.
	rec := f.post("/api/actions/"+f.actBooking.ID+"/done", "")
	wantStatus(t, rec, http.StatusOK)
	if got := decodeJSON[actionJSON](t, rec); got.Status != model.StatusDone {
		t.Errorf("done: status = %q, want done", got.Status)
	}
	rec = f.post("/api/actions/"+f.actBooking.ID+"/reopen", "")
	wantStatus(t, rec, http.StatusOK)
	if got := decodeJSON[actionJSON](t, rec); got.Status != model.StatusOpen {
		t.Errorf("reopen: status = %q, want open", got.Status)
	}

	rec = f.post("/api/actions/"+f.actBooking.ID+"/dismiss", "")
	wantStatus(t, rec, http.StatusOK)
	if got := decodeJSON[actionJSON](t, rec); got.Status != model.StatusDismissed {
		t.Errorf("dismiss: status = %q, want dismissed", got.Status)
	}
}

func TestActionSnoozeAPI(t *testing.T) {
	f := newFixture(t)

	t.Run("explicit until", func(t *testing.T) {
		until := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Second)
		rec := f.post("/api/actions/"+f.actBooking.ID+"/snooze", `{"until":"`+until.Format(time.RFC3339)+`"}`)
		wantStatus(t, rec, http.StatusOK)
		got := decodeJSON[actionJSON](t, rec)
		if got.Status != model.StatusSnoozed {
			t.Errorf("status = %q, want snoozed", got.Status)
		}
		a, err := f.s.GetAction(f.actBooking.ID)
		if err != nil || a == nil {
			t.Fatalf("action: %v", err)
		}
		wantWithin(t, a.SnoozedUntil, until, time.Second)
	})

	t.Run("default 24h when body absent", func(t *testing.T) {
		rec := f.post("/api/actions/"+f.actStaleQuote.ID+"/snooze", "")
		wantStatus(t, rec, http.StatusOK)
		a, err := f.s.GetAction(f.actStaleQuote.ID)
		if err != nil || a == nil {
			t.Fatalf("action: %v", err)
		}
		if a.Status != model.StatusSnoozed {
			t.Errorf("status = %q, want snoozed", a.Status)
		}
		wantWithin(t, a.SnoozedUntil, time.Now().Add(24*time.Hour), time.Hour)
	})

	t.Run("invalid until", func(t *testing.T) {
		wantStatus(t, f.post("/api/actions/"+f.actBooking.ID+"/snooze", `{"until":"tomorrow"}`), http.StatusBadRequest)
	})
}

func TestActionVerbAPIErrors(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		path string
		want int
	}{
		{"/api/actions/act_missing/done", http.StatusNotFound},
		{"/api/actions/" + f.actBooking.ID + "/bogus", http.StatusNotFound},
	}
	for _, tt := range tests {
		wantStatus(t, f.post(tt.path, ""), tt.want)
	}
}

// ---------------------------------------------------------------------------
// Conversations API
// ---------------------------------------------------------------------------

func TestConversationsAPI(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/api/conversations")
	wantStatus(t, rec, http.StatusOK)
	if convs := decodeJSON[[]conversationJSON](t, rec); len(convs) != 5 {
		t.Errorf("got %d conversations, want 5", len(convs))
	}

	rec = f.get("/api/conversations/" + f.mrsTan.ID)
	wantStatus(t, rec, http.StatusOK)
	if conv := decodeJSON[conversationJSON](t, rec); conv.ID != f.mrsTan.ID || conv.Title != "Mrs Tan" {
		t.Errorf("got %s/%q, want %s/\"Mrs Tan\"", conv.ID, conv.Title, f.mrsTan.ID)
	}

	rec = f.get("/api/conversations/" + f.alex.ID + "/messages")
	wantStatus(t, rec, http.StatusOK)
	msgs := decodeJSON[[]messageJSON](t, rec)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if msgs[0].Body != "Dinner on Friday?" { // chronological
		t.Errorf("first message = %q, want the dinner one", msgs[0].Body)
	}

	rec = f.get("/api/conversations/" + f.mrsTan.ID + "/actions")
	wantStatus(t, rec, http.StatusOK)
	acts := decodeJSON[[]actionJSON](t, rec)
	if len(acts) != 1 || acts[0].ID != f.actBooking.ID {
		t.Errorf("got %d actions, want the booking action", len(acts))
	}

	for _, path := range []string{
		"/api/conversations/conv_missing",
		"/api/conversations/conv_missing/messages",
		"/api/conversations/conv_missing/actions",
	} {
		wantStatus(t, f.get(path), http.StatusNotFound)
	}
}

// ---------------------------------------------------------------------------
// Connectors API
// ---------------------------------------------------------------------------

func TestConnectorsAPI(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/api/connectors")
	wantStatus(t, rec, http.StatusOK)
	conns := decodeJSON[[]connectorJSON](t, rec)
	if len(conns) != 1 || conns[0].Name != "Demo Seed" || conns[0].Status != model.ConnectorActive {
		t.Errorf("connectors = %+v, want one active Demo Seed", conns)
	}

	rec = f.post("/api/connectors/"+f.conn.ID+"/sync", "")
	wantStatus(t, rec, http.StatusAccepted)
	if body := decodeJSON[map[string]string](t, rec); body["status"] != "sync requested" {
		t.Errorf("sync body = %v, want status=sync requested", body)
	}

	wantStatus(t, f.post("/api/connectors/conn_missing/sync", ""), http.StatusNotFound)
}

func TestWebhookStubs(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/connectors/telegram/webhook",
		"/api/connectors/wacli/webhook",
	} {
		rec := f.post(path, `{"update_id":1}`)
		wantStatus(t, rec, http.StatusNotImplemented)
		body := decodeJSON[map[string]string](t, rec)
		if !strings.Contains(body["error"], "webhook mode not available in v0.1") {
			t.Errorf("%s: error = %q, want webhook-unavailable message", path, body["error"])
		}
	}
}

// ---------------------------------------------------------------------------
// Search & leaks API
// ---------------------------------------------------------------------------

func TestSearchAPI(t *testing.T) {
	f := newFixture(t)

	rec := f.get("/api/search?q=quote")
	wantStatus(t, rec, http.StatusOK)
	res := decodeJSON[searchResponseJSON](t, rec)
	// Mixed chat + reviewed business chat both match; the unreviewed unknown
	// chat ("logo design") and personal chats are excluded.
	if len(res.Messages) != 2 {
		t.Errorf("got %d message hits, want 2", len(res.Messages))
	}
	if len(res.Actions) != 1 || res.Actions[0].ID != f.actStaleQuote.ID {
		t.Errorf("got %d action hits, want the stale quote action", len(res.Actions))
	}

	// Unreviewed unknown chats are excluded from default search (spec §19).
	res = decodeJSON[searchResponseJSON](t, f.get("/api/search?q=logo"))
	if len(res.Messages) != 0 {
		t.Errorf("unknown chat leaked into search: %d hits", len(res.Messages))
	}
	if len(res.Leads) != 1 { // lead summary "Logo design enquiry" still matches
		t.Errorf("got %d lead hits, want 1", len(res.Leads))
	}

	// Empty query returns empty, well-formed results.
	res = decodeJSON[searchResponseJSON](t, f.get("/api/search"))
	if len(res.Messages)+len(res.Actions)+len(res.Leads) != 0 {
		t.Errorf("empty query returned hits: %+v", res)
	}
}

func TestLeaksAPI(t *testing.T) {
	f := newFixture(t)
	rec := f.get("/api/leaks")
	wantStatus(t, rec, http.StatusOK)
	lks := decodeJSON[[]leakJSON](t, rec)
	if len(lks) != 1 {
		t.Fatalf("got %d leaks, want 1: %+v", len(lks), lks)
	}
	if lks[0].Kind != "stale_quote" || lks[0].ConversationName != "Design Referrals" {
		t.Errorf("leak = %s/%s, want stale_quote/Design Referrals", lks[0].Kind, lks[0].ConversationName)
	}
	if lks[0].ActionID != f.actStaleQuote.ID {
		t.Errorf("leak actionId = %q, want %q", lks[0].ActionID, f.actStaleQuote.ID)
	}
}
