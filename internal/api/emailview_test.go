package api

import (
	"strings"
	"testing"
)

func TestParseEmailBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		subject     string
		visible     string
		quotedHas   string // substring expected in quoted; "" means no quote split
		visibleMiss string // substring that must NOT remain visible
	}{
		{
			name:    "plain message no quotes",
			body:    "Subject: Hello\n\nHi,\n\nCan we book Friday?\n\nThanks",
			subject: "Hello",
			visible: "Hi,\n\nCan we book Friday?\n\nThanks",
		},
		{
			name:        "gmail attribution",
			body:        "Subject: Re: Quote\n\nSounds good.\n\nOn Fri, 10 Apr 2026 at 13:56, Judy <judy@x.com> wrote:\n> Earlier text\n> more",
			subject:     "Re: Quote",
			visible:     "Sounds good.",
			quotedHas:   "Earlier text",
			visibleMiss: "wrote:",
		},
		{
			name:        "wrapped attribution",
			body:        "Thanks!\n\nOn Fri, 10 Apr 2026 at 13:56, Judy | The Wedding Vow <\njudy@x.com> wrote:\n> hi",
			visible:     "Thanks!",
			quotedHas:   "judy@x.com",
			visibleMiss: "wrote:",
		},
		{
			name:        "outlook header block",
			body:        "Subject: Re: Feature\n\nHi, keen to proceed.\n\nFrom: Judy | The Wedding Vow <judy@x.com>\nTo: \"ourmet\"<gourmet@y.sg>\nCc: \"TWV\"<contact@x.com>\nDate: Fri, 10 Apr 2026 13:56:01 +0800\nSubject: Re: Feature\nHi, earlier message body here.",
			subject:     "Re: Feature",
			visible:     "Hi, keen to proceed.",
			quotedHas:   "earlier message body",
			visibleMiss: "Date: Fri",
		},
		{
			name:      "single-line header block",
			body:      "New text.\n\nFrom: Judy <judy@x.com> To: \"g\"<g@y.sg> Date: Wed, 25 Mar 2026 09:16:00 +0800 Subject: Re: Feature old text",
			visible:   "New text.",
			quotedHas: "old text",
		},
		{
			name:      "original message divider",
			body:      "Subject: RE: Invoice\n\nPaid today.\n\n-----Original Message-----\nFrom: x\nolder",
			subject:   "RE: Invoice",
			visible:   "Paid today.",
			quotedHas: "older",
		},
		{
			name:      "begin forwarded message",
			body:      "FYI\n\nBegin forwarded message:\nFrom: someone\nbody",
			visible:   "FYI",
			quotedHas: "From: someone",
		},
		{
			name:      "trailing gt block",
			body:      "Agreed.\n\n> old line one\n> old line two",
			visible:   "Agreed.",
			quotedHas: "old line one",
		},
		{
			name:    "inline reply gt lines kept",
			body:    "> what time?\n3pm works.\n> where?\nAt the bar.",
			visible: "> what time?\n3pm works.\n> where?\nAt the bar.",
		},
		{
			name:    "on-wrote sentence without a date stays visible",
			body:    "On reflection, I agree with what Sarah wrote:\nLet's go with the buffet option.",
			visible: "On reflection, I agree with what Sarah wrote:\nLet's go with the buffet option.",
		},
		{
			name:    "prose from/sent/to without subject stays visible",
			body:    "From: our side all is confirmed.\nWe sent: the deposit invoice to: your accountant today.\nThanks!",
			visible: "From: our side all is confirmed.\nWe sent: the deposit invoice to: your accountant today.\nThanks!",
		},
		{
			name:      "quote only no new text",
			body:      "On Mon, 1 Jan 2026 at 10:00, A <a@b.c> wrote:\n> hello",
			visible:   "",
			quotedHas: "hello",
		},
		{
			name:    "mailto does not trigger header block",
			body:    "From: our side everything is ready.\nSee mailto:info@x.com for details.\nNo Subject or Date headers here... wait, none.",
			visible: "From: our side everything is ready.\nSee mailto:info@x.com for details.\nNo Subject or Date headers here... wait, none.",
		},
		{
			name:    "blank line runs collapsed",
			body:    "A\n\n\n\n\nB",
			visible: "A\n\nB",
		},
		{
			name:    "crlf and nbsp-only lines collapsed",
			body:    "Hi there,\r\n\r\n\r\n\r\nGood day!\r\n\r\n \r\n\r\nBye.",
			visible: "Hi there,\n\nGood day!\n\nBye.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, visible, quoted := parseEmailBody(tt.body)
			if subject != tt.subject {
				t.Errorf("subject = %q, want %q", subject, tt.subject)
			}
			if visible != tt.visible {
				t.Errorf("visible = %q, want %q", visible, tt.visible)
			}
			if tt.quotedHas == "" {
				if quoted != "" {
					t.Errorf("quoted = %q, want empty", quoted)
				}
			} else if !strings.Contains(quoted, tt.quotedHas) {
				t.Errorf("quoted = %q, want substring %q", quoted, tt.quotedHas)
			}
			if tt.visibleMiss != "" && strings.Contains(visible, tt.visibleMiss) {
				t.Errorf("visible = %q, must not contain %q", visible, tt.visibleMiss)
			}
		})
	}
}

func TestNormalizeSubject(t *testing.T) {
	for in, want := range map[string]string{
		"Re: Re: FW: Booking": "booking",
		"Booking":             "booking",
		"  Fwd: re: Hi ":      "hi",
	} {
		if got := normalizeSubject(in); got != want {
			t.Errorf("normalizeSubject(%q) = %q, want %q", in, got, want)
		}
	}
}
