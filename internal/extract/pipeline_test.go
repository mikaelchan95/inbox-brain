package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/classify"
	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

var testProfile = model.BusinessProfile{
	BusinessName:  "Alex Design Studio",
	BusinessType:  "freelance designer",
	Services:      []string{"logo design", "landing pages"},
	Tone:          "friendly",
	ReplyLanguage: "English",
}

// fakeProvider captures every ProviderInput it receives; respond decides the
// result per input (nil → empty result).
type fakeProvider struct {
	inputs  []ProviderInput
	respond func(in ProviderInput) (ProviderResult, error)
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) ExtractActions(_ context.Context, in ProviderInput) (ProviderResult, error) {
	f.inputs = append(f.inputs, in)
	if f.respond == nil {
		return ProviderResult{}, nil
	}
	return f.respond(in)
}

func (f *fakeProvider) inputFor(title string) (ProviderInput, bool) {
	for _, in := range f.inputs {
		if in.Conversation.Title == title {
			return in, true
		}
	}
	return ProviderInput{}, false
}

type tmsg struct {
	body string
	dir  string // default inbound
	ext  string // message external id
}

type tconv struct {
	title string
	msgs  []tmsg
	cls   *model.ConversationClassification // nil = no classification row
}

// buildStore opens a temp store and seeds the given conversations. Inbound
// messages get the conversation title as sender name.
func buildStore(t *testing.T, convs []tconv) (*store.Store, map[string]model.Conversation) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ib.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	out := map[string]model.Conversation{}
	base := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	for ci, tc := range convs {
		conv, err := s.UpsertConversation(model.Conversation{
			WorkspaceID:   ws.ID,
			ConnectorID:   "conn_test",
			Channel:       model.ChannelDemo,
			ExternalID:    fmt.Sprintf("ext-%d", ci),
			Title:         tc.title,
			LastMessageAt: base,
		})
		if err != nil {
			t.Fatalf("upsert conversation %q: %v", tc.title, err)
		}
		for mi, tm := range tc.msgs {
			dir := tm.dir
			if dir == "" {
				dir = model.DirectionInbound
			}
			sender := tc.title
			if dir == model.DirectionOutbound {
				sender = "Me"
			}
			if _, err := s.InsertMessage(model.Message{
				WorkspaceID:            ws.ID,
				ConversationID:         conv.ID,
				Channel:                model.ChannelDemo,
				Provider:               model.ProviderManualDemo,
				ConnectorID:            "conn_test",
				ConversationExternalID: conv.ExternalID,
				MessageExternalID:      tm.ext,
				SenderName:             sender,
				Body:                   tm.body,
				Direction:              dir,
				OccurredAt:             base.Add(time.Duration(ci*100+mi) * time.Minute),
				DedupeKey:              fmt.Sprintf("demo:%d:%d", ci, mi),
			}); err != nil {
				t.Fatalf("insert message %q: %v", tm.body, err)
			}
		}
		if tc.cls != nil {
			c := *tc.cls
			c.ConversationID = conv.ID
			if c.Source == "" {
				c.Source = model.SourceRules
			}
			if err := s.SaveConversationClassification(c); err != nil {
				t.Fatalf("save classification for %q: %v", tc.title, err)
			}
		}
		out[tc.title] = conv
	}
	return s, out
}

func newTestPipeline(s *store.Store, prov Provider, autoMode bool) *Pipeline {
	p := NewPipeline(s, classify.New(testProfile, nil), prov, testProfile, autoMode)
	p.Out = io.Discard
	return p
}

func seedCls(label string, conf float64, reviewed bool, override string) *model.ConversationClassification {
	return &model.ConversationClassification{
		Classification:     label,
		BusinessConfidence: conf,
		ReviewedByUser:     reviewed,
		UserOverride:       override,
	}
}

