package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

func inboundMsg(sender, body, ext string) model.Message {
	return model.Message{
		SenderName:        sender,
		Body:              body,
		Direction:         model.DirectionInbound,
		MessageExternalID: ext,
	}
}

func rulesInput(msgs ...model.Message) ProviderInput {
	return ProviderInput{
		Profile:      testProfile,
		Conversation: model.Conversation{Title: "Chat"},
		Messages:     msgs,
	}
}

func TestRulesProviderDetection(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantType    string
		wantUrgency string
	}{
		{"booking slot", "Can I book a slot on Saturday?", model.ActionBookingRequest, "normal"},
		{"appointment", "I'd like an appointment next week", model.ActionBookingRequest, "normal"},
		{"trial", "Is there a trial I can join?", model.ActionBookingRequest, "normal"},
		{"quote", "Can you give me a quote?", model.ActionQuoteRequest, "normal"},
		{"how much", "How much do you charge for a logo?", model.ActionQuoteRequest, "normal"},
		{"rate", "What is your hourly rate?", model.ActionQuoteRequest, "normal"},
		{"payment chase", "Sorry to chase, but the invoice is still unpaid", model.ActionPaymentIssue, "normal"},
		{"payment wrong", "The invoice amount is wrong", model.ActionPaymentIssue, "normal"},
		{"payment ok is follow up", "I have paid the deposit, see you then", model.ActionFollowUp, "normal"},
		{"complaint unhappy", "I am unhappy, the logo looks off", model.ActionComplaint, "high"},
		{"complaint broken", "The website is broken and I am disappointed", model.ActionComplaint, "high"},
		{"complaint not working", "The contact form is not working", model.ActionComplaint, "high"},
	}
	prov := NewRulesProvider()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := prov.ExtractActions(context.Background(), rulesInput(inboundMsg("Alex", tc.body, "m1")))
			if err != nil {
				t.Fatalf("ExtractActions: %v", err)
			}
			if len(res.Actions) != 1 {
				t.Fatalf("actions = %d, want 1 (%+v)", len(res.Actions), res.Actions)
			}
			a := res.Actions[0]
			if a.Type != tc.wantType {
				t.Errorf("type = %q, want %q", a.Type, tc.wantType)
			}
			if a.Urgency != tc.wantUrgency {
				t.Errorf("urgency = %q, want %q", a.Urgency, tc.wantUrgency)
			}
			if a.Confidence != 70 {
				t.Errorf("confidence = %v, want 70", a.Confidence)
			}
			if a.MessageExternalID != "m1" {
				t.Errorf("message external id = %q, want m1", a.MessageExternalID)
			}
			if !strings.HasSuffix(a.Title, " from Alex") {
				t.Errorf("title = %q, want it to end with %q", a.Title, " from Alex")
			}
			if !strings.Contains(a.Summary, tc.body) {
				t.Errorf("summary = %q, want it to quote the ask %q", a.Summary, tc.body)
			}
		})
	}
}

func TestRulesProviderNewLead(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"availability", "Hello! Are you available next month?"},
		{"service mention", "I was told you do logo design, is that right?"},
		{"do you offer", "Do you offer brand identity work?"},
	}
	prov := NewRulesProvider()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := prov.ExtractActions(context.Background(), rulesInput(inboundMsg("Stranger", tc.body, "m9")))
			if err != nil {
				t.Fatalf("ExtractActions: %v", err)
			}
			if len(res.Actions) != 1 {
				t.Fatalf("actions = %d, want 1 (%+v)", len(res.Actions), res.Actions)
			}
			a := res.Actions[0]
			if a.Type != model.ActionNewLead {
				t.Errorf("type = %q, want new_lead", a.Type)
			}
			if a.Title != "New lead from Stranger" {
				t.Errorf("title = %q, want %q", a.Title, "New lead from Stranger")
			}
			if a.MessageExternalID != "m9" {
				t.Errorf("message external id = %q, want m9", a.MessageExternalID)
			}
		})
	}
}

func TestRulesProviderNoLeadWhenOtherActionsFound(t *testing.T) {
	prov := NewRulesProvider()
	res, err := prov.ExtractActions(context.Background(), rulesInput(
		inboundMsg("Alex", "Can you give me a quote?", "m1"),
		inboundMsg("Alex", "Are you free for logo design work?", "m2"),
	))
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(res.Actions))
	}
	if res.Actions[0].Type != model.ActionQuoteRequest {
		t.Errorf("type = %q, want quote_request (no extra lead)", res.Actions[0].Type)
	}
}

