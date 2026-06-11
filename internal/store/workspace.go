package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// EnsureDefaultWorkspace returns the first workspace, creating one named
// "default" when the table is empty.
func (s *Store) EnsureDefaultWorkspace() (model.Workspace, error) {
	var w model.Workspace
	var created int64
	err := s.DB.QueryRow(
		`SELECT id, name, created_at FROM workspaces ORDER BY created_at ASC, id ASC LIMIT 1`,
	).Scan(&w.ID, &w.Name, &created)
	if err == nil {
		w.CreatedAt = fromMillis(created)
		return w, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Workspace{}, fmt.Errorf("query workspaces: %w", err)
	}
	w = model.Workspace{ID: model.NewID("ws"), Name: "default", CreatedAt: time.Now()}
	if _, err := s.DB.Exec(
		`INSERT INTO workspaces (id, name, created_at) VALUES (?, ?, ?)`,
		w.ID, w.Name, millis(w.CreatedAt),
	); err != nil {
		return model.Workspace{}, fmt.Errorf("create default workspace: %w", err)
	}
	return w, nil
}

// GetSetting returns the value for key, or "" when the key is missing.
func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// SetSetting inserts or replaces a settings key.
func (s *Store) SetSetting(key, value string) error {
	if _, err := s.DB.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}
