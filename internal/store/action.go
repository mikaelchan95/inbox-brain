package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// ActionFilter narrows ListActions.
type ActionFilter struct {
	Status         string    // optional: open|done|dismissed|snoozed
	Type           string    // optional
	ConversationID string    // optional
	CreatedAfter   time.Time // optional (zero = ignore)
	Limit          int
}

const actionCols = `id, workspace_id, conversation_id, message_id, customer_id, type, title, summary,
	suggested_reply, confidence, urgency, status, snoozed_until, source, created_at, updated_at`

func scanAction(scan func(dest ...any) error) (model.Action, error) {
	var a model.Action
	var snoozed, created, updated int64
	if err := scan(&a.ID, &a.WorkspaceID, &a.ConversationID, &a.MessageID, &a.CustomerID,
		&a.Type, &a.Title, &a.Summary, &a.SuggestedReply, &a.Confidence, &a.Urgency,
		&a.Status, &snoozed, &a.Source, &created, &updated); err != nil {
		return model.Action{}, err
	}
	a.SnoozedUntil = fromMillis(snoozed)
	a.CreatedAt = fromMillis(created)
	a.UpdatedAt = fromMillis(updated)
	return a, nil
}

// CreateAction stores a new action and returns it with id/status/times filled.
func (s *Store) CreateAction(a model.Action) (model.Action, error) {
	now := time.Now()
	if a.ID == "" {
		a.ID = model.NewID("act")
	}
	if a.Status == "" {
		a.Status = model.StatusOpen
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	if a.UpdatedAt.IsZero() {
		a.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO actions (`+actionCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.WorkspaceID, a.ConversationID, a.MessageID, a.CustomerID,
		a.Type, a.Title, a.Summary, a.SuggestedReply, a.Confidence, a.Urgency,
		a.Status, millis(a.SnoozedUntil), a.Source, millis(a.CreatedAt), millis(a.UpdatedAt),
	); err != nil {
		return model.Action{}, fmt.Errorf("create action: %w", err)
	}
	return a, nil
}

// GetAction returns the action with the given id, or (nil, nil) when it does
// not exist.
func (s *Store) GetAction(id string) (*model.Action, error) {
	row := s.DB.QueryRow(`SELECT `+actionCols+` FROM actions WHERE id = ?`, id)
	a, err := scanAction(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get action %s: %w", id, err)
	}
	return &a, nil
}

// UpdateActionStatus sets an action's status; snoozed_until is cleared unless
// the new status is snoozed.
func (s *Store) UpdateActionStatus(id, status string) error {
	query := `UPDATE actions SET status = ?, snoozed_until = 0, updated_at = ? WHERE id = ?`
	if status == model.StatusSnoozed {
		query = `UPDATE actions SET status = ?, updated_at = ? WHERE id = ?`
	}
	if _, err := s.DB.Exec(query, status, millis(time.Now()), id); err != nil {
		return fmt.Errorf("update action status %s: %w", id, err)
	}
	return nil
}

// SnoozeAction sets an action to snoozed until the given time.
func (s *Store) SnoozeAction(id string, until time.Time) error {
	if _, err := s.DB.Exec(
		`UPDATE actions SET status = ?, snoozed_until = ?, updated_at = ? WHERE id = ?`,
		model.StatusSnoozed, millis(until), millis(time.Now()), id,
	); err != nil {
		return fmt.Errorf("snooze action %s: %w", id, err)
	}
	return nil
}

// ListActions returns actions matching the filter, newest first. Snoozed
// actions whose snooze deadline has passed are woken (set back to open) first,
// so every consumer — dashboard, CLI, leaks — sees them reappear.
func (s *Store) ListActions(f ActionFilter) ([]model.Action, error) {
	now := millis(time.Now())
	if _, err := s.DB.Exec(
		`UPDATE actions SET status = ?, snoozed_until = 0, updated_at = ?
		 WHERE status = ? AND snoozed_until > 0 AND snoozed_until <= ?`,
		model.StatusOpen, now, model.StatusSnoozed, now,
	); err != nil {
		return nil, fmt.Errorf("wake snoozed actions: %w", err)
	}
	query := `SELECT ` + actionCols + ` FROM actions`
	var where []string
	var args []any
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	if f.Type != "" {
		where = append(where, `type = ?`)
		args = append(args, f.Type)
	}
	if f.ConversationID != "" {
		where = append(where, `conversation_id = ?`)
		args = append(args, f.ConversationID)
	}
	if !f.CreatedAfter.IsZero() {
		where = append(where, `created_at > ?`)
		args = append(args, millis(f.CreatedAfter))
	}
	for i, w := range where {
		if i == 0 {
			query += ` WHERE ` + w
		} else {
			query += ` AND ` + w
		}
	}
	query += ` ORDER BY created_at DESC, id ASC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	defer rows.Close()
	var out []model.Action
	for rows.Next() {
		a, err := scanAction(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}
	return out, nil
}

// ActionExistsForMessage reports whether any action is anchored to the given
// message.
func (s *Store) ActionExistsForMessage(messageID string) (bool, error) {
	var exists int
	if err := s.DB.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM actions WHERE message_id = ?)`, messageID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("action exists for message %s: %w", messageID, err)
	}
	return exists != 0, nil
}

// DeleteActionsForConversation removes every action derived from a
// conversation and returns how many were deleted.
func (s *Store) DeleteActionsForConversation(conversationID string) (int, error) {
	res, err := s.DB.Exec(`DELETE FROM actions WHERE conversation_id = ?`, conversationID)
	if err != nil {
		return 0, fmt.Errorf("delete actions for conversation %s: %w", conversationID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete actions for conversation %s: %w", conversationID, err)
	}
	return int(n), nil
}

// DeleteLeadsForConversation removes leads derived from a conversation and
// returns how many were deleted.
func (s *Store) DeleteLeadsForConversation(conversationID string) (int, error) {
	res, err := s.DB.Exec(`DELETE FROM leads WHERE conversation_id = ?`, conversationID)
	if err != nil {
		return 0, fmt.Errorf("delete leads for conversation %s: %w", conversationID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete leads for conversation %s: %w", conversationID, err)
	}
	return int(n), nil
}

const leadCols = `id, workspace_id, conversation_id, customer_id, action_id, status, summary, created_at, updated_at`

func scanLead(scan func(dest ...any) error) (model.Lead, error) {
	var l model.Lead
	var created, updated int64
	if err := scan(&l.ID, &l.WorkspaceID, &l.ConversationID, &l.CustomerID, &l.ActionID,
		&l.Status, &l.Summary, &created, &updated); err != nil {
		return model.Lead{}, err
	}
	l.CreatedAt = fromMillis(created)
	l.UpdatedAt = fromMillis(updated)
	return l, nil
}

// UpsertLead inserts a lead or, when one already exists for the conversation,
// updates it in place keeping the earliest created_at (and the existing id).
// The stored row is returned.
func (s *Store) UpsertLead(l model.Lead) (model.Lead, error) {
	now := time.Now()
	if l.ID == "" {
		l.ID = model.NewID("lead")
	}
	if l.Status == "" {
		l.Status = model.LeadOpen
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = now
	}
	if l.UpdatedAt.IsZero() {
		l.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO leads (`+leadCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(conversation_id) DO UPDATE SET
		   customer_id = excluded.customer_id,
		   action_id = excluded.action_id,
		   status = excluded.status,
		   summary = excluded.summary,
		   updated_at = excluded.updated_at`,
		l.ID, l.WorkspaceID, l.ConversationID, l.CustomerID, l.ActionID,
		l.Status, l.Summary, millis(l.CreatedAt), millis(l.UpdatedAt),
	); err != nil {
		return model.Lead{}, fmt.Errorf("upsert lead: %w", err)
	}
	row := s.DB.QueryRow(`SELECT `+leadCols+` FROM leads WHERE conversation_id = ?`, l.ConversationID)
	stored, err := scanLead(row.Scan)
	if err != nil {
		return model.Lead{}, fmt.Errorf("reload lead: %w", err)
	}
	return stored, nil
}

// ListLeads returns leads, newest first; status "" returns all.
func (s *Store) ListLeads(status string) ([]model.Lead, error) {
	query := `SELECT ` + leadCols + ` FROM leads`
	var args []any
	if status != "" {
		query += ` WHERE status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY created_at DESC, id ASC`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list leads: %w", err)
	}
	defer rows.Close()
	var out []model.Lead
	for rows.Next() {
		l, err := scanLead(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan lead: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list leads: %w", err)
	}
	return out, nil
}
