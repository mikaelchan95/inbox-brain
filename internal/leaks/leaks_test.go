package leaks

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

var testNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func addConv(t *testing.T, s *store.Store, title string) model.Conversation {
	t.Helper()
	c, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_test",
		ConnectorID: "conn_test",
		Channel:     model.ChannelDemo,
		ExternalID:  model.NewID("ext"),
		Title:       title,
	})
	if err != nil {
		t.Fatalf("upsert conversation: %v", err)
	}
	return c
}

func classify(t *testing.T, s *store.Store, convID, label string, reviewed bool, override string) {
	t.Helper()
	if err := s.SaveConversationClassification(model.ConversationClassification{
		ConversationID:     convID,
		Classification:     label,
		BusinessConfidence: 50,
		Source:             model.SourceRules,
		ReviewedByUser:     reviewed,
		UserOverride:       override,
	}); err != nil {
		t.Fatalf("save classification: %v", err)
	}
}

func addAction(t *testing.T, s *store.Store, convID, typ, status string, createdAt time.Time) model.Action {
	t.Helper()
	a, err := s.CreateAction(model.Action{
		WorkspaceID:    "ws_test",
		ConversationID: convID,
		Type:           typ,
		Title:          typ,
		Status:         status,
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	})
	if err != nil {
		t.Fatalf("create action: %v", err)
	}
	return a
}

func addLead(t *testing.T, s *store.Store, convID, status string, createdAt time.Time) model.Lead {
	t.Helper()
	l, err := s.UpsertLead(model.Lead{
		WorkspaceID:    "ws_test",
		ConversationID: convID,
		ActionID:       model.NewID("act"),
		Status:         status,
		Summary:        "test lead",
		CreatedAt:      createdAt,
		UpdatedAt:      createdAt,
	})
	if err != nil {
		t.Fatalf("upsert lead: %v", err)
	}
	return l
}

func addMsg(t *testing.T, s *store.Store, convID, direction, body string, at time.Time) model.Message {
	t.Helper()
	m := model.Message{
		WorkspaceID:    "ws_test",
		ConversationID: convID,
		Channel:        model.ChannelDemo,
		Provider:       model.ProviderManualDemo,
		ConnectorID:    "conn_test",
		Body:           body,
		Direction:      direction,
		OccurredAt:     at,
		DedupeKey:      model.NewID("dk"),
	}
	if _, err := s.InsertMessage(m); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	return m
}

func TestDetectActionRules(t *testing.T) {
	cases := []struct {
		name         string
		typ          string
		age          time.Duration
		status       string
		label        string // "" = no classification row
		reviewed     bool
		override     string
		wantKind     string // "" = no leak expected
		wantSeverity string
	}{
		{name: "quote open 49h reviewed business leaks high", typ: model.ActionQuoteRequest, age: 49 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true,
			wantKind: "stale_quote", wantSeverity: "high"},
		{name: "quote open 47h does not leak", typ: model.ActionQuoteRequest, age: 47 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true},
		{name: "booking open 25h leaks high", typ: model.ActionBookingRequest, age: 25 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true,
			wantKind: "stale_booking", wantSeverity: "high"},
		{name: "booking open 23h does not leak", typ: model.ActionBookingRequest, age: 23 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true},
		{name: "complaint open 13h leaks high", typ: model.ActionComplaint, age: 13 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true,
			wantKind: "stale_complaint", wantSeverity: "high"},
		{name: "complaint open 11h does not leak", typ: model.ActionComplaint, age: 11 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true},
		{name: "payment open 25h leaks medium", typ: model.ActionPaymentIssue, age: 25 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true,
			wantKind: "payment_unresolved", wantSeverity: "medium"},
		{name: "payment open 23h does not leak", typ: model.ActionPaymentIssue, age: 23 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true},
		{name: "done quote never leaks", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusDone, label: model.ConvBusiness, reviewed: true},
		{name: "dismissed quote never leaks", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusDismissed, label: model.ConvBusiness, reviewed: true},
		{name: "snoozed quote never leaks", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusSnoozed, label: model.ConvBusiness, reviewed: true},
		{name: "personal conversation excluded even with old open action", typ: model.ActionQuoteRequest,
			age: 100 * time.Hour, status: model.StatusOpen, label: model.ConvPersonal, reviewed: true},
		{name: "unreviewed business label excluded", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: false},
		{name: "user override business includes despite personal label", typ: model.ActionQuoteRequest,
			age: 49 * time.Hour, status: model.StatusOpen, label: model.ConvPersonal,
			override: model.ConvBusiness, wantKind: "stale_quote", wantSeverity: "high"},
		{name: "user override personal excludes despite reviewed business label", typ: model.ActionQuoteRequest,
			age: 100 * time.Hour, status: model.StatusOpen, label: model.ConvBusiness, reviewed: true,
			override: model.ConvPersonal},
		{name: "mixed label included without review", typ: model.ActionQuoteRequest, age: 49 * time.Hour,
			status: model.StatusOpen, label: model.ConvMixed,
			wantKind: "stale_quote", wantSeverity: "high"},
		{name: "user override mixed includes", typ: model.ActionBookingRequest, age: 25 * time.Hour,
			status: model.StatusOpen, label: model.ConvUnknown, override: model.ConvMixed,
			wantKind: "stale_booking", wantSeverity: "high"},
		{name: "unknown unreviewed excluded", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusOpen, label: model.ConvUnknown},
		{name: "no classification row excluded", typ: model.ActionQuoteRequest, age: 100 * time.Hour,
			status: model.StatusOpen, label: ""},
		{name: "general_task never leaks", typ: model.ActionGeneralTask, age: 200 * time.Hour,
			status: model.StatusOpen, label: model.ConvBusiness, reviewed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			conv := addConv(t, s, "Alex")
			if tc.label != "" {
				classify(t, s, conv.ID, tc.label, tc.reviewed, tc.override)
			}
			act := addAction(t, s, conv.ID, tc.typ, tc.status, testNow.Add(-tc.age))

			got, err := Detect(s, testNow)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if tc.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("expected no leaks, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 leak, got %d: %+v", len(got), got)
			}
			l := got[0]
			if l.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", l.Kind, tc.wantKind)
			}
			if l.Severity != tc.wantSeverity {
				t.Errorf("Severity = %q, want %q", l.Severity, tc.wantSeverity)
			}
			if l.ConversationID != conv.ID {
				t.Errorf("ConversationID = %q, want %q", l.ConversationID, conv.ID)
			}
			if l.ConversationName != "Alex" {
				t.Errorf("ConversationName = %q, want %q", l.ConversationName, "Alex")
			}
			if l.ActionID != act.ID {
				t.Errorf("ActionID = %q, want %q", l.ActionID, act.ID)
			}
			if !l.Since.Equal(testNow.Add(-tc.age)) {
				t.Errorf("Since = %v, want %v", l.Since, testNow.Add(-tc.age))
			}
		})
	}
}