// gatingConvs covers every branch of the spec §13 privacy gate.
func gatingConvs() []tconv {
	return []tconv{
		{title: "Personal Chat", msgs: []tmsg{{body: "Dinner tonight?"}},
			cls: seedCls(model.ConvPersonal, 10, false, "")},
		{title: "Business Reviewed", msgs: []tmsg{
			{body: "Can you send a quote for a logo?"},
			{body: "Sure, will do!", dir: model.DirectionOutbound},
		}, cls: seedCls(model.ConvBusiness, 70, true, "")},
		{title: "Business Unreviewed Low", msgs: []tmsg{{body: "How much for a website?"}},
			cls: seedCls(model.ConvBusiness, 70, false, "")},
		{title: "Business 95 Unreviewed", msgs: []tmsg{{body: "I need a booking for a photoshoot"}},
			cls: seedCls(model.ConvBusiness, 95, false, "")},
		{title: "Mixed Chat", msgs: []tmsg{
			{body: "Dinner on Friday?"},
			{body: "Cannot, busy this week", dir: model.DirectionOutbound},
			{body: "Can you quote me for a landing page?"},
			{body: "Please send the invoice for the logo design"},
			{body: "Sure, I will send the invoice tonight", dir: model.DirectionOutbound},
		}, cls: seedCls(model.ConvMixed, 68, false, "")},
		{title: "Unknown Chat", msgs: []tmsg{{body: "Hey, long time no see"}},
			cls: seedCls(model.ConvUnknown, 50, false, "")},
		{title: "Override Personal", msgs: []tmsg{{body: "Send me your rate card"}},
			cls: seedCls(model.ConvBusiness, 90, true, model.ConvPersonal)},
		{title: "Override Business", msgs: []tmsg{{body: "About that project we discussed"}},
			cls: seedCls(model.ConvPersonal, 20, true, model.ConvBusiness)},
		{title: "No Classification", msgs: []tmsg{{body: "Quote please"}}},
	}
}

func TestProcessApprovedPrivacyGate(t *testing.T) {
	cases := []struct {
		name     string
		autoMode bool
		want     []string // conversation titles that may reach the provider
	}{
		{
			name:     "auto mode off",
			autoMode: false,
			want:     []string{"Business Reviewed", "Mixed Chat", "Override Business"},
		},
		{
			name:     "auto mode on",
			autoMode: true,
			want:     []string{"Business 95 Unreviewed", "Business Reviewed", "Mixed Chat", "Override Business"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := buildStore(t, gatingConvs())
			prov := &fakeProvider{}
			p := newTestPipeline(s, prov, tc.autoMode)

			sum, err := p.ProcessApproved(context.Background())
			if err != nil {
				t.Fatalf("ProcessApproved: %v", err)
			}

			var got []string
			for _, in := range prov.inputs {
				got = append(got, in.Conversation.Title)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("conversations sent to provider = %v, want %v", got, want)
			}

			if sum.ConversationsProcessed != len(tc.want) {
				t.Errorf("ConversationsProcessed = %d, want %d", sum.ConversationsProcessed, len(tc.want))
			}
			if wantSkipped := len(gatingConvs()) - len(tc.want); sum.ConversationsSkipped != wantSkipped {
				t.Errorf("ConversationsSkipped = %d, want %d", sum.ConversationsSkipped, wantSkipped)
			}
			if sum.Failures != 0 {
				t.Errorf("Failures = %d, want 0", sum.Failures)
			}

			// Privacy invariant: no personal content ever reaches a provider.
			for _, in := range prov.inputs {
				for _, m := range in.Messages {
					if strings.Contains(strings.ToLower(m.Body), "dinner") {
						t.Errorf("personal message leaked to provider in %q: %q", in.Conversation.Title, m.Body)
					}
				}
			}
		})
	}
}

