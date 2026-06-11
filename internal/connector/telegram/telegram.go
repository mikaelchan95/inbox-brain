package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

const defaultBaseURL = "https://api.telegram.org"

const (
	followPollTimeout = 30 // getUpdates long-poll timeout in seconds
	maxBackoff        = 60 * time.Second
	degradeAfter      = 3 // consecutive failures before marking degraded
)

// httpClient must outlast the long-poll timeout used by Follow.
var httpClient = &http.Client{Timeout: 45 * time.Second}

// Connector syncs one Telegram bot into the local store.
type Connector struct {
	Token        string
	Store        *store.Store
	Workspace    model.Workspace
	ConnectorRow model.Connector
	BaseURL      string // overrides https://api.telegram.org in tests

	backoffBase time.Duration // Follow's initial backoff; zero means 1s
}

// Connect validates the bot token via getMe and upserts the connector row
// (channel telegram, provider telegram_bot_api, named after the bot
// username, status active).
func Connect(s *store.Store, token string) (*Connector, error) {
	return connect(s, token, defaultBaseURL)
}

func connect(s *store.Store, token, baseURL string) (*Connector, error) {
	if token == "" {
		return nil, fmt.Errorf("telegram connect: no bot token; set TELEGRAM_BOT_TOKEN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := apiCall(ctx, baseURL, token, "getMe", nil)
	if err != nil {
		return nil, fmt.Errorf("telegram connect: %w (check TELEGRAM_BOT_TOKEN)", err)
	}
	var me struct {
		Username  string `json:"username"`
		FirstName string `json:"first_name"`
	}
	if err := json.Unmarshal(result, &me); err != nil {
		return nil, fmt.Errorf("telegram connect: parse getMe result: %w", err)
	}
	name := me.Username
	if name == "" {
		name = me.FirstName
	}
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		return nil, fmt.Errorf("telegram connect: %w", err)
	}
	row, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelTelegram,
		Provider:    model.ProviderTelegramBotAPI,
		Name:        name,
		Status:      model.ConnectorActive,
	})
	if err != nil {
		return nil, fmt.Errorf("telegram connect: %w", err)
	}
	return &Connector{Token: token, Store: s, Workspace: ws, ConnectorRow: row, BaseURL: baseURL}, nil
}

// SyncOnce fetches pending updates from the stored cursor, normalizes and
// stores them, advances the cursor to max update_id + 1, and returns the
// number of NEW messages stored (duplicates excluded).
func (c *Connector) SyncOnce(ctx context.Context) (int, error) {
	return c.sync(ctx, 0)
}

func (c *Connector) sync(ctx context.Context, timeoutSeconds int) (int, error) {
	cursor, err := c.Store.GetSyncCursor(c.ConnectorRow.ID)
	if err != nil {
		return 0, fmt.Errorf("telegram sync: %w", err)
	}
	params := url.Values{}
	if cursor != "" {
		if _, err := strconv.ParseInt(cursor, 10, 64); err != nil {
			return 0, fmt.Errorf("telegram sync: invalid cursor %q: %w", cursor, err)
		}
		params.Set("offset", cursor)
	}
	if timeoutSeconds > 0 {
		params.Set("timeout", strconv.Itoa(timeoutSeconds))
	}
	result, err := apiCall(ctx, c.baseURL(), c.Token, "getUpdates", params)
	if err != nil {
		return 0, err
	}
	var updates []json.RawMessage
	if err := json.Unmarshal(result, &updates); err != nil {
		return 0, fmt.Errorf("telegram getUpdates: parse result: %w", err)
	}

	count := 0
	maxID := int64(-1)
	for _, raw := range updates {
		var head struct {
			UpdateID int64 `json:"update_id"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return count, fmt.Errorf("telegram sync: parse update: %w", err)
		}
		if head.UpdateID > maxID {
			maxID = head.UpdateID
		}
		n, err := NormalizeUpdate(raw, c.ConnectorRow.ID, c.Workspace.ID)
		if err != nil {
			return count, fmt.Errorf("telegram sync: %w", err)
		}
		if n == nil {
			continue // no text/caption; still advances the cursor
		}
		conv, err := c.Store.UpsertConversation(n.Conversation)
		if err != nil {
			return count, fmt.Errorf("telegram sync: %w", err)
		}
		cust, err := c.Store.UpsertCustomer(n.Customer)
		if err != nil {
			return count, fmt.Errorf("telegram sync: %w", err)
		}
		msg := n.Message
		msg.ConversationID = conv.ID
		msg.CustomerID = cust.ID
		inserted, err := c.Store.InsertMessage(msg)
		if err != nil {
			return count, fmt.Errorf("telegram sync: %w", err)
		}
		if inserted {
			count++
		}
	}
	if maxID >= 0 {
		if err := c.Store.SetSyncCursor(c.ConnectorRow.ID, strconv.FormatInt(maxID+1, 10)); err != nil {
			return count, fmt.Errorf("telegram sync: %w", err)
		}
	}
	return count, nil
}

// Follow long-polls getUpdates until ctx is cancelled (then returns nil).
// On errors it backs off exponentially (capped at 60s); after 3 consecutive
// failures the connector is marked degraded with the error detail, and it
// recovers to active on the next success (spec §24.4).
func (c *Connector) Follow(ctx context.Context) error {
	failures := 0
	backoff := c.initialBackoff()
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := c.sync(ctx, followPollTimeout)
		if err == nil {
			if failures >= degradeAfter {
				_ = c.Store.SetConnectorStatus(c.ConnectorRow.ID, model.ConnectorActive, "")
			}
			failures = 0
			backoff = c.initialBackoff()
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

func (c *Connector) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Connector) initialBackoff() time.Duration {
	if c.backoffBase > 0 {
		return c.backoffBase
	}
	return time.Second
}

// apiEnvelope is the standard Telegram Bot API response wrapper.
type apiEnvelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// apiCall performs one Bot API method call and returns the raw result.
func apiCall(ctx context.Context, baseURL, token, method string, params url.Values) (json.RawMessage, error) {
	u := baseURL + "/bot" + token + "/" + method
	if enc := params.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %w", method, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("telegram %s: read response: %w", method, err)
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("telegram %s: unexpected response (status %s): %w", method, resp.Status, err)
	}
	if !env.OK {
		return nil, fmt.Errorf("telegram %s failed: %s (error code %d)", method, env.Description, env.ErrorCode)
	}
	return env.Result, nil
}