func TestDetectStaleLead(t *testing.T) {
	cases := []struct {
		name     string
		age      time.Duration
		status   string
		label    string
		reviewed bool
		want     bool
	}{
		{name: "open lead 73h in approved conversation leaks", age: 73 * time.Hour,
			status: model.LeadOpen, label: model.ConvBusiness, reviewed: true, want: true},
		{name: "open lead 71h does not leak", age: 71 * time.Hour,
			status: model.LeadOpen, label: model.ConvBusiness, reviewed: true, want: false},
		{name: "won lead never leaks", age: 200 * time.Hour,
			status: model.LeadWon, label: model.ConvBusiness, reviewed: true, want: false},
		{name: "lost lead never leaks", age: 200 * time.Hour,
			status: model.LeadLost, label: model.ConvBusiness, reviewed: true, want: false},
		{name: "open lead in personal conversation excluded", age: 200 * time.Hour,
			status: model.LeadOpen, label: model.ConvPersonal, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			conv := addConv(t, s, "Unknown +65 9123 4567")
			classify(t, s, conv.ID, tc.label, tc.reviewed, "")
			lead := addLead(t, s, conv.ID, tc.status, testNow.Add(-tc.age))

			got, err := Detect(s, testNow)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if !tc.want {
				if len(got) != 0 {
					t.Fatalf("expected no leaks, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 leak, got %d: %+v", len(got), got)
			}
			l := got[0]
			if l.Kind != "stale_lead" {
				t.Errorf("Kind = %q, want stale_lead", l.Kind)
			}
			if l.Severity != "medium" {
				t.Errorf("Severity = %q, want medium", l.Severity)
			}
			if l.ActionID != lead.ActionID {
				t.Errorf("ActionID = %q, want %q", l.ActionID, lead.ActionID)
			}
			if !l.Since.Equal(testNow.Add(-tc.age)) {
				t.Errorf("Since = %v, want %v", l.Since, testNow.Add(-tc.age))
			}
		})
	}
}

