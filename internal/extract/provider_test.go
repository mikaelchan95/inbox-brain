package extract

import (
	"reflect"
	"testing"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

func TestValidateResult(t *testing.T) {
	cases := []struct {
		name string
		in   ExtractedAction
		want *ExtractedAction // nil = dropped
	}{
		{
			name: "unknown type dropped",
			in:   ExtractedAction{Type: "party_request", Title: "X"},
			want: nil,
		},
		{
			name: "empty type dropped",
			in:   ExtractedAction{Type: "", Title: "X"},
			want: nil,
		},
		{
			name: "no_action dropped",
			in:   ExtractedAction{Type: model.ActionNoAction, Title: "X"},
			want: nil,
		},
		{
			name: "confidence clamped low",
			in:   ExtractedAction{Type: model.ActionQuoteRequest, Title: "T", Confidence: -5},
			want: &ExtractedAction{Type: model.ActionQuoteRequest, Title: "T", Confidence: 0, Urgency: "normal"},
		},
		{
			name: "confidence clamped high",
			in:   ExtractedAction{Type: model.ActionQuoteRequest, Title: "T", Confidence: 150},
			want: &ExtractedAction{Type: model.ActionQuoteRequest, Title: "T", Confidence: 100, Urgency: "normal"},
		},
		{
			name: "empty title synthesized from type",
			in:   ExtractedAction{Type: model.ActionQuoteRequest, Title: "  ", Confidence: 80},
			want: &ExtractedAction{Type: model.ActionQuoteRequest, Title: "Quote request", Confidence: 80, Urgency: "normal"},
		},
		{
			name: "new_lead title synthesized",
			in:   ExtractedAction{Type: model.ActionNewLead, Confidence: 70},
			want: &ExtractedAction{Type: model.ActionNewLead, Title: "New lead", Confidence: 70, Urgency: "normal"},
		},
		{
			name: "empty urgency defaults to normal",
			in:   ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: ""},
			want: &ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: "normal"},
		},
		{
			name: "invalid urgency defaults to normal",
			in:   ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: "URGENT!!"},
			want: &ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: "normal"},
		},
		{
			name: "urgency case normalized",
			in:   ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: " HIGH "},
			want: &ExtractedAction{Type: model.ActionComplaint, Title: "T", Confidence: 50, Urgency: "high"},
		},
		{
			name: "urgency low kept",
			in:   ExtractedAction{Type: model.ActionFollowUp, Title: "T", Confidence: 50, Urgency: "low"},
			want: &ExtractedAction{Type: model.ActionFollowUp, Title: "T", Confidence: 50, Urgency: "low"},
		},
		{
			name: "valid action passes through",
			in: ExtractedAction{
				Type: model.ActionBookingRequest, Title: "Booking request from Mrs Tan",
				Summary: "Saturday trial class", SuggestedReply: "Hi!",
				Confidence: 94, Urgency: "normal", MessageExternalID: "m1",
			},
			want: &ExtractedAction{
				Type: model.ActionBookingRequest, Title: "Booking request from Mrs Tan",
				Summary: "Saturday trial class", SuggestedReply: "Hi!",
				Confidence: 94, Urgency: "normal", MessageExternalID: "m1",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidateResult(ProviderResult{Actions: []ExtractedAction{tc.in}})
			if tc.want == nil {
				if len(got.Actions) != 0 {
					t.Errorf("ValidateResult kept %+v, want dropped", got.Actions)
				}
				return
			}
			if len(got.Actions) != 1 {
				t.Fatalf("ValidateResult returned %d actions, want 1", len(got.Actions))
			}
			if !reflect.DeepEqual(got.Actions[0], *tc.want) {
				t.Errorf("ValidateResult = %+v, want %+v", got.Actions[0], *tc.want)
			}
		})
	}
}

func TestValidateResultKeepsOrderAndDropsInvalid(t *testing.T) {
	in := ProviderResult{Actions: []ExtractedAction{
		{Type: model.ActionQuoteRequest, Title: "first", Confidence: 80, Urgency: "normal"},
		{Type: "nonsense", Title: "dropped"},
		{Type: model.ActionNoAction, Title: "dropped too"},
		{Type: model.ActionComplaint, Title: "second", Confidence: 60, Urgency: "high"},
	}}
	got := ValidateResult(in)
	if len(got.Actions) != 2 {
		t.Fatalf("kept %d actions, want 2", len(got.Actions))
	}
	if got.Actions[0].Title != "first" || got.Actions[1].Title != "second" {
		t.Errorf("order not preserved: %+v", got.Actions)
	}
}

func TestValidateResultEmpty(t *testing.T) {
	got := ValidateResult(ProviderResult{})
	if len(got.Actions) != 0 {
		t.Errorf("ValidateResult(empty) = %+v, want no actions", got.Actions)
	}
}