func TestRulesProviderCapsAtTwoActions(t *testing.T) {
	prov := NewRulesProvider()
	res, err := prov.ExtractActions(context.Background(), rulesInput(
		inboundMsg("Alex", "Can I book an appointment?", "m1"),
		inboundMsg("Alex", "And how much would that be?", "m2"),
		inboundMsg("Alex", "Also please chase the unpaid invoice", "m3"),
	))
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 2 {
		t.Fatalf("actions = %d, want capped at 2 (%+v)", len(res.Actions), res.Actions)
	}
	if res.Actions[0].Type != model.ActionBookingRequest || res.Actions[1].Type != model.ActionQuoteRequest {
		t.Errorf("action types = %q, %q; want booking_request, quote_request",
			res.Actions[0].Type, res.Actions[1].Type)
	}
}

func TestRulesProviderDedupesActionTypes(t *testing.T) {
	prov := NewRulesProvider()
	res, err := prov.ExtractActions(context.Background(), rulesInput(
		inboundMsg("Alex", "Can you give me a quote?", "m1"),
		inboundMsg("Alex", "Any update on that quote?", "m2"),
	))
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 1 {
		t.Errorf("actions = %d, want 1 (same type deduped)", len(res.Actions))
	}
}

func TestRulesProviderIgnoresOutboundMessages(t *testing.T) {
	prov := NewRulesProvider()
	out := model.Message{
		SenderName: "Me",
		Body:       "How much should I quote? I am available for logo design.",
		Direction:  model.DirectionOutbound,
	}
	res, err := prov.ExtractActions(context.Background(), rulesInput(out))
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 0 {
		t.Errorf("actions = %d, want 0 (outbound messages never create actions)", len(res.Actions))
	}
}

func TestRulesProviderReplies(t *testing.T) {
	bodies := map[string]string{
		"booking":   "Can I book a slot?",
		"quote":     "How much for a logo?",
		"payment":   "Please chase the unpaid invoice",
		"follow up": "I have paid the deposit",
		"complaint": "I am unhappy and disappointed",
		"lead":      "Are you available for logo design?",
	}
	prov := NewRulesProvider()
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			res, err := prov.ExtractActions(context.Background(), rulesInput(inboundMsg("Alex", body, "")))
			if err != nil {
				t.Fatalf("ExtractActions: %v", err)
			}
			if len(res.Actions) != 1 {
				t.Fatalf("actions = %d, want 1", len(res.Actions))
			}
			reply := res.Actions[0].SuggestedReply
			if !strings.Contains(reply, "Alex Design Studio") {
				t.Errorf("reply %q does not mention the business name", reply)
			}
			if strings.ContainsAny(reply, "$0123456789") {
				t.Errorf("reply %q invents prices or figures", reply)
			}
			if !strings.Contains(reply, "Alex") {
				t.Errorf("reply %q does not greet the sender", reply)
			}
		})
	}
}

func TestRulesProviderTone(t *testing.T) {
	msg := inboundMsg("Alex", "Can you give me a quote?", "")

	friendly := rulesInput(msg)
	res, err := NewRulesProvider().ExtractActions(context.Background(), friendly)
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if !strings.HasPrefix(res.Actions[0].SuggestedReply, "Hi ") {
		t.Errorf("friendly reply = %q, want it to start with %q", res.Actions[0].SuggestedReply, "Hi ")
	}

	formal := friendly
	formal.Profile.Tone = "formal"
	res, err = NewRulesProvider().ExtractActions(context.Background(), formal)
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if !strings.HasPrefix(res.Actions[0].SuggestedReply, "Hello ") {
		t.Errorf("formal reply = %q, want it to start with %q", res.Actions[0].SuggestedReply, "Hello ")
	}
}

func TestRulesProviderNameFallsBackToConversationTitle(t *testing.T) {
	in := ProviderInput{
		Profile:      testProfile,
		Conversation: model.Conversation{Title: "Design Referrals"},
		Messages:     []model.Message{inboundMsg("", "Can you give me a quote?", "")},
	}
	res, err := NewRulesProvider().ExtractActions(context.Background(), in)
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(res.Actions))
	}
	if want := "Quote request from Design Referrals"; res.Actions[0].Title != want {
		t.Errorf("title = %q, want %q", res.Actions[0].Title, want)
	}
}

func TestRulesProviderName(t *testing.T) {
	if got := NewRulesProvider().Name(); got != "rules" {
		t.Errorf("Name() = %q, want rules", got)
	}
}
