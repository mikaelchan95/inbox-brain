package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// --- fixtures -------------------------------------------------------------

const privateTextUpdate = `{
  "update_id": 1001,
  "message": {
    "message_id": 11,
    "from": {"id": 555, "is_bot": false, "first_name": "Mrs", "last_name": "Tan", "username": "mrstan"},
    "chat": {"id": 555, "first_name": "Mrs", "last_name": "Tan", "type": "private"},
    "date": 1717900000,
    "text": "Can I book a trial class on Saturday?"
  }
}`

const groupTextUpdate = `{
  "update_id": 1002,
  "message": {
    "message_id": 7,
    "from": {"id": 777, "is_bot": false, "first_name": "Alex", "username": "alexd"},
    "chat": {"id": -100123456, "title": "Design Referrals", "type": "supergroup"},
    "date": 1717900100,
    "text": "Anyone available for a logo project?"
  }
}`

const captionPhotoUpdate = `{
  "update_id": 1003,
  "message": {
    "message_id": 12,
    "from": {"id": 555, "is_bot": false, "first_name": "Mrs", "last_name": "Tan", "username": "mrstan"},
    "chat": {"id": 555, "first_name": "Mrs", "last_name": "Tan", "type": "private"},
    "date": 1717900200,
    "photo": [{"file_id": "abc123", "width": 100, "height": 100}],
    "caption": "Here is the payment receipt"
  }
}`

const editedMessageUpdate = `{
  "update_id": 1004,
  "edited_message": {
    "message_id": 11,
    "from": {"id": 555, "is_bot": false, "first_name": "Mrs", "last_name": "Tan", "username": "mrstan"},
    "chat": {"id": 555, "first_name": "Mrs", "last_name": "Tan", "type": "private"},
    "date": 1717900000,
    "edit_date": 1717900300,
    "text": "Can I book a trial class on Sunday instead?"
  }
}`

const stickerUpdate = `{
  "update_id": 1005,
  "message": {
    "message_id": 13,
    "from": {"id": 555, "is_bot": false, "first_name": "Mrs", "last_name": "Tan"},
    "chat": {"id": 555, "first_name": "Mrs", "last_name": "Tan", "type": "private"},
    "date": 1717900400,
    "sticker": {"file_id": "xyz"}
  }
}`

const callbackQueryUpdate = `{
  "update_id": 1006,
  "callback_query": {"id": "cb1", "from": {"id": 555, "first_name": "Mrs"}, "data": "ok"}
}`

const replyTextUpdate = `{
  "update_id": 1007,
  "message": {
    "message_id": 14,
    "from": {"id": 555, "is_bot": false, "first_name": "Mrs", "last_name": "Tan", "username": "mrstan"},
    "chat": {"id": 555, "first_name": "Mrs", "last_name": "Tan", "type": "private"},
    "date": 1717900500,
    "reply_to_message": {"message_id": 11, "chat": {"id": 555, "type": "private"}, "date": 1717900000},
    "text": "Yes, Saturday works."
  }
}`

// --- NormalizeUpdate ------------------------------------------------------

