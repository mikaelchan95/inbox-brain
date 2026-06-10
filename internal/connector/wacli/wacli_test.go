package wacli

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

func openStore(t *testing.T) (*store.Store, model.Workspace) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "ib.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("ensure workspace: %v", err)
	}
	return s, ws
}

// buildFixture creates a synthetic wacli.db in a temp dir from a SQL script.
func buildFixture(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wacli.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	if _, err := db.Exec(script); err != nil {
		db.Close()
		t.Fatalf("build fixture: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return path
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func convByExternalID(t *testing.T, s *store.Store, ext string) model.Conversation {
	t.Helper()
	convs, err := s.ListConversations(store.ConversationFilter{})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	for _, c := range convs {
		if c.ExternalID == ext {
			return c
		}
	}
	t.Fatalf("conversation %s not found among %d conversations", ext, len(convs))
	return model.Conversation{}
}

func messagesOf(t *testing.T, s *store.Store, conversationID string) []model.Message {
	t.Helper()
	msgs, err := s.ListMessages(conversationID, 0)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	return msgs
}

const plausibleFixture = `
CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT);
CREATE TABLE messages (id TEXT, chat_jid TEXT, sender_jid TEXT, timestamp INTEGER, text TEXT, is_from_me INTEGER);
INSERT INTO chats VALUES ('6591234567@s.whatsapp.net', 'Mrs Tan');
INSERT INTO chats VALUES ('120363012345678901@g.us', 'Family Group');
INSERT INTO chats VALUES ('6587654321@s.whatsapp.net', NULL);
INSERT INTO messages VALUES ('MSG1', '6591234567@s.whatsapp.net', '6591234567@s.whatsapp.net', 1749600000, 'Hi, do you do trial classes?', 0);
INSERT INTO messages VALUES ('MSG2', '6591234567@s.whatsapp.net', 'me', 1749600060, 'Yes! Saturday 10am works.', 1);
INSERT INTO messages VALUES ('MSG3', '120363012345678901@g.us', '6599998888@s.whatsapp.net', 1749600120, 'Dinner on Sunday?', 0);
INSERT INTO messages VALUES ('MSG4', '6587654321@s.whatsapp.net', '6587654321@s.whatsapp.net', 1749600180, 'Hello, saw your ad about logo design', 0);
INSERT INTO messages VALUES ('MSG5', '6591234567@s.whatsapp.net', '6591234567@s.whatsapp.net', 1749600240, '', 0);
`

const alternateFixture = `
CREATE TABLE conversations (chat_jid TEXT, title TEXT);
CREATE TABLE message (message_id TEXT, chat TEXT, sender TEXT, ts INTEGER, body TEXT, from_me INTEGER);
INSERT INTO conversations VALUES ('6512340000@s.whatsapp.net', 'Alex');
INSERT INTO message VALUES ('A1', '6512340000@s.whatsapp.net', '6512340000@s.whatsapp.net', 1749600000123, 'Can you quote a landing page?', 0);
INSERT INTO message VALUES ('A2', '6512340000@s.whatsapp.net', 'me', 1749600300456, 'Sure, sending the invoice tonight', 1);
`

// realWacliFixture mirrors the actual openclaw/wacli schema (trimmed to the
// relevant columns): note msg_id (not id) and a NULL-text media row.
const realWacliFixture = `
CREATE TABLE chats (jid TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT, last_message_ts INTEGER, archived INTEGER NOT NULL DEFAULT 0);
CREATE TABLE messages (rowid INTEGER PRIMARY KEY AUTOINCREMENT, chat_jid TEXT NOT NULL, chat_name TEXT, msg_id TEXT NOT NULL, sender_jid TEXT, sender_name TEXT, ts INTEGER NOT NULL, from_me INTEGER NOT NULL, text TEXT, media_type TEXT);
INSERT INTO chats (jid, kind, name, last_message_ts) VALUES ('6591112222@s.whatsapp.net', 'user', 'Mrs Lee', 1749600100);
INSERT INTO messages (chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text) VALUES ('6591112222@s.whatsapp.net', 'Mrs Lee', '3EB0ABCDEF', '6591112222@s.whatsapp.net', 'Mrs Lee', 1749600000, 0, 'How much for a logo?');
INSERT INTO messages (chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me, text, media_type) VALUES ('6591112222@s.whatsapp.net', 'Mrs Lee', '3EB0AAAAAA', '6591112222@s.whatsapp.net', 'Mrs Lee', 1749600100, 0, NULL, 'image');
`

const noStableIDFixture = `
CREATE TABLE chats (jid TEXT, name TEXT);
CREATE TABLE messages (chat_jid TEXT, sender TEXT, timestamp INTEGER, text TEXT);
INSERT INTO chats VALUES ('6593334444@s.whatsapp.net', 'Ben');
INSERT INTO messages VALUES ('6593334444@s.whatsapp.net', '6593334444@s.whatsapp.net', 1749600000, 'hello');
INSERT INTO messages VALUES ('6593334444@s.whatsapp.net', '6593334444@s.whatsapp.net', 1749600001, 'hello');
`

func TestImportFixtures(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   ImportSummary
		verify func(t *testing.T, s *store.Store)
	}{
		{
			name:   "plausible wacli shape",
			script: plausibleFixture,
			want:   ImportSummary{Conversations: 3, Messages: 4, Skipped: 0},
			verify: func(t *testing.T, s *store.Store) {
				// Connector row.
				conns, err := s.ListConnectors()
				if err != nil {
					t.Fatalf("list connectors: %v", err)
				}
				if len(conns) != 1 {
					t.Fatalf("connectors = %d, want 1", len(conns))
				}
				conn := conns[0]
				if conn.Channel != model.ChannelWhatsApp || conn.Provider != model.ProviderWacli ||
					conn.Name != "wacli import" || conn.Status != model.ConnectorActive {
					t.Errorf("connector = %+v, want whatsapp/wacli/\"wacli import\"/active", conn)
				}

				// 1:1 chat with name.
				tan := convByExternalID(t, s, "6591234567@s.whatsapp.net")
				if tan.Title != "Mrs Tan" {
					t.Errorf("title = %q, want %q", tan.Title, "Mrs Tan")
				}
				if tan.IsGroup {
					t.Errorf("IsGroup = true for 1:1 chat")
				}
				if want := time.Unix(1749600060, 0).UTC(); !tan.LastMessageAt.Equal(want) {
					t.Errorf("LastMessageAt = %v, want %v (empty-text row must not count)", tan.LastMessageAt, want)
				}

				msgs := messagesOf(t, s, tan.ID)
				if len(msgs) != 2 {
					t.Fatalf("messages = %d, want 2 (empty text skipped)", len(msgs))
				}
				first, second := msgs[0], msgs[1]
				if first.Direction != model.DirectionInbound {
					t.Errorf("first direction = %q, want inbound", first.Direction)
				}
				if second.Direction != model.DirectionOutbound {
					t.Errorf("second direction = %q, want outbound (is_from_me=1)", second.Direction)
				}
				if want := "wacli:" + conn.ID + ":6591234567@s.whatsapp.net:MSG1"; first.DedupeKey != want {
					t.Errorf("dedupe key = %q, want %q", first.DedupeKey, want)
				}
				if want := time.Unix(1749600000, 0).UTC(); !first.OccurredAt.Equal(want) {
					t.Errorf("occurred at = %v, want %v (unix seconds)", first.OccurredAt, want)
				}
				if first.Channel != model.ChannelWhatsApp || first.Provider != model.ProviderWacli {
					t.Errorf("channel/provider = %q/%q, want whatsapp/wacli", first.Channel, first.Provider)
				}
				if first.Body != "Hi, do you do trial classes?" {
					t.Errorf("body = %q", first.Body)
				}
				var raw map[string]any
				if err := json.Unmarshal(first.RawJSON, &raw); err != nil {
					t.Fatalf("raw json: %v", err)
				}
				if raw["chat_jid"] != "6591234567@s.whatsapp.net" {
					t.Errorf("raw chat_jid = %v", raw["chat_jid"])
				}

				cust, err := s.GetCustomer(first.CustomerID)
				if err != nil || cust == nil {
					t.Fatalf("get customer %q: %v %v", first.CustomerID, cust, err)
				}
				if cust.Phone != "6591234567" {
					t.Errorf("customer phone = %q, want from jid local part", cust.Phone)
				}
				if cust.Name != "Mrs Tan" {
					t.Errorf("customer name = %q, want Mrs Tan", cust.Name)
				}

				// Group chat.
				grp := convByExternalID(t, s, "120363012345678901@g.us")
				if !grp.IsGroup {
					t.Errorf("IsGroup = false for @g.us jid")
				}
				if grp.Title != "Family Group" {
					t.Errorf("group title = %q", grp.Title)
				}

				// Unnamed chat falls back to phone.
				anon := convByExternalID(t, s, "6587654321@s.whatsapp.net")
				if anon.Title != "6587654321" {
					t.Errorf("unnamed chat title = %q, want phone fallback", anon.Title)
				}
			},
		},
		{
			name:   "alternate column names",
			script: alternateFixture,
			want:   ImportSummary{Conversations: 1, Messages: 2, Skipped: 0},
			verify: func(t *testing.T, s *store.Store) {
				alex := convByExternalID(t, s, "6512340000@s.whatsapp.net")
				if alex.Title != "Alex" {
					t.Errorf("title = %q, want Alex", alex.Title)
				}
				msgs := messagesOf(t, s, alex.ID)
				if len(msgs) != 2 {
					t.Fatalf("messages = %d, want 2", len(msgs))
				}
				if want := time.UnixMilli(1749600000123).UTC(); !msgs[0].OccurredAt.Equal(want) {
					t.Errorf("occurred at = %v, want %v (unix millis)", msgs[0].OccurredAt, want)
				}
				if msgs[0].MessageExternalID != "A1" {
					t.Errorf("message external id = %q, want A1", msgs[0].MessageExternalID)
				}
				if msgs[1].Direction != model.DirectionOutbound {
					t.Errorf("direction = %q, want outbound (from_me=1)", msgs[1].Direction)
				}
			},
		},
		{
			name:   "real wacli schema",
			script: realWacliFixture,
			want:   ImportSummary{Conversations: 1, Messages: 1, Skipped: 0},
			verify: func(t *testing.T, s *store.Store) {
				lee := convByExternalID(t, s, "6591112222@s.whatsapp.net")
				if lee.Title != "Mrs Lee" {
					t.Errorf("title = %q, want Mrs Lee", lee.Title)
				}
				msgs := messagesOf(t, s, lee.ID)
				if len(msgs) != 1 {
					t.Fatalf("messages = %d, want 1 (NULL-text media row skipped)", len(msgs))
				}
				m := msgs[0]
				if m.MessageExternalID != "3EB0ABCDEF" {
					t.Errorf("message external id = %q, want msg_id column value", m.MessageExternalID)
				}
				if !strings.HasSuffix(m.DedupeKey, ":6591112222@s.whatsapp.net:3EB0ABCDEF") ||
					!strings.HasPrefix(m.DedupeKey, "wacli:") {
					t.Errorf("dedupe key = %q, want wacli:<conn>:<jid>:<msg_id>", m.DedupeKey)
				}
				if m.SenderName != "Mrs Lee" {
					t.Errorf("sender name = %q, want Mrs Lee", m.SenderName)
				}
			},
		},
		{
			name:   "no stable message id",
			script: noStableIDFixture,
			want:   ImportSummary{Conversations: 1, Messages: 2, Skipped: 0},
			verify: func(t *testing.T, s *store.Store) {
				ben := convByExternalID(t, s, "6593334444@s.whatsapp.net")
				msgs := messagesOf(t, s, ben.ID)
				if len(msgs) != 2 {
					t.Fatalf("messages = %d, want 2", len(msgs))
				}
				for _, m := range msgs {
					if !strings.HasPrefix(m.DedupeKey, "hash:") {
						t.Errorf("dedupe key = %q, want hash fallback", m.DedupeKey)
					}
					if m.Direction != model.DirectionInbound {
						t.Errorf("direction = %q, want inbound when no from_me column", m.Direction)
					}
				}
				if msgs[0].DedupeKey == msgs[1].DedupeKey {
					t.Errorf("hash dedupe keys collide: %q", msgs[0].DedupeKey)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ws := openStore(t)
			dbPath := buildFixture(t, tc.script)
			before := fileHash(t, dbPath)

			sum, err := Import(context.Background(), s, ws, dbPath)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if sum != tc.want {
				t.Fatalf("summary = %+v, want %+v", sum, tc.want)
			}

			// Re-import must be idempotent: 0 new messages, all skipped.
			again, err := Import(context.Background(), s, ws, dbPath)
			if err != nil {
				t.Fatalf("re-Import: %v", err)
			}
			wantAgain := ImportSummary{Conversations: tc.want.Conversations, Messages: 0, Skipped: tc.want.Messages}
			if again != wantAgain {
				t.Fatalf("re-import summary = %+v, want %+v", again, wantAgain)
			}

			// The wacli database must never be written.
			if after := fileHash(t, dbPath); after != before {
				t.Errorf("wacli.db bytes changed: hash %s -> %s", before, after)
			}

			tc.verify(t, s)
		})
	}
}

func TestImportErrors(t *testing.T) {
	cases := []struct {
		name    string
		path    func(t *testing.T) string
		substrs []string
	}{
		{
			name: "missing file",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "wacli.db")
			},
			substrs: []string{"wacli sync", "not found"},
		},
		{
			name: "unrecognized schema",
			path: func(t *testing.T) string {
				return buildFixture(t, `CREATE TABLE stuff (a TEXT, b TEXT);`)
			},
			substrs: []string{"wacli sync", "stuff", "unrecognized"},
		},
		{
			name: "messages table without recognizable columns",
			path: func(t *testing.T) string {
				return buildFixture(t, `CREATE TABLE messages (foo TEXT, bar TEXT);`)
			},
			substrs: []string{"wacli sync", "messages"},
		},
		{
			name: "not a sqlite database",
			path: func(t *testing.T) string {
				p := filepath.Join(t.TempDir(), "wacli.db")
				if err := os.WriteFile(p, []byte("this is not a database"), 0o600); err != nil {
					t.Fatalf("write file: %v", err)
				}
				return p
			},
			substrs: []string{"wacli sync"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ws := openStore(t)
			_, err := Import(context.Background(), s, ws, tc.path(t))
			if err == nil {
				t.Fatalf("Import: expected error, got nil")
			}
			for _, sub := range tc.substrs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not mention %q", err, sub)
				}
			}
		})
	}
}

func TestTsToTime(t *testing.T) {
	cases := []struct {
		name string
		in   int64
		want time.Time
	}{
		{"zero", 0, time.Time{}},
		{"negative", -5, time.Time{}},
		{"unix seconds", 1749600000, time.Unix(1749600000, 0).UTC()},
		{"unix millis", 1749600000123, time.UnixMilli(1749600000123).UTC()},
		{"boundary stays seconds", 1_000_000_000_000, time.Unix(1_000_000_000_000, 0).UTC()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tsToTime(tc.in); !got.Equal(tc.want) {
				t.Errorf("tsToTime(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPhoneFromJID(t *testing.T) {
	cases := []struct {
		jid  string
		want string
	}{
		{"6591234567@s.whatsapp.net", "6591234567"},
		{"120363012345678901@g.us", ""},
		{"me", ""},
		{"abc@s.whatsapp.net", ""},
		{"6591234567:12@s.whatsapp.net", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.jid, func(t *testing.T) {
			if got := phoneFromJID(tc.jid); got != tc.want {
				t.Errorf("phoneFromJID(%q) = %q, want %q", tc.jid, got, tc.want)
			}
		})
	}
}
