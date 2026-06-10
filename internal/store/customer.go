package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

const customerCols = `id, workspace_id, channel, external_id, name, handle, phone, created_at, updated_at`

func scanCustomer(scan func(dest ...any) error) (model.Customer, error) {
	var c model.Customer
	var created, updated int64
	if err := scan(&c.ID, &c.WorkspaceID, &c.Channel, &c.ExternalID,
		&c.Name, &c.Handle, &c.Phone, &created, &updated); err != nil {
		return model.Customer{}, err
	}
	c.CreatedAt = fromMillis(created)
	c.UpdatedAt = fromMillis(updated)
	return c, nil
}

// UpsertCustomer inserts a customer or, when (workspace_id, channel,
// external_id) already exists, refreshes name/handle/phone (non-empty new
// values win; empty values never erase stored ones). The stored row is
// returned.
func (s *Store) UpsertCustomer(c model.Customer) (model.Customer, error) {
	now := time.Now()
	if c.ID == "" {
		c.ID = model.NewID("cust")
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = now
	}
	if _, err := s.DB.Exec(
		`INSERT INTO customers (`+customerCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(workspace_id, channel, external_id) DO UPDATE SET
		   name = CASE WHEN excluded.name <> '' THEN excluded.name ELSE customers.name END,
		   handle = CASE WHEN excluded.handle <> '' THEN excluded.handle ELSE customers.handle END,
		   phone = CASE WHEN excluded.phone <> '' THEN excluded.phone ELSE customers.phone END,
		   updated_at = excluded.updated_at`,
		c.ID, c.WorkspaceID, c.Channel, c.ExternalID,
		c.Name, c.Handle, c.Phone, millis(c.CreatedAt), millis(c.UpdatedAt),
	); err != nil {
		return model.Customer{}, fmt.Errorf("upsert customer: %w", err)
	}
	row := s.DB.QueryRow(
		`SELECT `+customerCols+` FROM customers
		 WHERE workspace_id = ? AND channel = ? AND external_id = ?`,
		c.WorkspaceID, c.Channel, c.ExternalID,
	)
	stored, err := scanCustomer(row.Scan)
	if err != nil {
		return model.Customer{}, fmt.Errorf("reload customer: %w", err)
	}
	return stored, nil
}

// GetCustomer returns the customer with the given id, or (nil, nil) when it
// does not exist.
func (s *Store) GetCustomer(id string) (*model.Customer, error) {
	row := s.DB.QueryRow(`SELECT `+customerCols+` FROM customers WHERE id = ?`, id)
	c, err := scanCustomer(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get customer %s: %w", id, err)
	}
	return &c, nil
}
