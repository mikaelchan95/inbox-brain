// Package store is the SQLite persistence layer for Inbox Brain. It wraps
// *sql.DB (driver modernc.org/sqlite, name "sqlite") and exposes one
// repository method set per entity. Timestamps are stored as Unix
// milliseconds (INTEGER); the zero time.Time maps to 0.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database handle. All repository methods hang off it.
type Store struct{ DB *sql.DB }

// Open opens (creating if needed) the SQLite database at path, enables WAL,
// foreign keys and a busy timeout on every connection, and applies any
// pending schema migrations (versioned via PRAGMA user_version).
func Open(path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database %s: %w", path, err)
	}
	return &Store{DB: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	if err := s.DB.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

// migrations holds one SQL script per schema version; migrations[i] moves the
// database from user_version i to i+1.
var migrations = []string{schemaV1}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", i+1, err)
		}
	}
	return nil
}

const schemaV1 = `
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE connectors (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  provider TEXT NOT NULL,
  name TEXT NOT NULL,
  status TEXT NOT NULL,
  status_detail TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (workspace_id, channel, provider, name)
);

CREATE TABLE customers (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  external_id TEXT NOT NULL,
  name TEXT NOT NULL DEFAULT '',
  handle TEXT NOT NULL DEFAULT '',
  phone TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (workspace_id, channel, external_id)
);

CREATE TABLE conversations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  connector_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  external_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  is_group INTEGER NOT NULL DEFAULT 0,
  last_message_at INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  UNIQUE (connector_id, external_id)
);

CREATE TABLE messages (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  customer_id TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL,
  provider TEXT NOT NULL,
  connector_id TEXT NOT NULL,
  conversation_external_id TEXT NOT NULL DEFAULT '',
  message_external_id TEXT NOT NULL DEFAULT '',
  sender_external_id TEXT NOT NULL DEFAULT '',
  sender_name TEXT NOT NULL DEFAULT '',
  sender_handle TEXT NOT NULL DEFAULT '',
  sender_phone TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  body_format TEXT NOT NULL DEFAULT '',
  direction TEXT NOT NULL DEFAULT '',
  occurred_at INTEGER NOT NULL,
  ingested_at INTEGER NOT NULL,
  reply_to_external_message_id TEXT NOT NULL DEFAULT '',
  media TEXT NOT NULL DEFAULT '',
  raw_json TEXT NOT NULL DEFAULT '',
  dedupe_key TEXT NOT NULL UNIQUE
);

CREATE INDEX idx_messages_conversation_occurred ON messages (conversation_id, occurred_at);

CREATE TABLE conversation_classifications (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL UNIQUE,
  classification TEXT NOT NULL,
  business_confidence REAL NOT NULL,
  source TEXT NOT NULL,
  reason TEXT,
  reviewed_by_user INTEGER NOT NULL DEFAULT 0,
  user_override TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE message_classifications (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL UNIQUE,
  classification TEXT NOT NULL,
  business_confidence REAL NOT NULL,
  reason TEXT,
  source TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE classification_rules (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  rule_type TEXT NOT NULL,
  pattern TEXT NOT NULL,
  action TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE actions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  message_id TEXT NOT NULL DEFAULT '',
  customer_id TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  title TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  suggested_reply TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 0,
  urgency TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  snoozed_until INTEGER NOT NULL DEFAULT 0,
  source TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_actions_status_created ON actions (status, created_at);

CREATE TABLE leads (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL UNIQUE,
  customer_id TEXT NOT NULL DEFAULT '',
  action_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE extraction_runs (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  conversation_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  status TEXT NOT NULL,
  error TEXT NOT NULL DEFAULT '',
  input_messages INTEGER NOT NULL DEFAULT 0,
  actions_created INTEGER NOT NULL DEFAULT 0,
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE sync_cursors (
  connector_id TEXT PRIMARY KEY,
  cursor TEXT NOT NULL
);

CREATE TABLE audit_events (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  subject TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL
);
`

// millis converts a time.Time to Unix milliseconds; the zero time maps to 0.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// fromMillis converts Unix milliseconds back to a time.Time; 0 maps to the
// zero time.
func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// mediaToJSON encodes message media as a JSON TEXT column ("" when empty).
func mediaToJSON(m []model.MessageMedia) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", fmt.Errorf("encode media: %w", err)
	}
	return string(b), nil
}

// mediaFromJSON decodes the media TEXT column ("" decodes to nil).
func mediaFromJSON(s string) ([]model.MessageMedia, error) {
	if s == "" {
		return nil, nil
	}
	var m []model.MessageMedia
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, fmt.Errorf("decode media: %w", err)
	}
	return m, nil
}
