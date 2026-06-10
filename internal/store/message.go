package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

const messageCols = `id, workspace_id, conversation_id, customer_id, channel, provider, connector_id,
	conversation_external_id, message_external_id,
	sender_external_id, sender_name, sender_handle, sender_phone,
	body, body_format, direction, occurred_at, ingested_at,
	reply_to_external_message_id, media, raw_json, dedupe_key`

func scanMessage(scan func(dest ...any) error) (model.Message, error) {
	var m model.Message
	var occurred, ingested int64
	var media, raw string
	if err := scan(&m.ID, &m.WorkspaceID, &m.ConversationID, &m.CustomerID,
		&m.Channel, &m.Provider, &m.ConnectorID,
		&m.ConversationExternalID, &m.MessageExternalID,
		&m.SenderExternalID, &m.SenderName, &m.SenderHandle, &m.SenderPhone,
		&m.Body, &m.BodyFormat, &m.Direction, &occurred, &ingested,
		&m.ReplyToExternalMessageID, &media, &raw, &m.DedupeKey); err != nil {
		return model.Message{}, err
	}
	m.OccurredAt = fromMillis(occurred)
	m.IngestedAt = fromMillis(ingested)
	decoded, err := mediaFromJSON(media)
	if err != nil {
		return model.Message{}, err
	}
	m.Media = decoded
	if raw != "" {
		m.RawJSON = []byte(raw)
	}
	return m, nil
}

// InsertMessage stores a normalized message. It returns (false, nil) when a
// message with the same dedupe_key already exists.
func (s *Store) InsertMessage(m model.Message) (bool, error) {
	if m.ID == "" {
		m.ID = model.NewID("msg")
	}
	if m.IngestedAt.IsZero() {
		m.IngestedAt = time.Now()
	}
	media, err := mediaToJSON(m.Media)
	if err != nil {
		return false, fmt.Errorf("insert message: %w", err)
	}
	res, err := s.DB.Exec(
		`INSERT INTO messages (`+messageCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(dedupe_key) DO NOTHING`,
		m.ID, m.WorkspaceID, m.ConversationID, m.CustomerID,
		m.Channel, m.Provider, m.ConnectorID,
		m.ConversationExternalID, m.MessageExternalID,
		m.SenderExternalID, m.SenderName, m.SenderHandle, m.SenderPhone,
		m.Body, m.BodyFormat, m.Direction, millis(m.OccurredAt), millis(m.IngestedAt),
		m.ReplyToExternalMessageID, media, string(m.RawJSON), m.DedupeKey,
	)
	if err != nil {
		return false, fmt.Errorf("insert message: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert message: %w", err)
	}
	return n > 0, nil
}

// ListMessages returns a conversation's messages in chronological order.
// limit 0 returns all.
func (s *Store) ListMessages(conversationID string, limit int) ([]model.Message, error) {
	query := `SELECT ` + messageCols + ` FROM messages WHERE conversation_id = ? ORDER BY occurred_at ASC, id ASC`
	args := []any{conversationID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		m, err := scanMessage(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return out, nil
}

// GetMessage returns the message with the given id, or (nil, nil) when it
// does not exist.
func (s *Store) GetMessage(id string) (*model.Message, error) {
	row := s.DB.QueryRow(`SELECT `+messageCols+` FROM messages WHERE id = ?`, id)
	m, err := scanMessage(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get message %s: %w", id, err)
	}
	return &m, nil
}

// CountMessages returns the number of messages in a conversation.
func (s *Store) CountMessages(conversationID string) (int, error) {
	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE conversation_id = ?`, conversationID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count messages: %w", err)
	}
	return n, nil
}
