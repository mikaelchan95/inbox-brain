package email

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

const (
	defaultInitialDays = 30 // first-sync window when no cursor exists
	pollInterval       = 60 * time.Second
	maxBackoff         = 10 * time.Minute
	degradeAfter       = 3 // consecutive failures before marking degraded
)

// Connector syncs one IMAP mailbox into the local store.
type Connector struct {
	Account      Account
	Store        *store.Store
	Workspace    model.Workspace
	ConnectorRow model.Connector

	dial        func() (*imapclient.Client, error) // overrides TLS dialing in tests
	backoffBase time.Duration                      // Follow's initial backoff; zero means 1s
}

// Connect validates the account by logging in and selecting its folder, then
// upserts the connector row (channel email, provider imap, named after the
// address, status active).
func Connect(s *store.Store, account Account) (*Connector, error) {
	c := &Connector{Account: account, Store: s}
	client, err := c.login()
	if err != nil {
		return nil, fmt.Errorf("email connect %s: %w", account.Address, err)
	}
	defer client.Close()
	if _, err := client.Select(account.FolderOrDefault(), &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		return nil, fmt.Errorf("email connect %s: select %s: %w", account.Address, account.FolderOrDefault(), err)
	}
	_ = client.Logout().Wait()

	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		return nil, fmt.Errorf("email connect: %w", err)
	}
	row, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelEmail,
		Provider:    model.ProviderIMAP,
		Name:        account.Address,
		Status:      model.ConnectorActive,
	})
	if err != nil {
		return nil, fmt.Errorf("email connect: %w", err)
	}
	c.Workspace = ws
	c.ConnectorRow = row
	return c, nil
}

// login dials the IMAP server and authenticates.
func (c *Connector) login() (*imapclient.Client, error) {
	dial := c.dial
	if dial == nil {
		dial = func() (*imapclient.Client, error) {
			return imapclient.DialTLS(c.Account.Addr(), nil)
		}
	}
	client, err := dial()
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", c.Account.Addr(), err)
	}
	if err := client.Login(c.Account.Address, c.Account.Password).Wait(); err != nil {
		client.Close()
		return nil, fmt.Errorf("login: %w (for Gmail/Yahoo use an app password)", err)
	}
	return client, nil
}

