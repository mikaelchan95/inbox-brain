package store

import (
	"reflect"
	"testing"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

func TestConversationClassificationUpsert(t *testing.T) {
	s := newTestStore(t)
	first := model.ConversationClassification{
		ConversationID: "conv_1", Classification: model.ConvUnknown,
		BusinessConfidence: 55, Source: model.SourceRules, Reason: "some signals",
		CreatedAt: at(0), UpdatedAt: at(0),
	}
	if err := s.SaveConversationClassification(first); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.GetConversationClassification("conv_1")
	if err != nil || got == nil {
		t.Fatalf("get: (%v, %v)", got, err)
	}
	if got.Classification != model.ConvUnknown || got.BusinessConfidence != 55 ||
		got.Source != model.SourceRules || got.Reason != "some signals" ||
		got.ReviewedByUser || got.UserOverride != "" {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "CreatedAt", got.CreatedAt, at(0))
	wantTime(t, "UpdatedAt", got.UpdatedAt, at(0))
	firstID := got.ID

	// Upsert by conversation_id: created_at and id preserved, rest updated.
	second := model.ConversationClassification{
		ConversationID: "conv_1", Classification: model.ConvBusiness,
		BusinessConfidence: 90, Source: model.SourceRules, Reason: "mentions quote, invoice",
		CreatedAt: at(5 * time.Hour), UpdatedAt: at(5 * time.Hour),
	}
	if err := s.SaveConversationClassification(second); err != nil {
		t.Fatalf("save again: %v", err)
	}
	got, err = s.GetConversationClassification("conv_1")
	if err != nil || got == nil {
		t.Fatalf("get again: (%v, %v)", got, err)
	}
	if got.ID != firstID {
		t.Errorf("upsert created new row: %q != %q", got.ID, firstID)
	}
	if got.Classification != model.ConvBusiness || got.BusinessConfidence != 90 ||
		got.Reason != "mentions quote, invoice" {
		t.Errorf("fields not updated: %+v", got)
	}
	wantTime(t, "CreatedAt preserved", got.CreatedAt, at(0))
	wantTime(t, "UpdatedAt updated", got.UpdatedAt, at(5*time.Hour))

	list, err := s.ListConversationClassifications()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
}

func TestGetConversationClassificationAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetConversationClassification("conv_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSetUserOverride(t *testing.T) {
	s := newTestStore(t)
	err := s.SaveConversationClassification(model.ConversationClassification{
		ConversationID: "conv_1", Classification: model.ConvBusiness,
		BusinessConfidence: 70, Source: model.SourceRules, Reason: "quote terms",
		CreatedAt: at(0), UpdatedAt: at(0),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.SetUserOverride("conv_1", model.ConvPersonal); err != nil {
		t.Fatalf("SetUserOverride: %v", err)
	}
	got, err := s.GetConversationClassification("conv_1")
	if err != nil || got == nil {
		t.Fatalf("get: (%v, %v)", got, err)
	}
	if got.UserOverride != model.ConvPersonal {
		t.Errorf("UserOverride = %q, want personal", got.UserOverride)
	}
	if !got.ReviewedByUser {
		t.Error("ReviewedByUser not set")
	}
	if got.Source != model.SourceUserOverride {
		t.Errorf("Source = %q, want user_override", got.Source)
	}
	// Classifier verdict is kept alongside the override.
	if got.Classification != model.ConvBusiness || got.BusinessConfidence != 70 {
		t.Errorf("classifier label clobbered: %+v", got)
	}
	wantTime(t, "CreatedAt preserved", got.CreatedAt, at(0))
}

func TestSetUserOverrideWithoutClassificationRow(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetUserOverride("conv_fresh", model.ConvBusiness); err != nil {
		t.Fatalf("SetUserOverride: %v", err)
	}
	got, err := s.GetConversationClassification("conv_fresh")
	if err != nil || got == nil {
		t.Fatalf("get: (%v, %v)", got, err)
	}
	if got.UserOverride != model.ConvBusiness || !got.ReviewedByUser ||
		got.Source != model.SourceUserOverride {
		t.Errorf("override row incomplete: %+v", got)
	}
}

func TestMarkReviewed(t *testing.T) {
	s := newTestStore(t)
	err := s.SaveConversationClassification(model.ConversationClassification{
		ConversationID: "conv_1", Classification: model.ConvUnknown,
		BusinessConfidence: 50, Source: model.SourceRules,
		CreatedAt: at(0), UpdatedAt: at(0),
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.MarkReviewed("conv_1"); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}
	got, err := s.GetConversationClassification("conv_1")
	if err != nil || got == nil {
		t.Fatalf("get: (%v, %v)", got, err)
	}
	if !got.ReviewedByUser {
		t.Error("ReviewedByUser not set")
	}
	if got.Classification != model.ConvUnknown || got.UserOverride != "" ||
		got.Source != model.SourceRules {
		t.Errorf("MarkReviewed changed more than reviewed flag: %+v", got)
	}
}

func TestMessageClassificationUpsert(t *testing.T) {
	s := newTestStore(t)
	first := model.MessageClassification{
		MessageID: "msg_1", Classification: model.MsgAmbiguous,
		BusinessConfidence: 50, Reason: "unclear", Source: model.SourceRules,
		CreatedAt: at(0),
	}
	if err := s.SaveMessageClassification(first); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.GetMessageClassification("msg_1")
	if err != nil || got == nil {
		t.Fatalf("get: (%v, %v)", got, err)
	}
	if got.Classification != model.MsgAmbiguous || got.BusinessConfidence != 50 ||
		got.Reason != "unclear" || got.Source != model.SourceRules {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "CreatedAt", got.CreatedAt, at(0))
	firstID := got.ID

	second := model.MessageClassification{
		MessageID: "msg_1", Classification: model.MsgBusiness,
		BusinessConfidence: 80, Reason: "mentions rate", Source: model.SourceRules,
		CreatedAt: at(time.Hour),
	}
	if err := s.SaveMessageClassification(second); err != nil {
		t.Fatalf("save again: %v", err)
	}
	got, err = s.GetMessageClassification("msg_1")
	if err != nil || got == nil {
		t.Fatalf("get again: (%v, %v)", got, err)
	}
	if got.ID != firstID {
		t.Errorf("upsert created new row: %q != %q", got.ID, firstID)
	}
	if got.Classification != model.MsgBusiness || got.BusinessConfidence != 80 ||
		got.Reason != "mentions rate" {
		t.Errorf("fields not updated: %+v", got)
	}
	wantTime(t, "CreatedAt preserved", got.CreatedAt, at(0))
}

func TestGetMessageClassificationAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetMessageClassification("msg_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestRules(t *testing.T) {
	s := newTestStore(t)
	r1, err := s.AddRule(model.ClassificationRule{
		WorkspaceID: "ws_1", RuleType: model.RuleChatName,
		Pattern: "Family Group", Action: model.RuleAlwaysIgnore, CreatedAt: at(0),
	})
	if err != nil {
		t.Fatalf("AddRule: %v", err)
	}
	r2, err := s.AddRule(model.ClassificationRule{
		WorkspaceID: "ws_1", RuleType: model.RuleKeyword,
		Pattern: "invoice", Action: model.RuleAlwaysInclude, CreatedAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("AddRule 2: %v", err)
	}

	list, err := s.ListRules()
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	var ids []string
	for _, r := range list {
		ids = append(ids, r.ID)
	}
	if !reflect.DeepEqual(ids, []string{r1.ID, r2.ID}) {
		t.Fatalf("ids = %v, want [%s %s]", ids, r1.ID, r2.ID)
	}
	if list[0].RuleType != model.RuleChatName || list[0].Pattern != "Family Group" ||
		list[0].Action != model.RuleAlwaysIgnore {
		t.Errorf("round trip mismatch: %+v", list[0])
	}
	wantTime(t, "CreatedAt", list[0].CreatedAt, at(0))

	if err := s.DeleteRule(r1.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	list, err = s.ListRules()
	if err != nil {
		t.Fatalf("ListRules after delete: %v", err)
	}
	if len(list) != 1 || list[0].ID != r2.ID {
		t.Errorf("delete failed: %+v", list)
	}
}
