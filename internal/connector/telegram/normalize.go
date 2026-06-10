// Package telegram is the Telegram Bot API connector. It validates the bot
// token, pulls updates via getUpdates (one-shot or long-poll), normalizes
// them into the shared message shape (spec §5) and stores them through
// internal/store.
package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// Normalized is the output of NormalizeUpdate: the conversation, customer and
// message derived from one Telegram update. Message.ConversationID and
// Message.CustomerID are filled in by the caller after upserting.
type Normalized struct {
	Conversation model.Conversation
	Customer     model.Customer
	Message      model.Message
}

// Telegram Bot API wire shapes (only the fields we read).
type tgUpdate struct {
	UpdateID      int64      `json:"update_id"`
	Message       *tgMessage `json:"message"`
	EditedMessage *tgMessage `json:"edited_message"`
}

type tgMessage struct {
	MessageID int64      `json:"message_id"`
	From      *tgUser    `json:"from"`
	Chat      tgChat     `json:"chat"`
	Date      int64      `json:"date"`
	Text      string     `json:"text"`
	Caption   string     `json:"caption"`
	ReplyTo   *tgMessage `json:"reply_to_message"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgChat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"` // private, group, supergroup, channel
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// NormalizeUpdate converts one raw Telegram update into the normalized
// conversation/customer/message triple (spec §5). It handles message and
// edited_message updates carrying text or a caption; everything else is
// skipped by returning (nil, nil).
func NormalizeUpdate(raw json.RawMessage, connectorID, workspaceID string) (*Normalized, error) {
	var u tgUpdate
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, fmt.Errorf("parse telegram update: %w", err)
	}
	msg := u.Message
	if msg == nil {
		msg = u.EditedMessage
	}
	if msg == nil {
		return nil, nil
	}
	body := msg.Text
	if body == "" {
		body = msg.Caption
	}
	if body == "" {
		return nil, nil
	}

	from := msg.From
	if from == nil {
		from = &tgUser{ID: msg.Chat.ID, FirstName: msg.Chat.FirstName, LastName: msg.Chat.LastName}
	}
	senderName := strings.TrimSpace(from.FirstName + " " + from.LastName)

	isGroup := msg.Chat.Type != "private"
	title := msg.Chat.Title
	if !isGroup {
		title = senderName
	}

	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	messageID := strconv.FormatInt(msg.MessageID, 10)
	senderID := strconv.FormatInt(from.ID, 10)
	occurred := time.Unix(msg.Date, 0).UTC()

	var replyTo string
	if msg.ReplyTo != nil {
		replyTo = strconv.FormatInt(msg.ReplyTo.MessageID, 10)
	}

	return &Normalized{
		Conversation: model.Conversation{
			WorkspaceID:   workspaceID,
			ConnectorID:   connectorID,
			Channel:       model.ChannelTelegram,
			ExternalID:    chatID,
			Title:         title,
			IsGroup:       isGroup,
			LastMessageAt: occurred,
		},
		Customer: model.Customer{
			WorkspaceID: workspaceID,
			Channel:     model.ChannelTelegram,
			ExternalID:  senderID,
			Name:        senderName,
			Handle:      from.Username,
		},
		Message: model.Message{
			WorkspaceID:              workspaceID,
			Channel:                  model.ChannelTelegram,
			Provider:                 model.ProviderTelegramBotAPI,
			ConnectorID:              connectorID,
			ConversationExternalID:   chatID,
			MessageExternalID:        messageID,
			SenderExternalID:         senderID,
			SenderName:               senderName,
			SenderHandle:             from.Username,
			Body:                     body,
			BodyFormat:               "plain_text",
			Direction:                model.DirectionInbound,
			OccurredAt:               occurred,
			ReplyToExternalMessageID: replyTo,
			RawJSON:                  append([]byte(nil), raw...),
			DedupeKey: fmt.Sprintf("%s:%s:%s:%s",
				model.ProviderTelegramBotAPI, connectorID, chatID, messageID),
		},
	}, nil
}
