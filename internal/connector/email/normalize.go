// Package email is the IMAP connector. It works with any IMAP server —
// self-hosted domains as well as Gmail, Yahoo and iCloud (via app passwords) —
// pulls new messages from one folder per account, normalizes them into the
// shared message shape (spec §5) and stores them through internal/store.
package email

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"regexp"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-message/mail"

	"github.com/mikaelchan95/inbox-brain/internal/model"

	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 messages
)

// Normalized is the output of NormalizeEmail: the conversation, customer and
// message derived from one email. Message.ConversationID and
// Message.CustomerID are filled in by the caller after upserting.
type Normalized struct {
	Conversation model.Conversation
	Customer     model.Customer
	Message      model.Message
}

// rawSummary is stored in Message.RawJSON instead of the full RFC 822 source,
// which can be megabytes once attachments are included.
type rawSummary struct {
	UID         uint32 `json:"uid"`
	UIDValidity uint32 `json:"uidValidity"`
	MessageID   string `json:"messageId,omitempty"`
	Subject     string `json:"subject,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Date        string `json:"date,omitempty"`
}

// NormalizeEmail converts one raw RFC 822 message into the normalized
// conversation/customer/message triple (spec §5). Conversations group by
// counterpart address: the sender for inbound mail, the first recipient when
// the account owner sent the message. Unparseable messages are skipped by
// returning (nil, nil).
func NormalizeEmail(raw []byte, uid imap.UID, uidValidity uint32, internalDate time.Time, account Account, connectorID, workspaceID string) (*Normalized, error) {
	mr, err := mail.CreateReader(bytes.NewReader(raw))
	if mr == nil {
		return nil, nil // malformed beyond repair (often spam); skip
	}
	defer mr.Close()
	_ = err // a non-nil reader is usable despite e.g. unknown-charset errors

	from := firstAddress(mr.Header, "From")
	to := firstAddress(mr.Header, "To")
	subject, _ := mr.Header.Subject()

	occurred, dateErr := mr.Header.Date()
	if dateErr != nil || occurred.IsZero() {
		occurred = internalDate
	}
	occurred = occurred.UTC()

	body, media := readParts(mr)
	body = strings.TrimSpace(body)
	if body == "" && subject == "" {
		return nil, nil // nothing classifiable
	}
	if subject != "" {
		body = strings.TrimSpace("Subject: " + subject + "\n\n" + body)
	}

	// The counterpart is who the account owner talks to: the sender for
	// inbound mail, the first recipient for mail sent by the account itself.
	self := strings.ToLower(account.Address)
	direction := model.DirectionInbound
	counterpart := from
	if from != nil && strings.ToLower(from.Address) == self {
		direction = model.DirectionOutbound
		counterpart = to
	}
	if counterpart == nil || counterpart.Address == "" {
		return nil, nil // no usable correspondent address
	}
	counterpartAddr := strings.ToLower(counterpart.Address)
	counterpartName := strings.TrimSpace(counterpart.Name)
	title := counterpartName
	if title == "" {
		title = counterpartAddr
	}

	messageID, _ := mr.Header.MessageID()
	if messageID == "" {
		messageID = fmt.Sprintf("uid:%d:%d", uidValidity, uid)
	}
	var replyTo string
	if ids, err := mr.Header.MsgIDList("In-Reply-To"); err == nil && len(ids) > 0 {
		replyTo = ids[0]
	}

	var senderName, senderAddr string
	if from != nil {
		senderName = strings.TrimSpace(from.Name)
		senderAddr = strings.ToLower(from.Address)
	}

	rawJSON, _ := json.Marshal(rawSummary{
		UID:         uint32(uid),
		UIDValidity: uidValidity,
		MessageID:   messageID,
		Subject:     subject,
		From:        senderAddr,
		To:          addrString(to),
		Date:        occurred.Format(time.RFC3339),
	})

	return &Normalized{
		Conversation: model.Conversation{
			WorkspaceID:   workspaceID,
			ConnectorID:   connectorID,
			Channel:       model.ChannelEmail,
			ExternalID:    counterpartAddr,
			Title:         title,
			IsGroup:       false,
			LastMessageAt: occurred,
		},
		Customer: model.Customer{
			WorkspaceID: workspaceID,
			Channel:     model.ChannelEmail,
			ExternalID:  counterpartAddr,
			Name:        counterpartName,
			Handle:      counterpartAddr,
		},
		Message: model.Message{
			WorkspaceID:              workspaceID,
			Channel:                  model.ChannelEmail,
			Provider:                 model.ProviderIMAP,
			ConnectorID:              connectorID,
			ConversationExternalID:   counterpartAddr,
			MessageExternalID:        messageID,
			SenderExternalID:         senderAddr,
			SenderName:               senderName,
			SenderHandle:             senderAddr,
			Body:                     body,
			BodyFormat:               "plain_text",
			Direction:                direction,
			OccurredAt:               occurred,
			ReplyToExternalMessageID: replyTo,
			Media:                    media,
			RawJSON:                  rawJSON,
			DedupeKey: fmt.Sprintf("%s:%s:%s:%s",
				model.ProviderIMAP, connectorID, counterpartAddr, messageID),
		},
	}, nil
}

// firstAddress returns the first address in a header field, or nil.
func firstAddress(h mail.Header, key string) *mail.Address {
	list, err := h.AddressList(key)
	if err != nil || len(list) == 0 {
		return nil
	}
	return list[0]
}

func addrString(a *mail.Address) string {
	if a == nil {
		return ""
	}
	return strings.ToLower(a.Address)
}

// readParts walks the MIME parts collecting the best text body (text/plain
// preferred, stripped text/html as fallback) and the attachment list.
func readParts(mr *mail.Reader) (string, []model.MessageMedia) {
	var plain, htmlBody string
	var media []model.MessageMedia
	for {
		p, err := mr.NextPart()
		if err == io.EOF || p == nil {
			break
		}
		if err != nil {
			continue // skip the malformed part, keep the rest
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			switch {
			case ct == "text/plain" && plain == "":
				b, _ := io.ReadAll(p.Body)
				plain = string(b)
			case ct == "text/html" && htmlBody == "":
				b, _ := io.ReadAll(p.Body)
				htmlBody = string(b)
			}
		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			ct, _, _ := h.ContentType()
			media = append(media, model.MessageMedia{
				Type:     mediaType(ct),
				FileName: filename,
				MimeType: ct,
			})
		}
	}
	if plain != "" {
		return plain, media
	}
	return htmlToText(htmlBody), media
}

// mediaType maps a MIME type onto the model's media type vocabulary.
func mediaType(contentType string) string {
	t, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		t = contentType
	}
	switch {
	case strings.HasPrefix(t, "image/"):
		return "image"
	case strings.HasPrefix(t, "video/"):
		return "video"
	case strings.HasPrefix(t, "audio/"):
		return "audio"
	default:
		return "document"
	}
}

var (
	htmlHiddenRE = regexp.MustCompile(`(?is)<(script|style|head)\b.*?</(script|style|head)>`)
	htmlTagRE    = regexp.MustCompile(`(?s)<[^>]*>`)
	blankLinesRE = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a crude HTML-to-text conversion: it drops script/style/head
// blocks, breaks block elements onto lines, strips the remaining tags and
// unescapes entities. Good enough for classification, not for display.
func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	s = htmlHiddenRE.ReplaceAllString(s, " ")
	for _, tag := range []string{"</p>", "</div>", "</tr>", "</li>", "<br>", "<br/>", "<br />"} {
		s = strings.ReplaceAll(s, tag, tag+"\n")
	}
	s = htmlTagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	s = strings.Join(lines, "\n")
	s = blankLinesRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