func TestProcessApprovedMixedFiltersMessages(t *testing.T) {
	s, convs := buildStore(t, gatingConvs())
	prov := &fakeProvider{}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}

	in, ok := prov.inputFor("Mixed Chat")
	if !ok {
		t.Fatal("mixed chat never reached the provider")
	}
	var bodies []string
	for _, m := range in.Messages {
		bodies = append(bodies, m.Body)
	}
	want := []string{
		"Can you quote me for a landing page?",
		"Please send the invoice for the logo design",
		"Sure, I will send the invoice tonight", // outbound business message stays for context
	}
	if !reflect.DeepEqual(bodies, want) {
		t.Errorf("mixed chat provider input = %q, want %q", bodies, want)
	}

	// Message-level classifications were persisted for every message.
	msgs, err := s.ListMessages(convs["Mixed Chat"].ID, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	for _, m := range msgs {
		mc, err := s.GetMessageClassification(m.ID)
		if err != nil {
			t.Fatalf("get message classification: %v", err)
		}
		if mc == nil {
			t.Errorf("message %q has no persisted classification", m.Body)
		}
	}
}

func TestProcessApprovedIncludesOutboundContextInBusinessChats(t *testing.T) {
	s, _ := buildStore(t, gatingConvs())
	prov := &fakeProvider{}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}
	in, ok := prov.inputFor("Business Reviewed")
	if !ok {
		t.Fatal("business reviewed chat never reached the provider")
	}
	if len(in.Messages) != 2 {
		t.Fatalf("provider input has %d messages, want 2 (inbound + outbound)", len(in.Messages))
	}
	if in.Messages[1].Direction != model.DirectionOutbound {
		t.Errorf("second message direction = %q, want outbound reply included for context", in.Messages[1].Direction)
	}
}

func TestProcessApprovedIdempotent(t *testing.T) {
	s, _ := buildStore(t, []tconv{{
		title: "Mrs Tan",
		msgs:  []tmsg{{body: "Can I book a trial class?", ext: "m1"}},
		cls:   seedCls(model.ConvBusiness, 94, true, ""),
	}})
	prov := &fakeProvider{respond: func(ProviderInput) (ProviderResult, error) {
		return ProviderResult{Actions: []ExtractedAction{{
			Type:       model.ActionBookingRequest,
			Title:      "Booking request from Mrs Tan",
			Summary:    "Asked for a trial class",
			Confidence: 90,
			Urgency:    "normal",
		}}}, nil
	}}
	p := newTestPipeline(s, prov, false)

	sum1, err := p.ProcessApproved(context.Background())
	if err != nil {
		t.Fatalf("first ProcessApproved: %v", err)
	}
	if sum1.ActionsCreated != 1 || sum1.ConversationsProcessed != 1 {
		t.Fatalf("first run = %+v, want 1 processed / 1 action", sum1)
	}

	sum2, err := p.ProcessApproved(context.Background())
	if err != nil {
		t.Fatalf("second ProcessApproved: %v", err)
	}
	if sum2.ActionsCreated != 0 {
		t.Errorf("second run ActionsCreated = %d, want 0", sum2.ActionsCreated)
	}
	if sum2.ConversationsProcessed != 0 || sum2.ConversationsSkipped != 1 {
		t.Errorf("second run = %+v, want 0 processed / 1 skipped", sum2)
	}
	if len(prov.inputs) != 1 {
		t.Errorf("provider called %d times, want 1 (second run must skip)", len(prov.inputs))
	}
	actions, err := s.ListActions(store.ActionFilter{})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) != 1 {
		t.Errorf("stored actions = %d, want 1", len(actions))
	}
}

