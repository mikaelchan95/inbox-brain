package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

func TestActionRoundTripAndStatus(t *testing.T) {
	s := newTestStore(t)
	in := model.Action{
		WorkspaceID: "ws_1", ConversationID: "conv_1", MessageID: "msg_1", CustomerID: "cust_1",
		Type: model.ActionQuoteRequest, Title: "Quote for landing page",
		Summary: "Alex asked for landing page pricing", SuggestedReply: "Hi Alex, ...",
		Confidence: 88, Urgency: "high", Source: "rules",
		CreatedAt: at(0), UpdatedAt: at(0),
	}
	created, err := s.CreateAction(in)
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	if created.ID == "" {
		t.Fatal("no id assigned")
	}
	if created.Status != model.StatusOpen {
		t.Errorf("default status = %q, want open", created.Status)
	}
	got, err := s.GetAction(created.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAction: (%v, %v)", got, err)
	}
	if got.Type != in.Type || got.Title != in.Title || got.Summary != in.Summary ||
		got.SuggestedReply != in.SuggestedReply || got.Confidence != in.Confidence ||
		got.Urgency != in.Urgency || got.Source != in.Source ||
		got.MessageID != in.MessageID || got.CustomerID != in.CustomerID {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "CreatedAt", got.CreatedAt, at(0))
	if !got.SnoozedUntil.IsZero() {
		t.Errorf("SnoozedUntil = %v, want zero", got.SnoozedUntil)
	}

	// Snooze sets status and snoozed_until.
	until := at(48 * time.Hour)
	if err := s.SnoozeAction(created.ID, until); err != nil {
		t.Fatalf("SnoozeAction: %v", err)
	}
	got, _ = s.GetAction(created.ID)
	if got.Status != model.StatusSnoozed {
		t.Errorf("status = %q, want snoozed", got.Status)
	}
	wantTime(t, "SnoozedUntil", got.SnoozedUntil, until)

	// Non-snoozed status clears snoozed_until.
	if err := s.UpdateActionStatus(created.ID, model.StatusDone); err != nil {
		t.Fatalf("UpdateActionStatus: %v", err)
	}
	got, _ = s.GetAction(created.ID)
	if got.Status != model.StatusDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if !got.SnoozedUntil.IsZero() {
		t.Errorf("SnoozedUntil = %v, want cleared", got.SnoozedUntil)
	}
}

func TestUpdateActionStatusSnoozedKeepsSnoozedUntil(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateAction(model.Action{
		WorkspaceID: "ws_1", ConversationID: "conv_1",
		Type: model.ActionFollowUp, Title: "Follow up",
		SnoozedUntil: at(24 * time.Hour), CreatedAt: at(0), UpdatedAt: at(0),
	})
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	if err := s.UpdateActionStatus(a.ID, model.StatusSnoozed); err != nil {
		t.Fatalf("UpdateActionStatus: %v", err)
	}
	got, _ := s.GetAction(a.ID)
	wantTime(t, "SnoozedUntil kept", got.SnoozedUntil, at(24*time.Hour))
}

func TestGetActionAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetAction("act_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestListActionsFilter(t *testing.T) {
	s := newTestStore(t)
	mk := func(typ, status, conv string, created time.Time) string {
		a, err := s.CreateAction(model.Action{
			WorkspaceID: "ws_1", ConversationID: conv, Type: typ, Title: typ,
			Status: status, CreatedAt: created, UpdatedAt: created,
		})
		if err != nil {
			t.Fatalf("CreateAction: %v", err)
		}
		return a.ID
	}
	openQuote := mk(model.ActionQuoteRequest, model.StatusOpen, "conv_1", at(1*time.Hour))
	doneQuote := mk(model.ActionQuoteRequest, model.StatusDone, "conv_1", at(2*time.Hour))
	openLead := mk(model.ActionNewLead, model.StatusOpen, "conv_2", at(3*time.Hour))
	openBooking := mk(model.ActionBookingRequest, model.StatusOpen, "conv_2", at(4*time.Hour))

	tests := []struct {
		name   string
		filter ActionFilter
		want   []string
	}{
		{"all newest first", ActionFilter{}, []string{openBooking, openLead, doneQuote, openQuote}},
		{"status open", ActionFilter{Status: model.StatusOpen}, []string{openBooking, openLead, openQuote}},
		{"status done", ActionFilter{Status: model.StatusDone}, []string{doneQuote}},
		{"type", ActionFilter{Type: model.ActionQuoteRequest}, []string{doneQuote, openQuote}},
		{"conversation", ActionFilter{ConversationID: "conv_2"}, []string{openBooking, openLead}},
		{"created after (strict)", ActionFilter{CreatedAfter: at(2 * time.Hour)}, []string{openBooking, openLead}},
		{"status and type", ActionFilter{Status: model.StatusOpen, Type: model.ActionQuoteRequest}, []string{openQuote}},
		{"status type conversation", ActionFilter{Status: model.StatusOpen, Type: model.ActionNewLead, ConversationID: "conv_2"}, []string{openLead}},
		{"limit", ActionFilter{Limit: 2}, []string{openBooking, openLead}},
		{"no match", ActionFilter{Status: model.StatusDismissed}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListActions(tt.filter)
			if err != nil {
				t.Fatalf("ListActions: %v", err)
			}
			var ids []string
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestActionExistsForMessage(t *testing.T) {
	s := newTestStore(t)
	exists, err := s.ActionExistsForMessage("msg_1")
	if err != nil {
		t.Fatalf("ActionExistsForMessage: %v", err)
	}
	if exists {
		t.Error("exists = true before any action")
	}
	if _, err := s.CreateAction(model.Action{
		WorkspaceID: "ws_1", ConversationID: "conv_1", MessageID: "msg_1",
		Type: model.ActionFollowUp, Title: "t",
	}); err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	exists, err = s.ActionExistsForMessage("msg_1")
	if err != nil {
		t.Fatalf("ActionExistsForMessage: %v", err)
	}
	if !exists {
		t.Error("exists = false after creating action")
	}
}

func TestDeleteActionsForConversation(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateAction(model.Action{
			WorkspaceID: "ws_1", ConversationID: "conv_del", Type: model.ActionGeneralTask, Title: "t",
		}); err != nil {
			t.Fatalf("CreateAction: %v", err)
		}
	}
	if _, err := s.CreateAction(model.Action{
		WorkspaceID: "ws_1", ConversationID: "conv_keep", Type: model.ActionGeneralTask, Title: "t",
	}); err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	n, err := s.DeleteActionsForConversation("conv_del")
	if err != nil {
		t.Fatalf("DeleteActionsForConversation: %v", err)
	}
	if n != 3 {
		t.Errorf("deleted = %d, want 3", n)
	}
	left, err := s.ListActions(ActionFilter{})
	if err != nil {
		t.Fatalf("ListActions: %v", err)
	}
	if len(left) != 1 || left[0].ConversationID != "conv_keep" {
		t.Errorf("remaining actions wrong: %+v", left)
	}
}

func TestLeadUpsertUniquePerConversation(t *testing.T) {
	s := newTestStore(t)
	first, err := s.UpsertLead(model.Lead{
		WorkspaceID: "ws_1", ConversationID: "conv_1", CustomerID: "cust_1",
		ActionID: "act_1", Status: model.LeadOpen, Summary: "asked about logo design",
		CreatedAt: at(0), UpdatedAt: at(0),
	})
	if err != nil {
		t.Fatalf("UpsertLead: %v", err)
	}
	second, err := s.UpsertLead(model.Lead{
		WorkspaceID: "ws_1", ConversationID: "conv_1", CustomerID: "cust_1",
		ActionID: "act_2", Status: model.LeadWon, Summary: "deal closed",
		CreatedAt: at(10 * time.Hour), UpdatedAt: at(10 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertLead again: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created new row: %q != %q", second.ID, first.ID)
	}
	wantTime(t, "CreatedAt earliest kept", second.CreatedAt, at(0))
	wantTime(t, "UpdatedAt", second.UpdatedAt, at(10*time.Hour))
	if second.Status != model.LeadWon || second.Summary != "deal closed" || second.ActionID != "act_2" {
		t.Errorf("fields not updated: %+v", second)
	}

	all, err := s.ListLeads("")
	if err != nil {
		t.Fatalf("ListLeads: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListLeads len = %d, want 1", len(all))
	}
}

func TestListLeadsByStatus(t *testing.T) {
	s := newTestStore(t)
	mk := func(conv, status string, created time.Time) string {
		l, err := s.UpsertLead(model.Lead{
			WorkspaceID: "ws_1", ConversationID: conv, Status: status,
			CreatedAt: created, UpdatedAt: created,
		})
		if err != nil {
			t.Fatalf("UpsertLead: %v", err)
		}
		return l.ID
	}
	open1 := mk("conv_1", model.LeadOpen, at(1*time.Hour))
	won := mk("conv_2", model.LeadWon, at(2*time.Hour))
	open2 := mk("conv_3", model.LeadOpen, at(3*time.Hour))

	got, err := s.ListLeads(model.LeadOpen)
	if err != nil {
		t.Fatalf("ListLeads open: %v", err)
	}
	var ids []string
	for _, l := range got {
		ids = append(ids, l.ID)
	}
	if !reflect.DeepEqual(ids, []string{open2, open1}) {
		t.Errorf("open ids = %v, want [%s %s]", ids, open2, open1)
	}
	all, err := s.ListLeads("")
	if err != nil {
		t.Fatalf("ListLeads all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all len = %d, want 3", len(all))
	}
	if all[0].ID != open2 || all[1].ID != won || all[2].ID != open1 {
		t.Errorf("all order wrong: %+v", all)
	}
}

func TestExtractionRuns(t *testing.T) {
	s := newTestStore(t)
	r, err := s.CreateExtractionRun(model.ExtractionRun{
		WorkspaceID: "ws_1", ConversationID: "conv_1", Provider: "rules",
		StartedAt: at(0),
	})
	if err != nil {
		t.Fatalf("CreateExtractionRun: %v", err)
	}
	if r.ID == "" || r.Status != model.RunPending {
		t.Fatalf("created run incomplete: %+v", r)
	}

	r.Status = model.RunSuccess
	r.Error = ""
	r.InputMessages = 7
	r.ActionsCreated = 2
	r.FinishedAt = at(time.Minute)
	if err := s.FinishExtractionRun(r); err != nil {
		t.Fatalf("FinishExtractionRun: %v", err)
	}

	_, err = s.CreateExtractionRun(model.ExtractionRun{
		WorkspaceID: "ws_1", ConversationID: "conv_2", Provider: "anthropic",
		Status: model.RunFailed, StartedAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateExtractionRun 2: %v", err)
	}

	runs, err := s.ListExtractionRuns(0)
	if err != nil {
		t.Fatalf("ListExtractionRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs len = %d, want 2", len(runs))
	}
	// Newest first.
	if runs[0].ConversationID != "conv_2" {
		t.Errorf("order wrong: %+v", runs)
	}
	got := runs[1]
	if got.ID != r.ID || got.Status != model.RunSuccess || got.InputMessages != 7 ||
		got.ActionsCreated != 2 || got.Provider != "rules" {
		t.Errorf("finished run mismatch: %+v", got)
	}
	wantTime(t, "StartedAt", got.StartedAt, at(0))
	wantTime(t, "FinishedAt", got.FinishedAt, at(time.Minute))

	limited, err := s.ListExtractionRuns(1)
	if err != nil {
		t.Fatalf("ListExtractionRuns limit: %v", err)
	}
	if len(limited) != 1 || limited[0].ConversationID != "conv_2" {
		t.Errorf("limited wrong: %+v", limited)
	}
}

func TestAuditEvents(t *testing.T) {
	s := newTestStore(t)
	events := []model.AuditEvent{
		{WorkspaceID: "ws_1", EventType: "classification_saved", Subject: "conv_1", Detail: "business 90", CreatedAt: at(1 * time.Hour)},
		{WorkspaceID: "ws_1", EventType: "user_override", Subject: "conv_1", Detail: "personal", CreatedAt: at(2 * time.Hour)},
		{WorkspaceID: "ws_1", EventType: "ai_context_sent", Subject: "conv_2", Detail: "3 messages to rules", CreatedAt: at(3 * time.Hour)},
	}
	for _, e := range events {
		if err := s.AddAuditEvent(e); err != nil {
			t.Fatalf("AddAuditEvent: %v", err)
		}
	}
	got, err := s.ListAuditEvents(0)
	if err != nil {
		t.Fatalf("ListAuditEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// Newest first.
	if got[0].EventType != "ai_context_sent" || got[2].EventType != "classification_saved" {
		t.Errorf("order wrong: %+v", got)
	}
	if got[1].Subject != "conv_1" || got[1].Detail != "personal" {
		t.Errorf("round trip mismatch: %+v", got[1])
	}
	wantTime(t, "CreatedAt", got[0].CreatedAt, at(3*time.Hour))

	limited, err := s.ListAuditEvents(2)
	if err != nil {
		t.Fatalf("ListAuditEvents limit: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("limited len = %d, want 2", len(limited))
	}
}