func TestNormalizeUpdate(t *testing.T) {
	const connID = "conn_test1"
	const wsID = "ws_test1"

	tests := []struct {
		name    string
		raw     string
		wantNil bool
		wantErr bool
		check   func(t *testing.T, n *Normalized)
	}{
		{
			name: "private text",
			raw:  privateTextUpdate,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Conversation.Title, "Mrs Tan"; got != want {
					t.Errorf("Conversation.Title = %q, want %q", got, want)
				}
				if n.Conversation.IsGroup {
					t.Error("Conversation.IsGroup = true, want false")
				}
				if got, want := n.Conversation.ExternalID, "555"; got != want {
					t.Errorf("Conversation.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Conversation.Channel, model.ChannelTelegram; got != want {
					t.Errorf("Conversation.Channel = %q, want %q", got, want)
				}
				if got, want := n.Conversation.ConnectorID, connID; got != want {
					t.Errorf("Conversation.ConnectorID = %q, want %q", got, want)
				}
				if got, want := n.Customer.ExternalID, "555"; got != want {
					t.Errorf("Customer.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Customer.Name, "Mrs Tan"; got != want {
					t.Errorf("Customer.Name = %q, want %q", got, want)
				}
				if got, want := n.Customer.Handle, "mrstan"; got != want {
					t.Errorf("Customer.Handle = %q, want %q", got, want)
				}
				m := n.Message
				if got, want := m.Body, "Can I book a trial class on Saturday?"; got != want {
					t.Errorf("Message.Body = %q, want %q", got, want)
				}
				if got, want := m.BodyFormat, "plain_text"; got != want {
					t.Errorf("Message.BodyFormat = %q, want %q", got, want)
				}
				if got, want := m.Direction, model.DirectionInbound; got != want {
					t.Errorf("Message.Direction = %q, want %q", got, want)
				}
				if got, want := m.Provider, model.ProviderTelegramBotAPI; got != want {
					t.Errorf("Message.Provider = %q, want %q", got, want)
				}
				if got, want := m.MessageExternalID, "11"; got != want {
					t.Errorf("Message.MessageExternalID = %q, want %q", got, want)
				}
				if got, want := m.ConversationExternalID, "555"; got != want {
					t.Errorf("Message.ConversationExternalID = %q, want %q", got, want)
				}
				if got, want := m.SenderExternalID, "555"; got != want {
					t.Errorf("Message.SenderExternalID = %q, want %q", got, want)
				}
				if got, want := m.SenderName, "Mrs Tan"; got != want {
					t.Errorf("Message.SenderName = %q, want %q", got, want)
				}
				wantTime := time.Unix(1717900000, 0)
				if !m.OccurredAt.Equal(wantTime) {
					t.Errorf("Message.OccurredAt = %v, want %v", m.OccurredAt, wantTime)
				}
				if got, want := m.DedupeKey, "telegram_bot_api:conn_test1:555:11"; got != want {
					t.Errorf("Message.DedupeKey = %q, want %q", got, want)
				}
				if string(m.RawJSON) != privateTextUpdate {
					t.Errorf("Message.RawJSON does not match the raw update")
				}
				if got, want := m.WorkspaceID, wsID; got != want {
					t.Errorf("Message.WorkspaceID = %q, want %q", got, want)
				}
			},
		},
		{
			name: "group text",
			raw:  groupTextUpdate,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Conversation.Title, "Design Referrals"; got != want {
					t.Errorf("Conversation.Title = %q, want %q", got, want)
				}
				if !n.Conversation.IsGroup {
					t.Error("Conversation.IsGroup = false, want true")
				}
				if got, want := n.Conversation.ExternalID, "-100123456"; got != want {
					t.Errorf("Conversation.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Customer.ExternalID, "777"; got != want {
					t.Errorf("Customer.ExternalID = %q, want %q", got, want)
				}
				if got, want := n.Customer.Name, "Alex"; got != want {
					t.Errorf("Customer.Name = %q, want %q", got, want)
				}
				if got, want := n.Message.DedupeKey, "telegram_bot_api:conn_test1:-100123456:7"; got != want {
					t.Errorf("Message.DedupeKey = %q, want %q", got, want)
				}
			},
		},
		{
			name: "caption-only photo",
			raw:  captionPhotoUpdate,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Message.Body, "Here is the payment receipt"; got != want {
					t.Errorf("Message.Body = %q, want %q", got, want)
				}
				if got, want := n.Message.MessageExternalID, "12"; got != want {
					t.Errorf("Message.MessageExternalID = %q, want %q", got, want)
				}
			},
		},
		{
			name: "edited_message",
			raw:  editedMessageUpdate,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Message.Body, "Can I book a trial class on Sunday instead?"; got != want {
					t.Errorf("Message.Body = %q, want %q", got, want)
				}
				// Same message_id as the original: same dedupe key.
				if got, want := n.Message.DedupeKey, "telegram_bot_api:conn_test1:555:11"; got != want {
					t.Errorf("Message.DedupeKey = %q, want %q", got, want)
				}
			},
		},
		{
			name: "reply message carries reply_to id",
			raw:  replyTextUpdate,
			check: func(t *testing.T, n *Normalized) {
				if got, want := n.Message.ReplyToExternalMessageID, "11"; got != want {
					t.Errorf("Message.ReplyToExternalMessageID = %q, want %q", got, want)
				}
			},
		},
		{name: "sticker without text or caption", raw: stickerUpdate, wantNil: true},
		{name: "non-message update", raw: callbackQueryUpdate, wantNil: true},
		{name: "invalid json", raw: `{"update_id": `, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := NormalizeUpdate(json.RawMessage(tt.raw), connID, wsID)
			if tt.wantErr {
				if err == nil {
					t.Fatal("NormalizeUpdate() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeUpdate() error = %v", err)
			}
			if tt.wantNil {
				if n != nil {
					t.Fatalf("NormalizeUpdate() = %+v, want nil", n)
				}
				return
			}
			if n == nil {
				t.Fatal("NormalizeUpdate() = nil, want non-nil")
			}
			tt.check(t, n)
		})
	}
}