func TestProcessApprovedNewLeadCreatesLead(t *testing.T) {
	s, convs := buildStore(t, []tconv{{
		title: "Unknown +Number",
		msgs:  []tmsg{{body: "Are you available for logo design?", ext: "m1"}},
		cls:   seedCls(model.ConvBusiness, 87, true, ""),
	}})
	prov := &fakeProvider{respond: func(ProviderInput) (ProviderResult, error) {
		return ProviderResult{Actions: []ExtractedAction{{
			Type:       model.ActionNewLead,
			Title:      "New lead",
			Summary:    "Asked about logo design availability",
			Confidence: 80,
			Urgency:    "normal",
		}}}, nil
	}}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}

	leads, err := s.ListLeads("")
	if err != nil {
		t.Fatalf("list leads: %v", err)
	}
	if len(leads) != 1 {
		t.Fatalf("leads = %d, want 1", len(leads))
	}
	lead := leads[0]
	if lead.Status != model.LeadOpen {
		t.Errorf("lead status = %q, want open", lead.Status)
	}
	if lead.ConversationID != convs["Unknown +Number"].ID {
		t.Errorf("lead conversation = %q, want %q", lead.ConversationID, convs["Unknown +Number"].ID)
	}
	if lead.Summary != "Asked about logo design availability" {
		t.Errorf("lead summary = %q", lead.Summary)
	}
	actions, err := s.ListActions(store.ActionFilter{Type: model.ActionNewLead})
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) != 1 || lead.ActionID != actions[0].ID {
		t.Errorf("lead.ActionID = %q, want %q", lead.ActionID, actions[0].ID)
	}
}

func TestProcessApprovedProviderFailureContinues(t *testing.T) {
	s, _ := buildStore(t, []tconv{
		{title: "Failing", msgs: []tmsg{{body: "Quote for a deck please"}},
			cls: seedCls(model.ConvBusiness, 90, true, "")},
		{title: "Working", msgs: []tmsg{{body: "Can I book a session?"}},
			cls: seedCls(model.ConvBusiness, 90, true, "")},
	})
	prov := &fakeProvider{respond: func(in ProviderInput) (ProviderResult, error) {
		if in.Conversation.Title == "Failing" {
			return ProviderResult{}, errors.New("boom: provider exploded")
		}
		return ProviderResult{Actions: []ExtractedAction{{
			Type: model.ActionBookingRequest, Title: "Booking", Confidence: 80, Urgency: "normal",
		}}}, nil
	}}
	p := newTestPipeline(s, prov, false)

	sum, err := p.ProcessApproved(context.Background())
	if err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}
	if sum.Failures != 1 {
		t.Errorf("Failures = %d, want 1", sum.Failures)
	}
	if sum.ConversationsProcessed != 1 || sum.ActionsCreated != 1 {
		t.Errorf("summary = %+v, want 1 processed / 1 action", sum)
	}

	runs, err := s.ListExtractionRuns(0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(runs))
	}
	var failed, succeeded *model.ExtractionRun
	for i := range runs {
		switch runs[i].Status {
		case model.RunFailed:
			failed = &runs[i]
		case model.RunSuccess:
			succeeded = &runs[i]
		}
	}
	if failed == nil {
		t.Fatal("no failed run recorded")
	}
	if !strings.Contains(failed.Error, "boom") {
		t.Errorf("failed run error = %q, want it to contain the provider error", failed.Error)
	}
	if failed.Provider != "fake" {
		t.Errorf("failed run provider = %q, want fake", failed.Provider)
	}
	if succeeded == nil {
		t.Fatal("no successful run recorded")
	}
	if succeeded.ActionsCreated != 1 || succeeded.InputMessages != 1 {
		t.Errorf("success run counts = %d actions / %d inputs, want 1 / 1",
			succeeded.ActionsCreated, succeeded.InputMessages)
	}
	if succeeded.FinishedAt.IsZero() {
		t.Error("success run has zero FinishedAt")
	}
}

func TestProcessApprovedRecordsAuditEvent(t *testing.T) {
	s, convs := buildStore(t, []tconv{{
		title: "Mrs Tan",
		msgs:  []tmsg{{body: "Can I book a trial class?"}},
		cls:   seedCls(model.ConvBusiness, 94, true, ""),
	}})
	prov := &fakeProvider{}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}

	events, err := s.ListAuditEvents(0)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	var found *model.AuditEvent
	for i := range events {
		if events[i].EventType == "ai_context_sent" {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatal("no ai_context_sent audit event recorded")
	}
	if found.Subject != convs["Mrs Tan"].ID {
		t.Errorf("audit subject = %q, want conversation id %q", found.Subject, convs["Mrs Tan"].ID)
	}
	if want := "1 messages from Mrs Tan sent to fake"; found.Detail != want {
		t.Errorf("audit detail = %q, want %q", found.Detail, want)
	}
}

