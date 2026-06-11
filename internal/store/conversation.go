package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// ConversationFilter narrows ListConversations.
type ConversationFilter struct {
	Channel        string // optional
	Classification string // optional: business|personal|mixed|unknown (joins classifications)
	Limit          int    // 0 = no limit
}

const conversationCols = `id, workspace_id, connector_id, channel, external_id, title, is_group, last_message_at, created_at, updated_at`

func scanConversation(scan func(dest ...any) error) (model.Conversation, error) {
	var c model.Conversation
	var isGroup int
	var last, created, updated int64
	if err := scan(&c.ID, &c.WorkspaceID, &c.ConnectorID, &c.Channel, &c.ExternalID,
		&c.Title, &isGroup, &last, &created, &updated); err != nil {
		return model.Conversation{}, err
	}
	c.IsGroup = isGroup != 0
	c.LastMessageAt = fromMillis(last)
	c.CreatedAt = fromMillis(created)
	c.UpdatedAt = fromMillis(updated)
	return c, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// UpsertConversation inserts a conversation or, when (connector_id,
// external_id) already exists, updates title (non-empty new titles win) and
// last_message_at (moves forward only). The stored row is returned.
func (s *Store) UpsertConversation(c model.Conversation) (model.Conversation, error) {
	now := time.Now()
	if c.ID == "" {
		c.ID = model.NewID("conv")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO conversations (`+conversationCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(connector_id, external_id) DO UPDATE SET
		   title = CASE WHEN excluded.title <> '' THEN excluded.title ELSE conversations.title END,
		   is_group = excluded.is_group,
		   last_message_at = CASE WHEN excluded.last_message_at > conversations.last_message_at
		                          THEN excluded.last_message_at ELSE conversations.last_message_at END,
		   updated_at = excluded.updated_at`,
		c.ID, c.WorkspaceID, c.ConnectorID, c.Channel, c.ExternalID,
		c.Title, boolToInt(c.IsGroup), millis(c.LastMessageAt), millis(c.CreatedAt), millis(c.UpdatedAt),
	); err != nil {
		return model.Conversation{}, fmt.Errorf("upsert conversation: %w", err)
	}
	row := s.DB.QueryRow(
		`SELECT `+conversationCols+` FROM conversations WHERE connector_id = ? AND external_id = ?`,
		c.ConnectorID, c.ExternalID,
	)
	stored, err := scanConversation(row.Scan)
	if err != nil {
		return model.Conversation{}, fmt.Errorf("reload conversation: %w", err)
	}
	return stored, nil
}

// GetConversation returns the conversation with the given id, or (nil, nil)
// when it does not exist.
func (s *Store) GetConversation(id string) (*model.Conversation, error) {
	row := s.DB.QueryRow(`SELECT `+conversationCols+` FROM conversations WHERE id = ?`, id)
	c, err := scanConversation(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation %s: %w", id, err)
	}
	return &c, nil
}

// ListConversations returns conversations matching the filter, newest
// last_message_at first. Filtering by Classification joins the
// classification table and matches the effective label (user_override when
// set, otherwise the classifier label).
func (s *Store) ListConversations(f ConversationFilter) ([]model.Conversation, error) {
	query := `SELECT c.id, c.workspace_id, c.connector_id, c.channel, c.external_id,
	                 c.title, c.is_group, c.last_message_at, c.created_at, c.updated_at
	          FROM conversations c`
	var where []string
	var args []any
	if f.Classification != "" {
		query += ` JOIN conversation_classifications cc ON cc.conversation_id = c.id`
		where = append(where,
			`CASE WHEN COALESCE(cc.user_override, '') <> '' THEN cc.user_override ELSE cc.classification END = ?`)
		args = append(args, f.Classification)
	}
	if f.Channel != "" {
		where = append(where, `c.channel = ?`)
		args = append(args, f.Channel)
	}
	for i, w := range where {
		if i == 0 {
			query += ` WHERE ` + w
		} else {
			query += ` AND ` + w
		}
	}
	query += ` ORDER BY c.last_message_at DESC, c.id ASC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()
	var out []model.Conversation
	for rows.Next() {
		c, err := scanConversation(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	return out, nil
}
