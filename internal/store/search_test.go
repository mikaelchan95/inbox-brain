package store

import (
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// seedSearchConv creates a conversation with one message whose body contains
// the word "quote", plus an optional classification row, and returns the
// message id.
func seedSearchConv(t *testing.T, s *Store, ext, title string, cls *model.ConversationClassification, occurred time.Time) string {
	t.Helper()
	conv, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelWhatsApp,
		ExternalID: ext, Title: title, LastMessageAt: occurred,
	})
	if err != nil {
		t.Fatalf("UpsertConversation %s: %v", title, err)
	}
	msgID := model.NewID("msg")
	if _, err := s.InsertMessage(model.Message{
		ID: msgID, WorkspaceID: "ws_1", ConversationID: conv.ID,
		Channel: model.ChannelWhatsApp, Provider: model.ProviderWacli, ConnectorID: "conn_1",
		SenderName: "Sender " + ext, Body: "Can I get a quote for this work?",
		Direction: model.DirectionInbound, OccurredAt: occurred, IngestedAt: occurred,
		DedupeKey: "wacli:conn_1:" + ext + ":1",
	}); err != nil {
		t.Fatalf("InsertMessage %s: %v", title, err)
	}
	if cls != nil {
		cls.ConversationID = conv.ID
		if err := s.SaveConversationClassification(*cls); err != nil {
			t.Fatalf("SaveConversationClassification %s: %v", title, err)
		}
	}
	return msgID
}

func TestSearchMessagesInclusionMatrix(t *testing.T) {
	s := newTestStore(t)
	cls := func(label, override string, reviewed bool) *model.ConversationClassification {
		return &model.ConversationClassification{
			Classification: label, BusinessConfidence: 50, Source: model.SourceRules,
			UserOverride: override, ReviewedByUser: reviewed,
		}
	}
	msgs := map[string]string{
		"business":           seedSearchConv(t, s, "1", "Mrs Tan", cls(model.ConvBusiness, "", false), at(1*time.Hour)),
		"personal":           seedSearchConv(t, s, "2", "Mum", cls(model.ConvPersonal, "", false), at(2*time.Hour)),
		"mixed":              seedSearchConv(t, s, "3", "Alex", cls(model.ConvMixed, "", false), at(3*time.Hour)),
		"unknown-reviewed":   seedSearchConv(t, s, "4", "Maybe Biz", cls(model.ConvUnknown, "", true), at(4*time.Hour)),
		"unknown-unreviewed": seedSearchConv(t, s, "5", "New Number", cls(model.ConvUnknown, "", false), at(5*time.Hour)),
		"no-classification":  seedSearchConv(t, s, "6", "Unscanned", nil, at(6*time.Hour)),
		"override-personal":  seedSearchConv(t, s, "7", "Old Friend", cls(model.ConvBusiness, model.ConvPersonal, true), at(7*time.Hour)),
		"override-business":  seedSearchConv(t, s, "8", "School Mate", cls(model.ConvPersonal, model.ConvBusiness, true), at(8*time.Hour)),
	}

	tests := []struct {
		name           string
		includeIgnored bool
		wantConvs      []string
	}{
		{
			name:           "default excludes personal, unreviewed unknown and unclassified",
			includeIgnored: false,
			wantConvs:      []string{"business", "mixed", "unknown-reviewed", "override-business"},
		},
		{
			name:           "includeIgnored returns everything",
			includeIgnored: true,
			wantConvs: []string{"business", "personal", "mixed", "unknown-reviewed",
				"unknown-unreviewed", "no-classification", "override-personal", "override-business"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.SearchMessages("quote", tt.includeIgnored, 0)
			if err != nil {
				t.Fatalf("SearchMessages: %v", err)
			}
			gotIDs := make([]string, 0, len(results))
			for _, r := range results {
				gotIDs = append(gotIDs, r.MessageID)
			}
			wantIDs := make([]string, 0, len(tt.wantConvs))
			for _, k := range tt.wantConvs {
				wantIDs = append(wantIDs, msgs[k])
			}
			sort.Strings(gotIDs)
			sort.Strings(wantIDs)
			if len(gotIDs) != len(wantIDs) {
				t.Fatalf("got %d results, want %d (%v)", len(gotIDs), len(wantIDs), tt.wantConvs)
			}
			for i := range gotIDs {
				if gotIDs[i] != wantIDs[i] {
					t.Fatalf("message ids = %v, want %v", gotIDs, wantIDs)
				}
			}
		})
	}
}