// --- test helpers ---------------------------------------------------------

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(body))
}

// fakeAPI serves canned getMe and getUpdates responses and records the
// offset parameter of every getUpdates call.
type fakeAPI struct {
	mu      sync.Mutex
	updates []string // raw update JSON served on every getUpdates call
	offsets []string // offset query param per getUpdates call
}

func (f *fakeAPI) handler(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bot" + token + "/getMe":
			writeJSON(w, http.StatusOK,
				`{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Inbox Brain","username":"inbox_brain_bot"}}`)
		case "/bot" + token + "/getUpdates":
			f.mu.Lock()
			f.offsets = append(f.offsets, r.URL.Query().Get("offset"))
			result := "[" + strings.Join(f.updates, ",") + "]"
			f.mu.Unlock()
			writeJSON(w, http.StatusOK, `{"ok":true,"result":`+result+`}`)
		default:
			writeJSON(w, http.StatusNotFound, `{"ok":false,"error_code":404,"description":"Not Found"}`)
		}
	}
}

func (f *fakeAPI) seenOffsets() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.offsets...)
}

// --- Connect --------------------------------------------------------------

func TestConnect(t *testing.T) {
	t.Run("valid token upserts active connector named after bot", func(t *testing.T) {
		s := openTestStore(t)
		api := &fakeAPI{}
		srv := httptest.NewServer(api.handler("GOODTOKEN"))
		defer srv.Close()

		c, err := connect(s, "GOODTOKEN", srv.URL)
		if err != nil {
			t.Fatalf("connect() error = %v", err)
		}
		row := c.ConnectorRow
		if row.Channel != model.ChannelTelegram {
			t.Errorf("Channel = %q, want %q", row.Channel, model.ChannelTelegram)
		}
		if row.Provider != model.ProviderTelegramBotAPI {
			t.Errorf("Provider = %q, want %q", row.Provider, model.ProviderTelegramBotAPI)
		}
		if row.Name != "inbox_brain_bot" {
			t.Errorf("Name = %q, want %q", row.Name, "inbox_brain_bot")
		}
		if row.Status != model.ConnectorActive {
			t.Errorf("Status = %q, want %q", row.Status, model.ConnectorActive)
		}
		if c.Workspace.ID == "" {
			t.Error("Workspace.ID is empty")
		}

		// Connecting again reuses the same connector row (upsert).
		c2, err := connect(s, "GOODTOKEN", srv.URL)
		if err != nil {
			t.Fatalf("second connect() error = %v", err)
		}
		if c2.ConnectorRow.ID != row.ID {
			t.Errorf("second connect ID = %q, want %q", c2.ConnectorRow.ID, row.ID)
		}
		rows, err := s.ListConnectors()
		if err != nil {
			t.Fatalf("ListConnectors() error = %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("ListConnectors() returned %d rows, want 1", len(rows))
		}
	})

	t.Run("bad token returns actionable error", func(t *testing.T) {
		s := openTestStore(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
		}))
		defer srv.Close()

		_, err := connect(s, "BADTOKEN", srv.URL)
		if err == nil {
			t.Fatal("connect() error = nil, want error")
		}
		for _, want := range []string{"Unauthorized", "TELEGRAM_BOT_TOKEN"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("connect() error %q does not mention %q", err, want)
			}
		}
		rows, err2 := s.ListConnectors()
		if err2 != nil {
			t.Fatalf("ListConnectors() error = %v", err2)
		}
		if len(rows) != 0 {
			t.Errorf("connector row created on failed connect: %d rows", len(rows))
		}
	})

	t.Run("empty token returns actionable error", func(t *testing.T) {
		s := openTestStore(t)
		_, err := connect(s, "", defaultBaseURL)
		if err == nil {
			t.Fatal("connect() error = nil, want error")
		}
		if !strings.Contains(err.Error(), "TELEGRAM_BOT_TOKEN") {
			t.Errorf("connect() error %q does not mention TELEGRAM_BOT_TOKEN", err)
		}
	})
}