func TestProcessApprovedAnchorsActions(t *testing.T) {
	s, convs := buildStore(t, []tconv{{
		title: "Alex",
		msgs: []tmsg{
			{body: "Quote for a logo please", ext: "m1"},
			{body: "Also the invoice from last month", ext: "m2"},
		},
		cls: seedCls(model.ConvBusiness, 90, true, ""),
	}})
	prov := &fakeProvider{respond: func(ProviderInput) (ProviderResult, error) {
		return ProviderResult{Actions: []ExtractedAction{
			{Type: model.ActionQuoteRequest, Title: "Quote", Confidence: 80, Urgency: "normal",
				MessageExternalID: "m1"},
			{Type: model.ActionFollowUp, Title: "Follow up", Confidence: 70, Urgency: "normal",
				MessageExternalID: "does-not-exist"},
		}}, nil
	}}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}

	msgs, err := s.ListMessages(convs["Alex"].ID, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	internal := map[string]string{} // external id → internal id
	for _, m := range msgs {
		internal[m.MessageExternalID] = m.ID
	}

	quote, err := s.ListActions(store.ActionFilter{Type: model.ActionQuoteRequest})
	if err != nil || len(quote) != 1 {
		t.Fatalf("quote actions = %d (err %v), want 1", len(quote), err)
	}
	if quote[0].MessageID != internal["m1"] {
		t.Errorf("quote action anchored to %q, want internal id of m1 (%q)", quote[0].MessageID, internal["m1"])
	}
	follow, err := s.ListActions(store.ActionFilter{Type: model.ActionFollowUp})
	if err != nil || len(follow) != 1 {
		t.Fatalf("follow_up actions = %d (err %v), want 1", len(follow), err)
	}
	if follow[0].MessageID != internal["m2"] {
		t.Errorf("unresolvable external id anchored to %q, want latest inbound (m2 = %q)",
			follow[0].MessageID, internal["m2"])
	}
	if quote[0].Source != "fake" || quote[0].Status != model.StatusOpen {
		t.Errorf("action source/status = %q/%q, want fake/open", quote[0].Source, quote[0].Status)
	}
}

func TestProcessApprovedCapsContextWindow(t *testing.T) {
	msgs := make([]tmsg, 35)
	for i := range msgs {
		msgs[i] = tmsg{body: fmt.Sprintf("Message number %d about the project", i)}
	}
	s, _ := buildStore(t, []tconv{{
		title: "Busy Client",
		msgs:  msgs,
		cls:   seedCls(model.ConvBusiness, 90, true, ""),
	}})
	prov := &fakeProvider{}
	p := newTestPipeline(s, prov, false)
	if _, err := p.ProcessApproved(context.Background()); err != nil {
		t.Fatalf("ProcessApproved: %v", err)
	}

	if len(prov.inputs) != 1 {
		t.Fatalf("provider called %d times, want 1", len(prov.inputs))
	}
	got := prov.inputs[0].Messages
	if len(got) != maxContextMessages {
		t.Fatalf("provider input = %d messages, want %d", len(got), maxContextMessages)
	}
	if want := "Message number 5 about the project"; got[0].Body != want {
		t.Errorf("first window message = %q, want %q (last 30 win)", got[0].Body, want)
	}
	if want := "Message number 34 about the project"; got[len(got)-1].Body != want {
		t.Errorf("last window message = %q, want %q", got[len(got)-1].Body, want)
	}
	runs, err := s.ListExtractionRuns(0)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %d (err %v), want 1", len(runs), err)
	}
	if runs[0].InputMessages != maxContextMessages {
		t.Errorf("run InputMessages = %d, want %d", runs[0].InputMessages, maxContextMessages)
	}
}

