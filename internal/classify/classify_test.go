package classify

import (
	"strings"
	"testing"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

func msgsOf(bodies ...string) []model.Message {
	out := make([]model.Message, 0, len(bodies))
	for _, b := range bodies {
		out = append(out, model.Message{Body: b})
	}
	return out
}

func TestLabelForScore(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0, model.ConvPersonal},
		{39, model.ConvPersonal},
		{40, model.ConvUnknown},
		{64, model.ConvUnknown},
		{65, model.ConvBusiness},
		{84, model.ConvBusiness},
		{85, model.ConvBusiness},
		{100, model.ConvBusiness},
	}
	for _, tt := range tests {
		if got := LabelForScore(tt.score); got != tt.want {
			t.Errorf("LabelForScore(%v) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestMessageLabelForScore(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0, model.MsgPersonal},
		{39, model.MsgPersonal},
		{40, model.MsgAmbiguous},
		{64, model.MsgAmbiguous},
		{65, model.MsgBusiness},
		{84, model.MsgBusiness},
		{85, model.MsgBusiness},
		{100, model.MsgBusiness},
	}
	for _, tt := range tests {
		if got := MessageLabelForScore(tt.score); got != tt.want {
			t.Errorf("MessageLabelForScore(%v) = %q, want %q", tt.score, got, tt.want)
		}
	}
}

func TestPhraseIndexWordBoundaries(t *testing.T) {
	tests := []struct {
		text   string
		phrase string
		want   bool
	}{
		{"what is your rate?", "rate", true},
		{"we should celebrate tonight", "rate", false},
		{"rated highly", "rate", false},
		{"rate", "rate", true},
		{"top-rate work", "rate", true}, // hyphen is a boundary
		{"how much is it?", "how much", true},
		{"show much appreciated", "how much", false}, // "show much" contains "how much" off-boundary
		{"need a landing page now", "landing page", true},
		{"landing pages", "landing page", false},
		{"a quote, please", "quote", true},
		{"misquoted", "quote", false},
		{"availability on saturday", "available", false}, // "available" inside "availability"
		{"availability on saturday", "availability", true},
		{"", "rate", false},
		{"rate", "", false},
	}
	for _, tt := range tests {
		if _, got := phraseIndex(tt.text, tt.phrase); got != tt.want {
			t.Errorf("phraseIndex(%q, %q) matched = %v, want %v", tt.text, tt.phrase, got, tt.want)
		}
	}
}

func TestScoreConversationSpec26(t *testing.T) {
	profile := model.BusinessProfile{
		BusinessName: "Bright Minds Tuition",
		BusinessType: "tuition center",
	}
	c := New(profile, nil)

	t.Run("mum-style chat is personal below 40", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_mum", Title: "Mum"}
		got := c.ScoreConversation(conv, msgsOf(
			"dinner at home tonight",
			"family birthday this weekend",
		))
		if got.Classification != model.ConvPersonal {
			t.Errorf("classification = %q, want %q (reason: %s)", got.Classification, model.ConvPersonal, got.Reason)
		}
		if got.BusinessConfidence >= 40 {
			t.Errorf("confidence = %v, want < 40", got.BusinessConfidence)
		}
		if got.ConversationID != "conv_mum" {
			t.Errorf("ConversationID = %q, want conv_mum", got.ConversationID)
		}
		if got.ID != "" {
			t.Errorf("ID = %q, want empty (caller assigns)", got.ID)
		}
		if got.Source != model.SourceRules {
			t.Errorf("Source = %q, want %q", got.Source, model.SourceRules)
		}
	})

	t.Run("mrs tan-style chat is business at 85 or above", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_tan", Title: "Mrs Tan"}
		got := c.ScoreConversation(conv, msgsOf(
			"Hi, I'd like a trial class for my son",
			"How much per session?",
			"Do you have Saturday availability?",
		))
		if got.Classification != model.ConvBusiness {
			t.Errorf("classification = %q, want %q (reason: %s)", got.Classification, model.ConvBusiness, got.Reason)
		}
		if got.BusinessConfidence < 85 {
			t.Errorf("confidence = %v, want >= 85", got.BusinessConfidence)
		}
	})

	t.Run("alex-style chat is mixed", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_alex", Title: "Alex"}
		got := c.ScoreConversation(conv, msgsOf(
			"dinner on friday?",
			"haha that meme was funny",
			"can you quote me for a landing page?",
			"send me the invoice",
		))
		if got.Classification != model.ConvMixed {
			t.Errorf("classification = %q, want %q (confidence %v, reason: %s)",
				got.Classification, model.ConvMixed, got.BusinessConfidence, got.Reason)
		}
		if !strings.HasPrefix(got.Reason, "Mentions ") || !strings.Contains(got.Reason, "; also ") {
			t.Errorf("reason %q does not list business and personal signals", got.Reason)
		}
		if !strings.Contains(got.Reason, "quote") {
			t.Errorf("reason %q missing business signal quote", got.Reason)
		}
		if !strings.Contains(got.Reason, "dinner") {
			t.Errorf("reason %q missing personal signal dinner", got.Reason)
		}
	})

	t.Run("alex messages classify individually", func(t *testing.T) {
		tests := []struct {
			body string
			want string
		}{
			{"can you quote me for a landing page?", model.MsgBusiness},
			{"send me the invoice", model.MsgBusiness},
			{"dinner on friday?", model.MsgPersonal},
			{"haha that meme was funny", model.MsgPersonal},
			{"ok sounds good", model.MsgAmbiguous},
		}
		for _, tt := range tests {
			got := c.ScoreMessage(model.Message{ID: "msg_1", Body: tt.body})
			if got.Classification != tt.want {
				t.Errorf("ScoreMessage(%q) = %q (confidence %v), want %q",
					tt.body, got.Classification, got.BusinessConfidence, tt.want)
			}
			if got.MessageID != "msg_1" {
				t.Errorf("MessageID = %q, want msg_1", got.MessageID)
			}
			if got.Source != model.SourceRules {
				t.Errorf("Source = %q, want %q", got.Source, model.SourceRules)
			}
		}
	})

	t.Run("logo design lead from unknown number is business", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_unknown", Title: "+65 9123 4567"}
		got := c.ScoreConversation(conv, msgsOf(
			"hello, are you available for logo design?",
		))
		if got.Classification != model.ConvBusiness {
			t.Errorf("classification = %q, want %q (confidence %v)", got.Classification, model.ConvBusiness, got.BusinessConfidence)
		}
		if got.BusinessConfidence < 85 {
			t.Errorf("confidence = %v, want >= 85", got.BusinessConfidence)
		}
	})

	t.Run("business words in title add", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_ref", Title: "Design Referrals"}
		got := c.ScoreConversation(conv, msgsOf(
			"any referrals for logo design this week?",
		))
		if got.Classification != model.ConvBusiness {
			t.Errorf("classification = %q, want %q (confidence %v)", got.Classification, model.ConvBusiness, got.BusinessConfidence)
		}
		if got.BusinessConfidence < 85 {
			t.Errorf("confidence = %v, want >= 85", got.BusinessConfidence)
		}
	})
}