// --- SyncOnce ---------------------------------------------------------------

func TestSyncOnce(t *testing.T) {
	s := openTestStore(t)
	api := &fakeAPI{updates: []string{
		privateTextUpdate, groupTextUpdate, captionPhotoUpdate,
		editedMessageUpdate, stickerUpdate,
	}}
	srv := httptest.NewServer(api.handler("TOKEN"))
	defer srv.Close()

	c, err := connect(s, "TOKEN", srv.URL)
	if err != nil {
		t.Fatalf("connect() error = %v", err)
	}

	// First sync: private text, group text and caption photo are new; the
	// edited_message shares message_id 11 with the private text (dedupe) and
	// the sticker has no text/caption (skip).
	count, err := c.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if count != 3 {
		t.Errorf("SyncOnce() = %d, want 3", count)
	}

	cursor, err := s.GetSyncCursor(c.ConnectorRow.ID)
	if err != nil {
		t.Fatalf("GetSyncCursor() error = %v", err)
	}
	if cursor != "1006" { // max update_id 1005 + 1
		t.Errorf("cursor = %q, want %q", cursor, "1006")
	}

	convs, err := s.ListConversations(store.ConversationFilter{})
	if err != nil {
		t.Fatalf("ListConversations() error = %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("got %d conversations, want 2", len(convs))
	}
	byExternal := map[string]model.Conversation{}
	for _, cv := range convs {
		byExternal[cv.ExternalID] = cv
	}
	private, ok := byExternal["555"]
	if !ok {
		t.Fatal("conversation 555 not found")
	}
	if private.Title != "Mrs Tan" || private.IsGroup {
		t.Errorf("private conversation = %q group=%v, want Mrs Tan group=false", private.Title, private.IsGroup)
	}
	group, ok := byExternal["-100123456"]
	if !ok {
		t.Fatal("group conversation not found")
	}
	if group.Title != "Design Referrals" || !group.IsGroup {
		t.Errorf("group conversation = %q group=%v, want Design Referrals group=true", group.Title, group.IsGroup)
	}

	msgs, err := s.ListMessages(private.ID, 0)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("private conversation has %d messages, want 2", len(msgs))
	}
	if msgs[0].CustomerID == "" || msgs[0].ConversationID != private.ID {
		t.Errorf("message not linked: customer=%q conversation=%q", msgs[0].CustomerID, msgs[0].ConversationID)
	}
	cust, err := s.GetCustomer(msgs[0].CustomerID)
	if err != nil {
		t.Fatalf("GetCustomer() error = %v", err)
	}
	if cust == nil || cust.Name != "Mrs Tan" {
		t.Errorf("customer = %+v, want name Mrs Tan", cust)
	}

	// Second sync over the same canned data: everything dedupes to 0.
	count, err = c.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("second SyncOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("second SyncOnce() = %d, want 0", count)
	}

	offsets := api.seenOffsets()
	if len(offsets) != 2 {
		t.Fatalf("getUpdates called %d times, want 2", len(offsets))
	}
	if offsets[0] != "" {
		t.Errorf("first getUpdates offset = %q, want empty", offsets[0])
	}
	if offsets[1] != "1006" {
		t.Errorf("second getUpdates offset = %q, want 1006", offsets[1])
	}
}

