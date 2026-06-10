package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

const connectorCols = `id, workspace_id, channel, provider, name, status, status_detail, created_at, updated_at`

func scanConnector(scan func(dest ...any) error) (model.Connector, error) {
	var c model.Connector
	var created, updated int64
	if err := scan(&c.ID, &c.WorkspaceID, &c.Channel, &c.Provider, &c.Name,
		&c.Status, &c.StatusDetail, &created, &updated); err != nil {
		return model.Connector{}, err
	}
	c.CreatedAt = fromMillis(created)
	c.UpdatedAt = fromMillis(updated)
	return c, nil
}

// UpsertConnector inserts a connector or, when (workspace_id, channel,
// provider, name) already exists, updates its status/status_detail. The
// stored row is returned (existing ID and created_at are preserved).
func (s *Store) UpsertConnector(c model.Connector) (model.Connector, error) {
	now := time.Now()
	if c.ID == "" {
		c.ID = model.NewID("conn")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO connectors (`+connectorCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, channel, provider, name) DO UPDATE SET
		   status = excluded.status,
		   status_detail = excluded.status_detail,
		   updated_at = excluded.updated_at`,
		c.ID, c.WorkspaceID, c.Channel, c.Provider, c.Name,
		c.Status, c.StatusDetail, millis(c.CreatedAt), millis(c.UpdatedAt),
	); err != nil {
		return model.Connector{}, fmt.Errorf("upsert connector: %w", err)
	}
	row := s.DB.QueryRow(
		`SELECT `+connectorCols+` FROM connectors
		 WHERE workspace_id = ? AND channel = ? AND provider = ? AND name = ?`,
		c.WorkspaceID, c.Channel, c.Provider, c.Name,
	)
	stored, err := scanConnector(row.Scan)
	if err != nil {
		return model.Connector{}, fmt.Errorf("reload connector: %w", err)
	}
	return stored, nil
}

// ListConnectors returns all connectors, oldest first.
func (s *Store) ListConnectors() ([]model.Connector, error) {
	rows, err := s.DB.Query(`SELECT ` + connectorCols + ` FROM connectors ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	defer rows.Close()
	var out []model.Connector
	for rows.Next() {
		c, err := scanConnector(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan connector: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list connectors: %w", err)
	}
	return out, nil
}

// GetConnector returns the connector with the given id, or (nil, nil) when
// it does not exist.
func (s *Store) GetConnector(id string) (*model.Connector, error) {
	row := s.DB.QueryRow(`SELECT `+connectorCols+` FROM connectors WHERE id = ?`, id)
	c, err := scanConnector(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get connector %s: %w", id, err)
	}
	return &c, nil
}

// SetConnectorStatus updates a connector's status and status detail.
func (s *Store) SetConnectorStatus(id, status, detail string) error {
	if _, err := s.DB.Exec(
		`UPDATE connectors SET status = ?, status_detail = ?, updated_at = ? WHERE id = ?`,
		status, detail, millis(time.Now()), id,
	); err != nil {
		return fmt.Errorf("set connector status %s: %w", id, err)
	}
	return nil
}

// GetSyncCursor returns the stored cursor for a connector, or "" when absent.
func (s *Store) GetSyncCursor(connectorID string) (string, error) {
	var cursor string
	err := s.DB.QueryRow(`SELECT cursor FROM sync_cursors WHERE connector_id = ?`, connectorID).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get sync cursor %s: %w", connectorID, err)
	}
	return cursor, nil
}

// SetSyncCursor inserts or replaces the cursor for a connector.
func (s *Store) SetSyncCursor(connectorID, cursor string) error {
	if _, err := s.DB.Exec(
		`INSERT INTO sync_cursors (connector_id, cursor) VALUES (?, ?)
		 ON CONFLICT(connector_id) DO UPDATE SET cursor = excluded.cursor`,
		connectorID, cursor,
	); err != nil {
		return fmt.Errorf("set sync cursor %s: %w", connectorID, err)
	}
	return nil
}