// SyncOnce fetches messages newer than the stored cursor (or the initial-days
// window on first sync), normalizes and stores them, advances the cursor to
// "uidvalidity:maxuid", and returns the number of NEW messages stored
// (duplicates excluded). Messages are fetched with BODY.PEEK so they stay
// unread in the mailbox.
func (c *Connector) SyncOnce(ctx context.Context) (int, error) {
	cursor, err := c.Store.GetSyncCursor(c.ConnectorRow.ID)
	if err != nil {
		return 0, fmt.Errorf("email sync: %w", err)
	}
	cursorValidity, lastUID, err := parseCursor(cursor)
	if err != nil {
		return 0, fmt.Errorf("email sync: %w", err)
	}

	client, err := c.login()
	if err != nil {
		return 0, fmt.Errorf("email sync %s: %w", c.Account.Address, err)
	}
	defer client.Close()

	folder := c.Account.FolderOrDefault()
	selData, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return 0, fmt.Errorf("email sync %s: select %s: %w", c.Account.Address, folder, err)
	}
	// A changed UIDVALIDITY means the server renumbered the mailbox: stored
	// UIDs are meaningless, so start over (dedupe keys absorb the refetch).
	if cursorValidity != selData.UIDValidity {
		lastUID = 0
	}

	criteria := &imap.SearchCriteria{}
	if lastUID > 0 {
		var set imap.UIDSet
		set.AddRange(imap.UID(lastUID+1), 0) // N:*
		criteria.UID = []imap.UIDSet{set}
	} else {
		days := c.Account.InitialDays
		if days <= 0 {
			days = defaultInitialDays
		}
		criteria.Since = time.Now().AddDate(0, 0, -days)
	}
	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return 0, fmt.Errorf("email sync %s: search: %w", c.Account.Address, err)
	}
	// "UID N:*" always matches at least the newest message, even when its UID
	// is below N — filter those out.
	var uids []imap.UID
	for _, uid := range searchData.AllUIDs() {
		if uint32(uid) > lastUID {
			uids = append(uids, uid)
		}
	}

	count := 0
	maxUID := lastUID
	if len(uids) > 0 {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		section := &imap.FetchItemBodySection{Peek: true}
		fetchCmd := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
			UID:          true,
			InternalDate: true,
			BodySection:  []*imap.FetchItemBodySection{section},
		})
		msgs, err := fetchCmd.Collect()
		if err != nil {
			return 0, fmt.Errorf("email sync %s: fetch: %w", c.Account.Address, err)
		}
		for _, msg := range msgs {
			if uint32(msg.UID) > maxUID {
				maxUID = uint32(msg.UID)
			}
			raw := msg.FindBodySection(section)
			if raw == nil {
				continue
			}
			n, err := NormalizeEmail(raw, msg.UID, selData.UIDValidity, msg.InternalDate, c.Account, c.ConnectorRow.ID, c.Workspace.ID)
			if err != nil {
				return count, fmt.Errorf("email sync: %w", err)
			}
			if n == nil {
				continue // unparseable or empty; still advances the cursor
			}
			conv, err := c.Store.UpsertConversation(n.Conversation)
			if err != nil {
				return count, fmt.Errorf("email sync: %w", err)
			}
			cust, err := c.Store.UpsertCustomer(n.Customer)
			if err != nil {
				return count, fmt.Errorf("email sync: %w", err)
			}
			m := n.Message
			m.ConversationID = conv.ID
			m.CustomerID = cust.ID
			inserted, err := c.Store.InsertMessage(m)
			if err != nil {
				return count, fmt.Errorf("email sync: %w", err)
			}
			if inserted {
				count++
			}
		}
	}
	_ = client.Logout().Wait()

	newCursor := fmt.Sprintf("%d:%d", selData.UIDValidity, maxUID)
	if newCursor != cursor {
		if err := c.Store.SetSyncCursor(c.ConnectorRow.ID, newCursor); err != nil {
			return count, fmt.Errorf("email sync: %w", err)
		}
	}
	return count, nil
}

// Follow polls SyncOnce every minute until ctx is cancelled (then returns
// nil). On errors it backs off exponentially (capped at 10m); after 3
// consecutive failures the connector is marked degraded with the error
// detail, and it recovers to active on the next success (spec §24.4).
func (c *Connector) Follow(ctx context.Context) error {
	failures := 0
	backoff := c.initialBackoff()
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := c.SyncOnce(ctx)
		if err == nil {
			if failures >= degradeAfter {
				_ = c.Store.SetConnectorStatus(c.ConnectorRow.ID, model.ConnectorActive, "")
			}
			failures = 0
			backoff = c.initialBackoff()
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pollInterval):
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		failures++
		if failures >= degradeAfter {
			_ = c.Store.SetConnectorStatus(c.ConnectorRow.ID, model.ConnectorDegraded, err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Connector) initialBackoff() time.Duration {
	if c.backoffBase > 0 {
		return c.backoffBase
	}
	return time.Second
}

// parseCursor splits a "uidvalidity:lastuid" cursor; an empty cursor is
// (0, 0, nil).
func parseCursor(cursor string) (validity, lastUID uint32, err error) {
	if cursor == "" {
		return 0, 0, nil
	}
	v, u, ok := strings.Cut(cursor, ":")
	if !ok {
		return 0, 0, fmt.Errorf("invalid cursor %q", cursor)
	}
	v64, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor %q: %w", cursor, err)
	}
	u64, err := strconv.ParseUint(u, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid cursor %q: %w", cursor, err)
	}
	return uint32(v64), uint32(u64), nil
}
