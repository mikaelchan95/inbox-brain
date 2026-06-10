package search

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// fixture seeds one business conversation, one personal conversation, two
// actions and two leads and returns the ids the tests assert against.
type fixture struct {
	s *store.Store

	bizConvID      string // "Mrs Tan", business reviewed
	personalConvID string // "Mum", personal

	quoteActionID   string // title mentions "Quote"
	bookingActionID string // summary mentions "trial"

	quoteLeadID   string // summary mentions "quote"
	tuitionLeadID string // summary mentions "tuition"
}

func seed(t *testing.T) fixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	f := fixture{s: s}
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	addConv := func(title, label string, reviewed bool) string {
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
		if err := s.SaveConversationClassification(model.ConversationClassification{
			ConversationID:     c.ID,
			Classification:     label,
			BusinessConfidence: 50,
			Source:             model.SourceRules,
			ReviewedByUser:     reviewed,
		}); err != nil {
			t.Fatalf("save classification: %v", err)
		}
		return c.ID
	}
	addMsg := func(convID, body string) {
		if _, err := s.InsertMessage(model.Message{
			WorkspaceID:    "ws_test",
			ConversationID: convID,
			Channel:        model.ChannelDemo,
			Provider:       model.ProviderManualDemo,
			ConnectorID:    "conn_test",
			Body:           body,
			Direction:      model.DirectionInbound,
			OccurredAt:     now,
			DedupeKey:      model.NewID("dk"),
		}); err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}

	f.bizConvID = addConv("Mrs Tan", model.ConvBusiness, true)
	addMsg(f.bizConvID, "Can I get a quote for the Saturday class?")

	f.personalConvID = addConv("Mum", model.ConvPersonal, false)
	addMsg(f.personalConvID, "that quote from the movie was funny")

	addAction := func(title, summary string) string {
		a, err := s.CreateAction(model.Action{
			WorkspaceID:    "ws_test",
			ConversationID: f.bizConvID,
			Type:           model.ActionQuoteRequest,
			Title:          title,
			Summary:        summary,
		})
		if err != nil {
			t.Fatalf("create action: %v", err)
		}
		return a.ID
	}
	f.quoteActionID = addAction("Quote request from Alex", "Landing page pricing")
	f.bookingActionID = addAction("Booking from Mrs Tan", "Saturday trial class")

	addLead := func(convID, summary string) string {
		l, err := s.UpsertLead(model.Lead{
			WorkspaceID:    "ws_test",
			ConversationID: convID,
			Status:         model.LeadOpen,
			Summary:        summary,
		})
		if err != nil {
			t.Fatalf("upsert lead: %v", err)
		}
		return l.ID
	}
	f.quoteLeadID = addLead(f.bizConvID, "Wants a quote for logo design")
	f.tuitionLeadID = addLead(f.personalConvID, "Interested in tuition")

	return f
}

func msgConvIDs(rs []store.SearchResult) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ConversationID)
	}
	return out
}

func actionIDs(as []model.Action) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.ID)
	}
	return out
}

func leadIDs(ls []model.Lead) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.ID)
	}
	return out
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", what, got, want)
		return
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	for _, g := range got {
		if !wantSet[g] {
			t.Errorf("%s = %v, want %v", what, got, want)
			return
		}
	}
}

func TestSearch(t *testing.T) {
	f := seed(t)
	cases := []struct {
		name           string
		q              string
		includeIgnored bool
		wantMsgConvs   []string
		wantActions    []string
		wantLeads      []string
	}{
		{name: "quote excludes personal chat by default", q: "quote",
			wantMsgConvs: []string{f.bizConvID},
			wantActions:  []string{f.quoteActionID},
			wantLeads:    []string{f.quoteLeadID}},
		{name: "quote with includeIgnored finds personal chat message", q: "quote", includeIgnored: true,
			wantMsgConvs: []string{f.bizConvID, f.personalConvID},
			wantActions:  []string{f.quoteActionID},
			wantLeads:    []string{f.quoteLeadID}},
		{name: "query is case-insensitive", q: "QUOTE",
			wantMsgConvs: []string{f.bizConvID},
			wantActions:  []string{f.quoteActionID},
			wantLeads:    []string{f.quoteLeadID}},
		{name: "action matches on summary", q: "trial",
			wantMsgConvs: nil,
			wantActions:  []string{f.bookingActionID},
			wantLeads:    nil},
		{name: "lead matches on summary", q: "tuition",
			wantMsgConvs: nil,
			wantActions:  nil,
			wantLeads:    []string{f.tuitionLeadID}},
		{name: "no matches anywhere", q: "zzzunmatchedzzz",
			wantMsgConvs: nil, wantActions: nil, wantLeads: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Search(f.s, tc.q, tc.includeIgnored)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			sameSet(t, "message conversations", msgConvIDs(got.Messages), tc.wantMsgConvs)
			sameSet(t, "actions", actionIDs(got.Actions), tc.wantActions)
			sameSet(t, "leads", leadIDs(got.Leads), tc.wantLeads)
		})
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	f := seed(t)
	got, err := Search(f.s, "", false)
	if err != nil {
		t.Fatalf("Search with empty q: %v", err)
	}
	if len(got.Messages) != 0 || len(got.Actions) != 0 || len(got.Leads) != 0 {
		t.Errorf("empty q should return empty Results, got %+v", got)
	}
}