func TestDetectUnansweredQuestion(t *testing.T) {
	type msg struct {
		dir  string
		body string
		age  time.Duration
	}
	cases := []struct {
		name  string
		label string
		msgs  []msg
		want  bool
		since time.Duration // age of the question message when want
	}{
		{name: "question mark 25h no reply leaks",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "How much for a logo?", 25 * time.Hour}},
			want:  true, since: 25 * time.Hour},
		{name: "question with later outbound reply does not leak",
			label: model.ConvBusiness,
			msgs: []msg{
				{model.DirectionInbound, "How much for a logo?", 25 * time.Hour},
				{model.DirectionOutbound, "It is $500", 24 * time.Hour},
			},
			want: false},
		{name: "question only 23h old does not leak",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "How much for a logo?", 23 * time.Hour}},
			want:  false},
		{name: "personal conversation excluded",
			label: model.ConvPersonal,
			msgs:  []msg{{model.DirectionInbound, "How much for a logo?", 25 * time.Hour}},
			want:  false},
		{name: "latest inbound is not a question",
			label: model.ConvBusiness,
			msgs: []msg{
				{model.DirectionInbound, "How much for a logo?", 30 * time.Hour},
				{model.DirectionInbound, "thanks for the info", 25 * time.Hour},
			},
			want: false},
		{name: "can-you phrasing without question mark leaks",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "can you design my company logo", 25 * time.Hour}},
			want:  true, since: 25 * time.Hour},
		{name: "how-much phrasing leaks case-insensitively",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "How Much for a cleaning slot", 25 * time.Hour}},
			want:  true, since: 25 * time.Hour},
		{name: "when phrasing leaks",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "when are you free for the session", 25 * time.Hour}},
			want:  true, since: 25 * time.Hour},
		{name: "whenever does not count as when",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionInbound, "whenever works for me", 25 * time.Hour}},
			want:  false},
		{name: "outbound before the question still leaks",
			label: model.ConvBusiness,
			msgs: []msg{
				{model.DirectionOutbound, "Hi there", 30 * time.Hour},
				{model.DirectionInbound, "How much for a logo?", 25 * time.Hour},
			},
			want: true, since: 25 * time.Hour},
		{name: "no inbound messages no leak",
			label: model.ConvBusiness,
			msgs:  []msg{{model.DirectionOutbound, "Are you still interested?", 25 * time.Hour}},
			want:  false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			conv := addConv(t, s, "Mrs Tan")
			classify(t, s, conv.ID, tc.label, true, "")
			for _, m := range tc.msgs {
				addMsg(t, s, conv.ID, m.dir, m.body, testNow.Add(-m.age))
			}

			got, err := Detect(s, testNow)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if !tc.want {
				if len(got) != 0 {
					t.Fatalf("expected no leaks, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("expected 1 leak, got %d: %+v", len(got), got)
			}
			l := got[0]
			if l.Kind != "unanswered_question" {
				t.Errorf("Kind = %q, want unanswered_question", l.Kind)
			}
			if l.Severity != "medium" {
				t.Errorf("Severity = %q, want medium", l.Severity)
			}
			if l.ActionID != "" {
				t.Errorf("ActionID = %q, want empty", l.ActionID)
			}
			if !l.Since.Equal(testNow.Add(-tc.since)) {
				t.Errorf("Since = %v, want %v", l.Since, testNow.Add(-tc.since))
			}
		})
	}
}

func TestDetectDescriptions(t *testing.T) {
	s := newStore(t)
	alex := addConv(t, s, "Alex")
	classify(t, s, alex.ID, model.ConvBusiness, true, "")
	addAction(t, s, alex.ID, model.ActionQuoteRequest, model.StatusOpen, testNow.Add(-49*time.Hour))

	tan := addConv(t, s, "Mrs Tan")
	classify(t, s, tan.ID, model.ConvBusiness, true, "")
	addAction(t, s, tan.ID, model.ActionComplaint, model.StatusOpen, testNow.Add(-13*time.Hour))
	addMsg(t, s, tan.ID, model.DirectionInbound, "Can you do Saturday?", testNow.Add(-25*time.Hour))

	got, err := Detect(s, testNow)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	want := map[string]string{
		"stale_quote":         "Quote request from Alex open for 2 days",
		"stale_complaint":     "Complaint from Mrs Tan open for 13 hours",
		"unanswered_question": "Question from Mrs Tan unanswered for 1 day",
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d leaks, got %d: %+v", len(want), len(got), got)
	}
	for _, l := range got {
		if w, ok := want[l.Kind]; !ok {
			t.Errorf("unexpected leak kind %q", l.Kind)
		} else if l.Description != w {
			t.Errorf("Description for %s = %q, want %q", l.Kind, l.Description, w)
		}
	}
}

func TestDetectSortOrder(t *testing.T) {
	s := newStore(t)

	a := addConv(t, s, "A")
	classify(t, s, a.ID, model.ConvBusiness, true, "")
	addAction(t, s, a.ID, model.ActionPaymentIssue, model.StatusOpen, testNow.Add(-100*time.Hour)) // medium, oldest

	b := addConv(t, s, "B")
	classify(t, s, b.ID, model.ConvBusiness, true, "")
	addAction(t, s, b.ID, model.ActionComplaint, model.StatusOpen, testNow.Add(-13*time.Hour)) // high, newest

	c := addConv(t, s, "C")
	classify(t, s, c.ID, model.ConvBusiness, true, "")
	addAction(t, s, c.ID, model.ActionQuoteRequest, model.StatusOpen, testNow.Add(-60*time.Hour)) // high, older

	got, err := Detect(s, testNow)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	var kinds []string
	for _, l := range got {
		kinds = append(kinds, l.Kind)
	}
	want := []string{"stale_quote", "stale_complaint", "payment_unresolved"}
	if len(kinds) != len(want) {
		t.Fatalf("expected %d leaks, got %v", len(want), kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("order = %v, want %v", kinds, want)
		}
	}
}
