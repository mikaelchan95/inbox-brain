package email

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// literal adapts a string to imap.LiteralReader for APPEND.
type literal struct {
	*strings.Reader
}

func (l literal) Size() int64 { return int64(l.Reader.Len()) }

func newLiteral(s string) literal { return literal{strings.NewReader(s)} }

// startServer runs an in-memory IMAP server with one user and returns its
// address plus the user handle for appending messages.
func startServer(t *testing.T, username, password string) (string, *imapmemserver.User) {
	t.Helper()
	user := imapmemserver.NewUser(username, password)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create INBOX: %v", err)
	}
	mem := imapmemserver.New()
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		InsecureAuth: true,
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String(), user
}

func appendMail(t *testing.T, user *imapmemserver.User, raw string) {
	t.Helper()
	if _, err := user.Append("INBOX", newLiteral(raw), &imap.AppendOptions{Time: time.Now()}); err != nil {
		t.Fatalf("append: %v", err)
	}
}

func newTestConnector(t *testing.T, addr string) *Connector {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace() error = %v", err)
	}
	account := Account{Address: "inbox@thewinery.com.sg", Host: "ignored", Password: "secret"}
	row, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelEmail,
		Provider:    model.ProviderIMAP,
		Name:        account.Address,
		Status:      model.ConnectorActive,
	})
	if err != nil {
		t.Fatalf("UpsertConnector() error = %v", err)
	}
	return &Connector{
		Account:      account,
		Store:        s,
		Workspace:    ws,
		ConnectorRow: row,
		dial: func() (*imapclient.Client, error) {
			return imapclient.DialInsecure(addr, nil)
		},
	}
}

func TestSyncOnce(t *testing.T) {
	addr, user := startServer(t, "inbox@thewinery.com.sg", "secret")
	appendMail(t, user, plainInbound)
	appendMail(t, user, htmlWithAttachment)

	c := newTestConnector(t, addr)
	ctx := context.Background()

	n, err := c.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce() error = %v", err)
	}
	if n != 2 {
		t.Fatalf("SyncOnce() = %d, want 2", n)
	}

	// Same mailbox again: everything is behind the cursor.
	n, err = c.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce() second run error = %v", err)
	}
	if n != 0 {
		t.Fatalf("SyncOnce() second run = %d, want 0", n)
	}

	// New mail arrives: only it is fetched.
	appendMail(t, user, outboundFromSelf)
	n, err = c.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce() third run error = %v", err)
	}
	if n != 1 {
		t.Fatalf("SyncOnce() third run = %d, want 1", n)
	}

	// Two correspondents → two conversations, threaded by counterpart.
	convs, err := c.Store.ListConversations(store.ConversationFilter{})
	if err != nil {
		t.Fatalf("ListConversations() error = %v", err)
	}
	if len(convs) != 2 {
		t.Fatalf("len(conversations) = %d, want 2", len(convs))
	}
	byExternal := map[string]model.Conversation{}
	for _, cv := range convs {
		byExternal[cv.ExternalID] = cv
	}
	tan, ok := byExternal["mrs.tan@example.com"]
	if !ok {
		t.Fatalf("conversation for mrs.tan@example.com not found (have %v)", byExternal)
	}
	msgs, err := c.Store.ListMessages(tan.ID, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	// Inbound booking plus the owner's outbound reply share the conversation.
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}

	cursor, err := c.Store.GetSyncCursor(c.ConnectorRow.ID)
	if err != nil {
		t.Fatalf("GetSyncCursor() error = %v", err)
	}
	if !strings.Contains(cursor, ":") {
		t.Fatalf("cursor = %q, want uidvalidity:lastuid", cursor)
	}
}

func TestSyncOnceBadCredentials(t *testing.T) {
	addr, _ := startServer(t, "inbox@thewinery.com.sg", "secret")
	c := newTestConnector(t, addr)
	c.Account.Password = "wrong"
	if _, err := c.SyncOnce(context.Background()); err == nil {
		t.Fatal("SyncOnce() with bad password: expected error")
	}
}
