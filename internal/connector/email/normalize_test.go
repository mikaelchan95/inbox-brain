package email

import (
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// --- fixtures -------------------------------------------------------------

const plainInbound = "From: Mrs Tan <Mrs.Tan@example.com>\r\n" +
	"To: Inbox <inbox@thewinery.com.sg>\r\n" +
	"Subject: Wine tasting booking\r\n" +
	"Date: Mon, 09 Jun 2025 10:00:00 +0800\r\n" +
	"Message-ID: <abc123@example.com>\r\n" +
	"In-Reply-To: <prev99@thewinery.com.sg>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Hi, can I book a tasting for 6 pax this Saturday?\r\n"

const htmlWithAttachment = "From: orders@vendor.example\r\n" +
	"To: inbox@thewinery.com.sg\r\n" +
	"Subject: Your quote\r\n" +
	"Date: Mon, 09 Jun 2025 11:00:00 +0800\r\n" +
	"Message-ID: <q1@vendor.example>\r\n" +
	"Content-Type: multipart/mixed; boundary=\"b1\"\r\n" +
	"\r\n" +
	"--b1\r\n" +
	"Content-Type: text/html; charset=utf-8\r\n" +
	"\r\n" +
	"<html><head><style>p{color:red}</style></head><body><p>Hello <b>there</b></p><p>Quote attached &amp; ready</p></body></html>\r\n" +
	"--b1\r\n" +
	"Content-Type: application/pdf; name=\"quote.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"quote.pdf\"\r\n" +
	"\r\n" +
	"%PDF-fake\r\n" +
	"--b1--\r\n"

const outboundFromSelf = "From: Inbox <inbox@thewinery.com.sg>\r\n" +
	"To: Mrs Tan <mrs.tan@example.com>\r\n" +
	"Subject: Re: Wine tasting booking\r\n" +
	"Date: Mon, 09 Jun 2025 12:00:00 +0800\r\n" +
	"Message-ID: <reply1@thewinery.com.sg>\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Saturday 3pm is confirmed.\r\n"

const noMessageID = "From: walkin@example.com\r\n" +
	"To: inbox@thewinery.com.sg\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"Are you open on Sunday?\r\n"

// --- NormalizeEmail -------------------------------------------------------

func testAccount() Account {
	return Account{Address: "inbox@thewinery.com.sg", Host: "imap.thewinery.com.sg"}
}

func TestNormalizeEmail(t *testing.T) {
	const connID = "conn_test1"
	const wsID = "ws_test1"
	internalDate := time.Date(2025, 6, 9, 5, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		raw     string
		wantNil bool
		check   func(t *testing.T, n *Normalized)
	}{
		{
			name: "plain inbound",
			raw:  plainInbound,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Conversation.ExternalID, "mrs.tan@example.com"; got != want {
					t.Errorf("Conversation.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Conversation.Title, "Mrs Tan"; got != want {
					t.Errorf("Conversation.Title = %q, want %q", got, want)
				}
				if got, want := n.Conversation.Channel, model.ChannelEmail; got != want {
					t.Errorf("Conversation.Channel = %q, want %q", got, want)
				}
				if n.Conversation.IsGroup {
					t.Error("Conversation.IsGroup = true, want false")
				}
				if got, want := n.Customer.ExternalID, "mrs.tan@example.com"; got != want {
					t.Errorf("Customer.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Message.Direction, model.DirectionInbound; got != want {
					t.Errorf("Message.Direction = %q, want %q", got, want)
				}
				if !strings.HasPrefix(n.Message.Body, "Subject: Wine tasting booking\n\n") {
					t.Errorf("Message.Body = %q, want subject prefix", n.Message.Body)
				}
				if !strings.Contains(n.Message.Body, "6 pax") {
					t.Errorf("Message.Body = %q, want booking text", n.Message.Body)
				}
				if got, want := n.Message.MessageExternalID, "abc123@example.com"; got != want {
					t.Errorf("Message.MessageExternalID = %q, want %q", got, want)
				}
				if got, want := n.Message.ReplyToExternalMessageID, "prev99@thewinery.com.sg"; got != want {
					t.Errorf("Message.ReplyToExternalMessageID = %q, want %q", got, want)
				}
				if got, want := n.Message.OccurredAt, time.Date(2025, 6, 9, 2, 0, 0, 0, time.UTC); !got.Equal(want) {
					t.Errorf("Message.OccurredAt = %v, want %v", got, want)
				}
				wantKey := "imap:conn_test1:mrs.tan@example.com:abc123@example.com"
				if got := n.Message.DedupeKey; got != wantKey {
					t.Errorf("Message.DedupeKey = %q, want %q", got, wantKey)
				}
			},
		},
		{
			name: "html body with attachment",
			raw:  htmlWithAttachment,
			check: func(t *testing.T, n *Normalized) {
				if strings.Contains(n.Message.Body, "<") || strings.Contains(n.Message.Body, "color:red") {
					t.Errorf("Message.Body = %q, want HTML stripped", n.Message.Body)
				}
				if !strings.Contains(n.Message.Body, "Hello there") {
					t.Errorf("Message.Body = %q, want text from HTML", n.Message.Body)
				}
				if !strings.Contains(n.Message.Body, "Quote attached & ready") {
					t.Errorf("Message.Body = %q, want unescaped entities", n.Message.Body)
				}
				if len(n.Message.Media) != 1 {
					t.Fatalf("len(Message.Media) = %d, want 1", len(n.Message.Media))
				}
				m := n.Message.Media[0]
				if m.Type != "document" || m.FileName != "quote.pdf" {
					t.Errorf("Media[0] = %+v, want document quote.pdf", m)
				}
			},
		},
		{
			name: "outbound from self",
			raw:  outboundFromSelf,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Message.Direction, model.DirectionOutbound; got != want {
					t.Errorf("Message.Direction = %q, want %q", got, want)
				}
				if got, want := n.Conversation.ExternalID, "mrs.tan@example.com"; got != want {
					t.Errorf("Conversation.ExternalID = %q, want counterpart %q", got, want)
				}
			},
		},
		{
			name: "missing message-id falls back to uid",
			raw:  noMessageID,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Message.MessageExternalID, "uid:777:42"; got != want {
					t.Errorf("Message.MessageExternalID = %q, want %q", got, want)
				}
				if !n.Message.OccurredAt.Equal(internalDate) {
					t.Errorf("Message.OccurredAt = %v, want internal date %v", n.Message.OccurredAt, internalDate)
				}
			},
		},
		{
			name:    "garbage is skipped",
			raw:     "not an email at all",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := NormalizeEmail([]byte(tt.raw), 42, 777, internalDate, testAccount(), connID, wsID)
			if err != nil {
				t.Fatalf("NormalizeEmail() error = %v", err)
			}
			if tt.wantNil {
				if n != nil {
					t.Fatalf("NormalizeEmail() = %+v, want nil", n)
				}
				return
			}
			if n == nil {
				t.Fatal("NormalizeEmail() = nil, want normalized message")
			}
			tt.check(t, n)
		})
	}
}

func TestDefaultHost(t *testing.T) {
	tests := []struct {
		address string
		want    string
	}{
		{"someone@gmail.com", "imap.gmail.com"},
		{"someone@Yahoo.com", "imap.mail.yahoo.com"},
		{"inbox@thewinery.com.sg", ""},
		{"not-an-address", ""},
	}
	for _, tt := range tests {
		if got := DefaultHost(tt.address); got != tt.want {
			t.Errorf("DefaultHost(%q) = %q, want %q", tt.address, got, tt.want)
		}
	}
}