func TestAlwaysIgnoreAndAlwaysInclude(t *testing.T) {
	t.Run("profile always-ignore forces personal 5", func(t *testing.T) {
		profile := model.BusinessProfile{AlwaysIgnoreChats: []string{"Family Group"}}
		c := New(profile, nil)
		conv := model.Conversation{ID: "conv_fam", Title: "Family Group"}
		// Business-heavy body must not matter: the forced rule wins.
		got := c.ScoreConversation(conv, msgsOf(
			"please send the invoice and the quote for the project deadline",
		))
		if got.Classification != model.ConvPersonal {
			t.Errorf("classification = %q, want %q", got.Classification, model.ConvPersonal)
		}
		if got.BusinessConfidence != 5 {
			t.Errorf("confidence = %v, want 5", got.BusinessConfidence)
		}
		if !strings.Contains(got.Reason, "Family Group") || !strings.Contains(got.Reason, "always-ignore") {
			t.Errorf("reason %q does not name the always-ignore rule", got.Reason)
		}
	})

	t.Run("profile always-ignore matches case-insensitively", func(t *testing.T) {
		profile := model.BusinessProfile{AlwaysIgnoreChats: []string{"family group"}}
		c := New(profile, nil)
		conv := model.Conversation{ID: "conv_fam", Title: "Family Group"}
		got := c.ScoreConversation(conv, nil)
		if got.Classification != model.ConvPersonal || got.BusinessConfidence != 5 {
			t.Errorf("got %q/%v, want personal/5", got.Classification, got.BusinessConfidence)
		}
	})

	t.Run("profile always-include forces business 95", func(t *testing.T) {
		profile := model.BusinessProfile{AlwaysIncludeChats: []string{"Design Referrals"}}
		c := New(profile, nil)
		conv := model.Conversation{ID: "conv_ref", Title: "Design Referrals"}
		got := c.ScoreConversation(conv, msgsOf("dinner and football this weekend"))
		if got.Classification != model.ConvBusiness {
			t.Errorf("classification = %q, want %q", got.Classification, model.ConvBusiness)
		}
		if got.BusinessConfidence != 95 {
			t.Errorf("confidence = %v, want 95", got.BusinessConfidence)
		}
		if !strings.Contains(got.Reason, "Design Referrals") || !strings.Contains(got.Reason, "always-include") {
			t.Errorf("reason %q does not name the always-include rule", got.Reason)
		}
	})

	t.Run("always_include chat_name rule forces business 95", func(t *testing.T) {
		rules := []model.ClassificationRule{{
			ID:       "rule_1",
			RuleType: model.RuleChatName,
			Pattern:  "Client Leads",
			Action:   model.RuleAlwaysInclude,
		}}
		c := New(model.BusinessProfile{}, rules)
		conv := model.Conversation{ID: "conv_cl", Title: "client leads"} // case-insensitive
		got := c.ScoreConversation(conv, msgsOf("dinner and football this weekend"))
		if got.Classification != model.ConvBusiness || got.BusinessConfidence != 95 {
			t.Errorf("got %q/%v, want business/95", got.Classification, got.BusinessConfidence)
		}
		if !strings.Contains(got.Reason, "Client Leads") {
			t.Errorf("reason %q does not name the matched rule pattern", got.Reason)
		}
	})

	t.Run("always_ignore contact_name rule matches sender name", func(t *testing.T) {
		rules := []model.ClassificationRule{{
			ID:       "rule_2",
			RuleType: model.RuleContactName,
			Pattern:  "Mum",
			Action:   model.RuleAlwaysIgnore,
		}}
		c := New(model.BusinessProfile{}, rules)
		conv := model.Conversation{ID: "conv_m", Title: "+65 8000 0000"}
		msgs := []model.Message{{Body: "please send the invoice", SenderName: "Mum"}}
		got := c.ScoreConversation(conv, msgs)
		if got.Classification != model.ConvPersonal || got.BusinessConfidence != 5 {
			t.Errorf("got %q/%v, want personal/5", got.Classification, got.BusinessConfidence)
		}
	})

	t.Run("keyword rules do not force classification", func(t *testing.T) {
		rules := []model.ClassificationRule{{
			ID:       "rule_3",
			RuleType: model.RuleKeyword,
			Pattern:  "Mum",
			Action:   model.RuleAlwaysIgnore,
		}}
		c := New(model.BusinessProfile{}, rules)
		conv := model.Conversation{ID: "conv_k", Title: "Mum"}
		got := c.ScoreConversation(conv, msgsOf("send the invoice and the quote please"))
		if got.BusinessConfidence == 5 {
			t.Errorf("keyword rule must not force personal/5, got %q/%v", got.Classification, got.BusinessConfidence)
		}
	})
}