func TestSearchMessagesNewestFirstAndLimit(t *testing.T) {
	s := newTestStore(t)
	old := seedSearchConv(t, s, "1", "A", &model.ConversationClassification{
		Classification: model.ConvBusiness, BusinessConfidence: 90, Source: model.SourceRules,
	}, at(1*time.Hour))
	recent := seedSearchConv(t, s, "2", "B", &model.ConversationClassification{
		Classification: model.ConvBusiness, BusinessConfidence: 90, Source: model.SourceRules,
	}, at(2*time.Hour))

	results, err := s.SearchMessages("quote", false, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 2 || results[0].MessageID != recent || results[1].MessageID != old {
		t.Fatalf("order wrong: %+v", results)
	}
	limited, err := s.SearchMessages("quote", false, 1)
	if err != nil {
		t.Fatalf("SearchMessages limit: %v", err)
	}
	if len(limited) != 1 || limited[0].MessageID != recent {
		t.Fatalf("limit wrong: %+v", limited)
	}
}

func TestSearchMessagesCaseInsensitiveAndTitleMatch(t *testing.T) {
	s := newTestStore(t)
	conv, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelDemo,
		ExternalID: "9", Title: "Invoice Reminders", LastMessageAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertConversation: %v", err)
	}
	if err := s.SaveConversationClassification(model.ConversationClassification{
		ConversationID: conv.ID, Classification: model.ConvBusiness,
		BusinessConfidence: 90, Source: model.SourceRules,
	}); err != nil {
		t.Fatalf("SaveConversationClassification: %v", err)
	}
	if _, err := s.InsertMessage(model.Message{
		WorkspaceID: "ws_1", ConversationID: conv.ID,
		Channel: model.ChannelDemo, Provider: model.ProviderManualDemo, ConnectorID: "conn_1",
		SenderName: "Pat", Body: "see you tomorrow",
		OccurredAt: at(time.Hour), IngestedAt: at(time.Hour),
		DedupeKey: "manual_demo:conn_1:9:1",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Body does not contain the query, the title does; query case differs.
	results, err := s.SearchMessages("INVOICE", false, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1 (title match)", len(results))
	}
	r := results[0]
	if r.ConversationName != "Invoice Reminders" || r.Channel != model.ChannelDemo ||
		r.SenderName != "Pat" || r.ConversationID != conv.ID {
		t.Errorf("result fields wrong: %+v", r)
	}
	if r.Snippet != "see you tomorrow" {
		t.Errorf("Snippet = %q, want full short body", r.Snippet)
	}
	wantTime(t, "OccurredAt", r.OccurredAt, at(time.Hour))

	// Lowercase body matched by uppercase query too.
	results, err = s.SearchMessages("TOMORROW", false, 0)
	if err != nil {
		t.Fatalf("SearchMessages body: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("body case-insensitive match failed: %+v", results)
	}
}

func TestSearchMessagesConversationNameFallback(t *testing.T) {
	s := newTestStore(t)
	// Conversation with no title: falls back to sender name, then external id.
	conv, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelWhatsApp,
		ExternalID: "+65 9123 4567", Title: "", LastMessageAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertConversation: %v", err)
	}
	if _, err := s.InsertMessage(model.Message{
		WorkspaceID: "ws_1", ConversationID: conv.ID,
		Channel: model.ChannelWhatsApp, Provider: model.ProviderWacli, ConnectorID: "conn_1",
		SenderName: "Unknown Caller", Body: "need a logo quote",
		OccurredAt: at(time.Hour), IngestedAt: at(time.Hour),
		DedupeKey: "wacli:conn_1:+65:1",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if _, err := s.InsertMessage(model.Message{
		WorkspaceID: "ws_1", ConversationID: conv.ID,
		Channel: model.ChannelWhatsApp, Provider: model.ProviderWacli, ConnectorID: "conn_1",
		SenderName: "", Body: "another quote question",
		OccurredAt: at(2 * time.Hour), IngestedAt: at(2 * time.Hour),
		DedupeKey: "wacli:conn_1:+65:2",
	}); err != nil {
		t.Fatalf("InsertMessage 2: %v", err)
	}

	results, err := s.SearchMessages("quote", true, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	// Newest first: no sender name → external id; older has sender name.
	if results[0].ConversationName != "+65 9123 4567" {
		t.Errorf("fallback to external id failed: %q", results[0].ConversationName)
	}
	if results[1].ConversationName != "Unknown Caller" {
		t.Errorf("fallback to sender name failed: %q", results[1].ConversationName)
	}
}

func TestSearchMessagesSnippetWindow(t *testing.T) {
	s := newTestStore(t)
	conv, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelDemo,
		ExternalID: "long", Title: "Long Chat", LastMessageAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertConversation: %v", err)
	}
	body := strings.Repeat("a", 200) + " NEEDLE " + strings.Repeat("b", 200)
	if _, err := s.InsertMessage(model.Message{
		WorkspaceID: "ws_1", ConversationID: conv.ID,
		Channel: model.ChannelDemo, Provider: model.ProviderManualDemo, ConnectorID: "conn_1",
		Body: body, OccurredAt: at(time.Hour), IngestedAt: at(time.Hour),
		DedupeKey: "manual_demo:conn_1:long:1",
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	results, err := s.SearchMessages("needle", true, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	snip := results[0].Snippet
	if !strings.Contains(snip, "NEEDLE") {
		t.Errorf("snippet does not contain match: %q", snip)
	}
	if n := utf8.RuneCountInString(snip); n > snippetWindow {
		t.Errorf("snippet length = %d, want <= %d", n, snippetWindow)
	}
	// Context on both sides of the match.
	if !strings.Contains(snip, "a NEEDLE b") {
		t.Errorf("snippet not centered on match: %q", snip)
	}
}

func TestSnippet(t *testing.T) {
	long := strings.Repeat("x", 300)
	tests := []struct {
		name string
		body string
		q    string
		want func(t *testing.T, got string)
	}{
		{
			name: "short body returned whole",
			body: "a short message",
			q:    "short",
			want: func(t *testing.T, got string) {
				if got != "a short message" {
					t.Errorf("got %q", got)
				}
			},
		},
		{
			name: "match at start keeps window from start",
			body: "needle " + long,
			q:    "needle",
			want: func(t *testing.T, got string) {
				if !strings.HasPrefix(got, "needle ") {
					t.Errorf("got %q", got)
				}
				if utf8.RuneCountInString(got) != snippetWindow {
					t.Errorf("len = %d", utf8.RuneCountInString(got))
				}
			},
		},
		{
			name: "match at end keeps window to end",
			body: long + " needle",
			q:    "needle",
			want: func(t *testing.T, got string) {
				if !strings.HasSuffix(got, " needle") {
					t.Errorf("got %q", got)
				}
				if utf8.RuneCountInString(got) != snippetWindow {
					t.Errorf("len = %d", utf8.RuneCountInString(got))
				}
			},
		},
		{
			name: "no match in body returns leading window",
			body: long,
			q:    "absent",
			want: func(t *testing.T, got string) {
				if got != strings.Repeat("x", snippetWindow) {
					t.Errorf("got %q", got)
				}
			},
		},
		{
			name: "unicode body sliced on rune boundaries",
			body: strings.Repeat("好", 150) + "報價" + strings.Repeat("好", 150),
			q:    "報價",
			want: func(t *testing.T, got string) {
				if !strings.Contains(got, "報價") {
					t.Errorf("got %q", got)
				}
				if !utf8.ValidString(got) {
					t.Errorf("invalid utf8: %q", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want(t, snippet(tt.body, tt.q))
		})
	}
}