// --- Follow -----------------------------------------------------------------

func TestFollow(t *testing.T) {
	s := openTestStore(t)
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace() error = %v", err)
	}
	row, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelTelegram,
		Provider:    model.ProviderTelegramBotAPI,
		Name:        "inbox_brain_bot",
		Status:      model.ConnectorActive,
	})
	if err != nil {
		t.Fatalf("UpsertConnector() error = %v", err)
	}

	var failing atomic.Bool
	failing.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			writeJSON(w, http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"boom"}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"ok":true,"result":[]}`)
	}))
	defer srv.Close()

	c := &Connector{
		Token:        "TOKEN",
		Store:        s,
		Workspace:    ws,
		ConnectorRow: row,
		BaseURL:      srv.URL,
		backoffBase:  time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Follow(ctx) }()

	// After 3 consecutive failures the connector goes degraded with detail.
	waitForStatus(t, s, row.ID, model.ConnectorDegraded)
	got, err := s.GetConnector(row.ID)
	if err != nil {
		t.Fatalf("GetConnector() error = %v", err)
	}
	if !strings.Contains(got.StatusDetail, "boom") {
		t.Errorf("StatusDetail = %q, want it to contain %q", got.StatusDetail, "boom")
	}

	// Once the API recovers, the connector returns to active.
	failing.Store(false)
	waitForStatus(t, s, row.ID, model.ConnectorActive)

	// Cancelling the context exits the loop with nil.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Follow() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Follow() did not exit after context cancellation")
	}
}

func waitForStatus(t *testing.T, s *store.Store, connectorID, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := s.GetConnector(connectorID)
		if err != nil {
			t.Fatalf("GetConnector() error = %v", err)
		}
		if c != nil && c.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c, _ := s.GetConnector(connectorID)
	t.Fatalf("connector never reached status %q (last: %+v)", want, c)
}

// --- error paths ------------------------------------------------------------

func TestSyncOnceServerError(t *testing.T) {
	s := openTestStore(t)
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace() error = %v", err)
	}
	row, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelTelegram,
		Provider:    model.ProviderTelegramBotAPI,
		Name:        "bot",
		Status:      model.ConnectorActive,
	})
	if err != nil {
		t.Fatalf("UpsertConnector() error = %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadGateway, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	}))
	defer srv.Close()

	c := &Connector{Token: "TOKEN", Store: s, Workspace: ws, ConnectorRow: row, BaseURL: srv.URL}
	if _, err := c.SyncOnce(context.Background()); err == nil {
		t.Fatal("SyncOnce() error = nil, want error")
	} else if !strings.Contains(err.Error(), "Bad Gateway") {
		t.Errorf("SyncOnce() error = %v, want it to mention Bad Gateway", err)
	}

	// Cursor must not advance on failure.
	cursor, err := s.GetSyncCursor(row.ID)
	if err != nil {
		t.Fatalf("GetSyncCursor() error = %v", err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q, want empty after failed sync", cursor)
	}
}

func TestSyncOnceEmptyUpdates(t *testing.T) {
	s := openTestStore(t)
	api := &fakeAPI{} // no updates
	srv := httptest.NewServer(api.handler("TOKEN"))
	defer srv.Close()

	c, err := connect(s, "TOKEN", srv.URL)
	if err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	count, err := c.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if count != 0 {
		t.Errorf("SyncOnce() = %d, want 0", count)
	}
	cursor, err := s.GetSyncCursor(c.ConnectorRow.ID)
	if err != nil {
		t.Fatalf("GetSyncCursor() error = %v", err)
	}
	if cursor != "" {
		t.Errorf("cursor = %q, want empty when no updates were returned", cursor)
	}
}