func TestProfileKeywordsAndServicesExtendBusinessSet(t *testing.T) {
	profile := model.BusinessProfile{
		Services:         []string{"Wheel Throwing Class"},
		BusinessKeywords: []string{"Kiln"},
	}
	c := New(profile, nil)

	tests := []struct {
		body string
		want string
	}{
		{"do you teach a wheel throwing class?", model.MsgBusiness},
		{"is the kiln ready?", model.MsgBusiness},
		{"kilns are ready", model.MsgAmbiguous}, // word boundary: "kiln" not matched in "kilns"
	}
	for _, tt := range tests {
		got := c.ScoreMessage(model.Message{Body: tt.body})
		if got.Classification != tt.want {
			t.Errorf("ScoreMessage(%q) = %q (confidence %v), want %q",
				tt.body, got.Classification, got.BusinessConfidence, tt.want)
		}
	}
}

func TestMixedOverridesScoreBand(t *testing.T) {
	c := New(model.BusinessProfile{}, nil)
	conv := model.Conversation{ID: "conv_x", Title: "Jess"}
	// 5 business signals would land in the business band, but 2 personal
	// signals make the thread mixed regardless.
	got := c.ScoreConversation(conv, msgsOf(
		"price quote invoice booking deadline",
		"dinner then party later?",
	))
	if got.Classification != model.ConvMixed {
		t.Errorf("classification = %q (confidence %v), want %q",
			got.Classification, got.BusinessConfidence, model.ConvMixed)
	}
}