func TestProcessConversation(t *testing.T) {
	s, convs := buildStore(t, gatingConvs())
	prov := &fakeProvider{respond: func(ProviderInput) (ProviderResult, error) {
		return ProviderResult{Actions: []ExtractedAction{{
			Type: model.ActionFollowUp, Title: "Follow up", Confidence: 70, Urgency: "normal",
		}}}, nil
	}}
	p := newTestPipeline(s, prov, false)
	ctx := context.Background()

	n, err := p.ProcessConversation(ctx, convs["Business Reviewed"].ID)
	if err != nil {
		t.Fatalf("ProcessConversation(eligible): %v", err)
	}
	if n != 1 {
		t.Errorf("actions created = %d, want 1", n)
	}

	ineligible := []string{"Personal Chat", "Unknown Chat", "Override Personal", "Business Unreviewed Low", "No Classification"}
	for _, title := range ineligible {
		if _, err := p.ProcessConversation(ctx, convs[title].ID); err == nil {
			t.Errorf("ProcessConversation(%q) = nil error, want not-approved error", title)
		}
	}
	if _, err := p.ProcessConversation(ctx, "conv_missing"); err == nil {
		t.Error("ProcessConversation(missing id) = nil error, want not-found error")
	}
}

func TestClassifyAll(t *testing.T) {
	s, convs := buildStore(t, []tconv{
		{title: "Mrs Tan", msgs: []tmsg{
			{body: "Can I get a trial class for my son? How much is the package price?"},
		}},
		{title: "Mum", msgs: []tmsg{
			{body: "Dinner at home this Sunday? Your dad misses you"},
		}},
		{title: "Reviewed Keep", msgs: []tmsg{{body: "dinner and football"}},
			cls: seedCls(model.ConvBusiness, 77, true, "")},
		{title: "Overridden Keep", msgs: []tmsg{{body: "quote invoice deadline project"}},
			cls: seedCls(model.ConvPersonal, 12, true, model.ConvPersonal)},
	})
	p := newTestPipeline(s, &fakeProvider{}, false)

	n, err := p.ClassifyAll(context.Background())
	if err != nil {
		t.Fatalf("ClassifyAll: %v", err)
	}
	if n != 2 {
		t.Errorf("classified = %d, want 2 (reviewed/overridden untouched)", n)
	}

	check := func(title, wantLabel string) *model.ConversationClassification {
		t.Helper()
		cls, err := s.GetConversationClassification(convs[title].ID)
		if err != nil {
			t.Fatalf("get classification %q: %v", title, err)
		}
		if cls == nil {
			t.Fatalf("no classification stored for %q", title)
		}
		if cls.Classification != wantLabel {
			t.Errorf("%q label = %q (confidence %v), want %q", title, cls.Classification, cls.BusinessConfidence, wantLabel)
		}
		return cls
	}

	check("Mrs Tan", model.ConvBusiness)
	check("Mum", model.ConvPersonal)

	kept := check("Reviewed Keep", model.ConvBusiness)
	if kept.BusinessConfidence != 77 || !kept.ReviewedByUser {
		t.Errorf("reviewed classification clobbered: %+v", kept)
	}
	over := check("Overridden Keep", model.ConvPersonal)
	if over.UserOverride != model.ConvPersonal {
		t.Errorf("user override clobbered: %+v", over)
	}

	// Re-running reclassifies the same unreviewed conversations again.
	n2, err := p.ClassifyAll(context.Background())
	if err != nil {
		t.Fatalf("second ClassifyAll: %v", err)
	}
	if n2 != 2 {
		t.Errorf("second pass classified = %d, want 2", n2)
	}
}

func TestNewPipelineOutNeverNil(t *testing.T) {
	p := NewPipeline(nil, nil, nil, model.BusinessProfile{}, false)
	if p.Out == nil {
		t.Fatal("NewPipeline left Out nil")
	}
}
