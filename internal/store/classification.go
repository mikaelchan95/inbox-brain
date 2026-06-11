package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

const convClassCols = `id, conversation_id, classification, business_confidence, source,
	COALESCE(reason, ''), reviewed_by_user, COALESCE(user_override, ''), created_at, updated_at`

func scanConvClassification(scan func(dest ...any) error) (model.ConversationClassification, error) {
	var c model.ConversationClassification
	var reviewed int
	var created, updated int64
	if err := scan(&c.ID, &c.ConversationID, &c.Classification, &c.BusinessConfidence,
		&c.Source, &c.Reason, &reviewed, &c.UserOverride, &created, &updated); err != nil {
		return model.ConversationClassification{}, err
	}
	c.ReviewedByUser = reviewed != 0
	c.CreatedAt = fromMillis(created)
	c.UpdatedAt = fromMillis(updated)
	return c, nil
}

// SaveConversationClassification upserts a classification by conversation_id.
// On update the existing id and created_at are preserved; everything else is
// replaced and updated_at is refreshed.
func (s *Store) SaveConversationClassification(c model.ConversationClassification) error {
	now := time.Now()
	if c.ID == "" {
		c.ID = model.NewID("cls")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO conversation_classifications
		   (id, conversation_id, classification, business_confidence, source, reason,
		    reviewed_by_user, user_override, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET
		   classification = excluded.classification,
		   business_confidence = excluded.business_confidence,
		   source = excluded.source,
		   reason = excluded.reason,
		   reviewed_by_user = excluded.reviewed_by_user,
		   user_override = excluded.user_override,
		   updated_at = excluded.updated_at`,
		c.ID, c.ConversationID, c.Classification, c.BusinessConfidence, c.Source, c.Reason,
		boolToInt(c.ReviewedByUser), c.UserOverride, millis(c.CreatedAt), millis(c.UpdatedAt),
	); err != nil {
		return fmt.Errorf("save conversation classification: %w", err)
	}
	return nil
}

// GetConversationClassification returns the classification for a
// conversation, or (nil, nil) when it does not exist.
func (s *Store) GetConversationClassification(conversationID string) (*model.ConversationClassification, error) {
	row := s.DB.QueryRow(
		`SELECT `+convClassCols+` FROM conversation_classifications WHERE conversation_id = ?`,
		conversationID,
	)
	c, err := scanConvClassification(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get conversation classification %s: %w", conversationID, err)
	}
	return &c, nil
}

// ListConversationClassifications returns every conversation classification,
// oldest first.
func (s *Store) ListConversationClassifications() ([]model.ConversationClassification, error) {
	rows, err := s.DB.Query(
		`SELECT ` + convClassCols + ` FROM conversation_classifications ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list conversation classifications: %w", err)
	}
	defer rows.Close()
	var out []model.ConversationClassification
	for rows.Next() {
		c, err := scanConvClassification(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan conversation classification: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list conversation classifications: %w", err)
	}
	return out, nil
}

// SetUserOverride records the user's verdict for a conversation: it sets
// user_override, reviewed_by_user=1 and source=user_override. When the
// conversation has no classification row yet, one is created (label unknown,
// confidence 0) so the override still takes effect.
func (s *Store) SetUserOverride(conversationID, override string) error {
	now := millis(time.Now())
	if _, err := s.DB.Exec(
		`INSERT INTO conversation_classifications
		   (id, conversation_id, classification, business_confidence, source, reason,
		    reviewed_by_user, user_override, created_at, updated_at)
		 VALUES (?, ?, ?, 0, ?, '', 1, ?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET
		   user_override = excluded.user_override,
		   reviewed_by_user = 1,
		   source = excluded.source,
		   updated_at = excluded.updated_at`,
		model.NewID("cls"), conversationID, model.ConvUnknown, model.SourceUserOverride,
		override, now, now,
	); err != nil {
		return fmt.Errorf("set user override %s: %w", conversationID, err)
	}
	return nil
}

// MarkReviewed flags a conversation classification as reviewed by the user
// without changing its label.
func (s *Store) MarkReviewed(conversationID string) error {
	if _, err := s.DB.Exec(
		`UPDATE conversation_classifications SET reviewed_by_user = 1, updated_at = ? WHERE conversation_id = ?`,
		millis(time.Now()), conversationID,
	); err != nil {
		return fmt.Errorf("mark reviewed %s: %w", conversationID, err)
	}
	return nil
}

// SaveMessageClassification upserts a message classification by message_id.
// On update the existing id and created_at are preserved.
func (s *Store) SaveMessageClassification(c model.MessageClassification) error {
	if c.ID == "" {
		c.ID = model.NewID("mcls")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if _, err := s.DB.Exec(
		`INSERT INTO message_classifications
		   (id, message_id, classification, business_confidence, reason, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(message_id) DO UPDATE SET
		   classification = excluded.classification,
		   business_confidence = excluded.business_confidence,
		   reason = excluded.reason,
		   source = excluded.source`,
		c.ID, c.MessageID, c.Classification, c.BusinessConfidence, c.Reason, c.Source,
		millis(c.CreatedAt),
	); err != nil {
		return fmt.Errorf("save message classification: %w", err)
	}
	return nil
}

// GetMessageClassification returns the classification for a message, or
// (nil, nil) when it does not exist.
func (s *Store) GetMessageClassification(messageID string) (*model.MessageClassification, error) {
	var c model.MessageClassification
	var created int64
	err := s.DB.QueryRow(
		`SELECT id, message_id, classification, business_confidence, COALESCE(reason, ''), source, created_at
		 FROM message_classifications WHERE message_id = ?`,
		messageID,
	).Scan(&c.ID, &c.MessageID, &c.Classification, &c.BusinessConfidence, &c.Reason, &c.Source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get message classification %s: %w", messageID, err)
	}
	c.CreatedAt = fromMillis(created)
	return &c, nil
}

// AddRule stores a new classification rule and returns it with id/created_at
// filled.
func (s *Store) AddRule(r model.ClassificationRule) (model.ClassificationRule, error) {
	if r.ID == "" {
		r.ID = model.NewID("rule")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	if _, err := s.DB.Exec(
		`INSERT INTO classification_rules (id, workspace_id, rule_type, pattern, action, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.WorkspaceID, r.RuleType, r.Pattern, r.Action, millis(r.CreatedAt),
	); err != nil {
		return model.ClassificationRule{}, fmt.Errorf("add rule: %w", err)
	}
	return r, nil
}

// ListRules returns all classification rules, oldest first.
func (s *Store) ListRules() ([]model.ClassificationRule, error) {
	rows, err := s.DB.Query(
		`SELECT id, workspace_id, rule_type, pattern, action, created_at
		 FROM classification_rules ORDER BY created_at ASC, id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	var out []model.ClassificationRule
	for rows.Next() {
		var r model.ClassificationRule
		var created int64
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.RuleType, &r.Pattern, &r.Action, &created); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		r.CreatedAt = fromMillis(created)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	return out, nil
}

// DeleteRule removes a classification rule by id.
func (s *Store) DeleteRule(id string) error {
	if _, err := s.DB.Exec(`DELETE FROM classification_rules WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete rule %s: %w", id, err)
	}
	return nil
}