func TestScoreClampingAndNeutral(t *testing.T) {
	c := New(model.BusinessProfile{}, nil)

	t.Run("clamps at 100", func(t *testing.T) {
		got := c.ScoreMessage(model.Message{Body: "price quote invoice booking deadline payment"})
		if got.BusinessConfidence != 100 {
			t.Errorf("confidence = %v, want 100", got.BusinessConfidence)
		}
		if got.Classification != model.MsgBusiness {
			t.Errorf("classification = %q, want business", got.Classification)
		}
	})

	t.Run("clamps at 0 with personal title", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_mum", Title: "Mum"}
		got := c.ScoreConversation(conv, msgsOf(
			"dinner lunch birthday holiday party wedding",
		))
		if got.BusinessConfidence != 0 {
			t.Errorf("confidence = %v, want 0", got.BusinessConfidence)
		}
		if got.Classification != model.ConvPersonal {
			t.Errorf("classification = %q, want personal", got.Classification)
		}
		if !strings.Contains(got.Reason, "chat name looks personal") {
			t.Errorf("reason %q does not mention personal chat name", got.Reason)
		}
	})

	t.Run("no signals stays neutral unknown", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_n", Title: "Sam"}
		got := c.ScoreConversation(conv, msgsOf("ok", "see you"))
		if got.BusinessConfidence != 50 {
			t.Errorf("confidence = %v, want 50", got.BusinessConfidence)
		}
		if got.Classification != model.ConvUnknown {
			t.Errorf("classification = %q, want unknown", got.Classification)
		}
		if got.Reason != "No business or personal signals found" {
			t.Errorf("reason = %q", got.Reason)
		}
	})

	t.Run("empty conversation is unknown", func(t *testing.T) {
		conv := model.Conversation{ID: "conv_e", Title: "Sam"}
		got := c.ScoreConversation(conv, nil)
		if got.Classification != model.ConvUnknown || got.BusinessConfidence != 50 {
			t.Errorf("got %q/%v, want unknown/50", got.Classification, got.BusinessConfidence)
		}
	})
}

func TestReasonListsAtMostFiveSignals(t *testing.T) {
	c := New(model.BusinessProfile{}, nil)
	got := c.ScoreMessage(model.Message{
		Body: "price quote invoice booking deadline payment deposit refund",
	})
	listed := strings.TrimPrefix(got.Reason, "Mentions ")
	if listed == got.Reason {
		t.Fatalf("reason = %q, want it to start with Mentions", got.Reason)
	}
	if n := len(strings.Split(listed, ", ")); n > 5 {
		t.Errorf("reason lists %d signals, want <= 5: %q", n, got.Reason)
	}
}

func TestDeterminism(t *testing.T) {
	profile := model.BusinessProfile{BusinessKeywords: []string{"kiln"}}
	conv := model.Conversation{ID: "conv_d", Title: "Alex"}
	msgs := msgsOf("dinner on friday?", "can you quote me for a landing page?", "send me the invoice", "party next week")
	first := New(profile, nil).ScoreConversation(conv, msgs)
	for i := 0; i < 10; i++ {
		got := New(profile, nil).ScoreConversation(conv, msgs)
		if got != first {
			t.Fatalf("run %d differs: %+v vs %+v", i, got, first)
		}
	}
}
