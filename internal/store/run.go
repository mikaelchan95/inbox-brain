package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// CreateExtractionRun stores a new extraction run and returns it with
// id/status/started_at filled.
func (s *Store) CreateExtractionRun(r model.ExtractionRun) (model.ExtractionRun, error) {
	if r.ID == "" {
		r.ID = model.NewID("run")
	}
	if r.Status == "" {
		r.Status = model.RunPending
	}
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	if _, err := s.DB.Exec(
		`INSERT INTO extraction_runs
		   (id, workspace_id, conversation_id, provider, status, error,
		    input_messages, actions_created, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.WorkspaceID, r.ConversationID, r.Provider, r.Status, r.Error,
		r.InputMessages, r.ActionsCreated, millis(r.StartedAt), millis(r.FinishedAt),
	); err != nil {
		return model.ExtractionRun{}, fmt.Errorf("create extraction run: %w", err)
	}
	return r, nil
}

// FinishExtractionRun updates a run's status, error, counts and finished_at
// by id. A zero FinishedAt is replaced with the current time.
func (s *Store) FinishExtractionRun(r model.ExtractionRun) error {
	if r.FinishedAt.IsZero() {
		r.FinishedAt = time.Now()
	}
	if _, err := s.DB.Exec(
		`UPDATE extraction_runs
		 SET status = ?, error = ?, input_messages = ?, actions_created = ?, finished_at = ?
		 WHERE id = ?`,
		r.Status, r.Error, r.InputMessages, r.ActionsCreated, millis(r.FinishedAt), r.ID,
	); err != nil {
		return fmt.Errorf("finish extraction run %s: %w", r.ID, err)
	}
	return nil
}

// LatestSuccessfulRunStart returns the started_at of the most recent
// successful extraction run for a conversation; ok is false when none exists.
func (s *Store) LatestSuccessfulRunStart(conversationID string) (t time.Time, ok bool, err error) {
	var started int64
	row := s.DB.QueryRow(
		`SELECT started_at FROM extraction_runs
		 WHERE conversation_id = ? AND status = ?
		 ORDER BY started_at DESC LIMIT 1`,
		conversationID, model.RunSuccess,
	)
	if err := row.Scan(&started); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("latest successful run for %s: %w", conversationID, err)
	}
	return fromMillis(started), true, nil
}

// ListExtractionRuns returns runs newest first; limit <= 0 returns all.
func (s *Store) ListExtractionRuns(limit int) ([]model.ExtractionRun, error) {
	query := `SELECT id, workspace_id, conversation_id, provider, status, error,
	                 input_messages, actions_created, started_at, finished_at
	          FROM extraction_runs ORDER BY started_at DESC, id ASC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list extraction runs: %w", err)
	}
	defer rows.Close()
	var out []model.ExtractionRun
	for rows.Next() {
		var r model.ExtractionRun
		var started, finished int64
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.ConversationID, &r.Provider, &r.Status,
			&r.Error, &r.InputMessages, &r.ActionsCreated, &started, &finished); err != nil {
			return nil, fmt.Errorf("scan extraction run: %w", err)
		}
		r.StartedAt = fromMillis(started)
		r.FinishedAt = fromMillis(finished)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list extraction runs: %w", err)
	}
	return out, nil
}

// AddAuditEvent stores an audit event.
func (s *Store) AddAuditEvent(e model.AuditEvent) error {
	if e.ID == "" {
		e.ID = model.NewID("evt")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	if _, err := s.DB.Exec(
		`INSERT INTO audit_events (id, workspace_id, event_type, subject, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.WorkspaceID, e.EventType, e.Subject, e.Detail, millis(e.CreatedAt),
	); err != nil {
		return fmt.Errorf("add audit event: %w", err)
	}
	return nil
}

// ListAuditEvents returns audit events newest first; limit <= 0 returns all.
func (s *Store) ListAuditEvents(limit int) ([]model.AuditEvent, error) {
	query := `SELECT id, workspace_id, event_type, subject, detail, created_at
	          FROM audit_events ORDER BY created_at DESC, id ASC`
	var args []any
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var out []model.AuditEvent
	for rows.Next() {
		var e model.AuditEvent
		var created int64
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.EventType, &e.Subject, &e.Detail, &created); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		e.CreatedAt = fromMillis(created)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	return out, nil
}
