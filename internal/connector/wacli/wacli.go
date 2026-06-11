// Package wacli imports WhatsApp chats and messages from a wacli SQLite
// database (github.com/steipete/wacli) into the Inbox Brain store. The wacli
// database is opened strictly read-only and is never written to (spec §4.2).
// The on-disk schema is detected adaptively from sqlite_master so minor wacli
// schema variations keep importing (spec §24.3).
package wacli

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"

	_ "modernc.org/sqlite"
)

// ImportSummary reports the result of one Import run.
type ImportSummary struct{ Conversations, Messages, Skipped int }

// connectorName is the fixed name of the single wacli connector row.
const connectorName = "wacli import"

// phoneJIDRe matches 1:1 WhatsApp JIDs whose local part is a phone number.
var phoneJIDRe = regexp.MustCompile(`^(\d+)@s\.whatsapp\.net$`)

// groupJIDSuffix marks WhatsApp group JIDs.
const groupJIDSuffix = "@g.us"

// schema describes the detected wacli table/column layout. Optional columns
// are "" when absent.
type schema struct {
	chatsTable string // optional: names come from messages/jid when missing
	chatJID    string
	chatName   string // optional

	msgTable      string
	msgChatJID    string
	msgID         string // optional: fall back to model.HashDedupeKey
	msgSender     string // optional
	msgSenderName string // optional
	msgTS         string
	msgText       string
	msgFromMe     string // optional: all messages inbound when absent
}

// Import reads chats and messages from the wacli SQLite database at dbPath
// (read-only) and stores them as normalized whatsapp conversations, customers
// and messages under workspace ws. Re-running Import is idempotent: already
// imported messages are deduped and counted in Skipped.
func Import(ctx context.Context, s *store.Store, ws model.Workspace, dbPath string) (ImportSummary, error) {
	var sum ImportSummary

	if _, err := os.Stat(dbPath); err != nil {
		return sum, fmt.Errorf(`wacli database not found at %s: install wacli and run "wacli sync" first: %w`, dbPath, err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=0")
	if err == nil {
		err = db.PingContext(ctx)
	}
	if err != nil {
		if db != nil {
			db.Close()
		}
		return sum, fmt.Errorf(`cannot open wacli database %s (run "wacli sync" first): %w`, dbPath, err)
	}
	defer db.Close()

	sc, tables, err := detectSchema(ctx, db)
	if err != nil {
		return sum, fmt.Errorf(`cannot read wacli database %s (run "wacli sync" first): %w`, dbPath, err)
	}
	if sc == nil {
		return sum, fmt.Errorf(
			`unrecognized wacli database schema at %s: found tables [%s], expected chats/messages tables with chat jid, timestamp and text columns; run "wacli sync" first`,
			dbPath, strings.Join(tables, ", "))
	}

	conn, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelWhatsApp,
		Provider:    model.ProviderWacli,
		Name:        connectorName,
		Status:      model.ConnectorActive,
	})
	if err != nil {
		return sum, fmt.Errorf("upsert wacli connector: %w", err)
	}

	chatNames, err := loadChatNames(ctx, db, sc)
	if err != nil {
		return sum, fmt.Errorf("read wacli chats: %w", err)
	}

	// convState tracks one conversation across the import so each chat is
	// upserted once and last_message_at can be advanced at the end.
	type convState struct {
		conv   model.Conversation
		custID string
		maxTS  time.Time
	}
	convs := map[string]*convState{}

	ensureConv := func(jid string) (*convState, error) {
		if st, ok := convs[jid]; ok {
			return st, nil
		}
		name := chatNames[jid]
		phone := phoneFromJID(jid)
		cust, err := s.UpsertCustomer(model.Customer{
			WorkspaceID: ws.ID,
			Channel:     model.ChannelWhatsApp,
			ExternalID:  jid,
			Name:        name,
			Phone:       phone,
		})
		if err != nil {
			return nil, fmt.Errorf("upsert customer %s: %w", jid, err)
		}
		title := name
		if title == "" {
			title = phone
		}
		if title == "" {
			title = jid
		}
		conv, err := s.UpsertConversation(model.Conversation{
			WorkspaceID: ws.ID,
			ConnectorID: conn.ID,
			Channel:     model.ChannelWhatsApp,
			ExternalID:  jid,
			Title:       title,
			IsGroup:     strings.HasSuffix(jid, groupJIDSuffix),
		})
		if err != nil {
			return nil, fmt.Errorf("upsert conversation %s: %w", jid, err)
		}
		st := &convState{conv: conv, custID: cust.ID}
		convs[jid] = st
		return st, nil
	}

	// Upsert every chat from the chats table, even ones without text messages.
	for jid := range chatNames {
		if _, err := ensureConv(jid); err != nil {
			return sum, err
		}
	}

	rows, err := db.QueryContext(ctx, messagesQuery(sc))
	if err != nil {
		return sum, fmt.Errorf("read wacli messages: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var chatJID, msgID, sender, senderName, text sql.NullString
		var ts sql.NullInt64
		var fromMe sql.NullBool
		if err := rows.Scan(&chatJID, &msgID, &sender, &senderName, &ts, &text, &fromMe); err != nil {
			return sum, fmt.Errorf("scan wacli message: %w", err)
		}
		if chatJID.String == "" || text.String == "" {
			continue // non-text (media/system) rows are not imported
		}
		st, err := ensureConv(chatJID.String)
		if err != nil {
			return sum, err
		}

		occurredAt := tsToTime(ts.Int64)
		direction := model.DirectionInbound
		if fromMe.Valid && fromMe.Bool {
			direction = model.DirectionOutbound
		}
		dedupeKey := ""
		if msgID.String != "" {
			dedupeKey = fmt.Sprintf("wacli:%s:%s:%s", conn.ID, chatJID.String, msgID.String)
		} else {
			dedupeKey = model.HashDedupeKey(model.ProviderWacli, chatJID.String,
				sender.String, strconv.FormatInt(ts.Int64, 10), text.String)
		}
		raw, err := json.Marshal(map[string]any{
			"chat_jid":    chatJID.String,
			"message_id":  msgID.String,
			"sender":      sender.String,
			"sender_name": senderName.String,
			"ts":          ts.Int64,
			"from_me":     fromMe.Valid && fromMe.Bool,
			"text":        text.String,
		})
		if err != nil {
			return sum, fmt.Errorf("encode wacli message %s: %w", dedupeKey, err)
		}

		inserted, err := s.InsertMessage(model.Message{
			WorkspaceID:            ws.ID,
			ConversationID:         st.conv.ID,
			CustomerID:             st.custID,
			Channel:                model.ChannelWhatsApp,
			Provider:               model.ProviderWacli,
			ConnectorID:            conn.ID,
			ConversationExternalID: chatJID.String,
			MessageExternalID:      msgID.String,
			SenderExternalID:       sender.String,
			SenderName:             senderName.String,
			SenderPhone:            phoneFromJID(sender.String),
			Body:                   text.String,
			BodyFormat:             "plain_text",
			Direction:              direction,
			OccurredAt:             occurredAt,
			RawJSON:                raw,
			DedupeKey:              dedupeKey,
		})
		if err != nil {
			return sum, fmt.Errorf("insert wacli message %s: %w", dedupeKey, err)
		}
		if inserted {
			sum.Messages++
		} else {
			sum.Skipped++
		}
		if occurredAt.After(st.maxTS) {
			st.maxTS = occurredAt
		}
	}
	if err := rows.Err(); err != nil {
		return sum, fmt.Errorf("read wacli messages: %w", err)
	}

	// Advance last_message_at (UpsertConversation only ever moves it forward).
	for _, st := range convs {
		if st.maxTS.IsZero() || !st.maxTS.After(st.conv.LastMessageAt) {
			continue
		}
		c := st.conv
		c.LastMessageAt = st.maxTS
		if _, err := s.UpsertConversation(c); err != nil {
			return sum, fmt.Errorf("update conversation %s: %w", c.ExternalID, err)
		}
	}

	sum.Conversations = len(convs)
	return sum, nil
}

// detectSchema inspects sqlite_master for a chats-like and a messages-like
// table and maps their flexible column names. It returns (nil, foundTables,
// nil) when no usable messages table exists; a chats table is optional
// (chat titles then fall back to phone/jid).
func detectSchema(ctx context.Context, db *sql.DB) (*schema, []string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list tables: %w", err)
	}

	var sc schema
	for _, t := range tables {
		switch strings.ToLower(t) {
		case "chats", "conversations":
			if sc.chatsTable != "" {
				continue
			}
			cols, err := tableColumns(ctx, db, t)
			if err != nil {
				return nil, tables, err
			}
			jid := pickColumn(cols, "jid", "chat_jid")
			if jid == "" {
				continue
			}
			sc.chatsTable = t
			sc.chatJID = jid
			sc.chatName = pickColumn(cols, "name", "chat_name", "title")
		case "messages", "message":
			if sc.msgTable != "" {
				continue
			}
			cols, err := tableColumns(ctx, db, t)
			if err != nil {
				return nil, tables, err
			}
			chatJID := pickColumn(cols, "chat_jid", "chat", "jid")
			ts := pickColumn(cols, "timestamp", "ts", "time")
			text := pickColumn(cols, "text", "body", "content")
			if chatJID == "" || ts == "" || text == "" {
				continue
			}
			sc.msgTable = t
			sc.msgChatJID = chatJID
			sc.msgTS = ts
			sc.msgText = text
			sc.msgID = pickColumn(cols, "id", "message_id", "msg_id")
			sc.msgSender = pickColumn(cols, "sender", "sender_jid")
			sc.msgSenderName = pickColumn(cols, "sender_name")
			sc.msgFromMe = pickColumn(cols, "is_from_me", "from_me")
		}
	}
	if sc.msgTable == "" {
		return nil, tables, nil
	}
	return &sc, tables, nil
}

// tableColumns returns the columns of table as a map of lowercase name to
// the actual on-disk name.
func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	defer rows.Close()
	cols := map[string]string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan column of %s: %w", table, err)
		}
		cols[strings.ToLower(name)] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns of %s: %w", table, err)
	}
	return cols, nil
}

// pickColumn returns the actual name of the first candidate present in cols,
// or "" when none match.
func pickColumn(cols map[string]string, candidates ...string) string {
	for _, c := range candidates {
		if actual, ok := cols[c]; ok {
			return actual
		}
	}
	return ""
}

// loadChatNames reads the chats table into a jid → name map. When no chats
// table was detected it returns an empty map and chats are discovered from
// the messages table instead.
func loadChatNames(ctx context.Context, db *sql.DB, sc *schema) (map[string]string, error) {
	names := map[string]string{}
	if sc.chatsTable == "" {
		return names, nil
	}
	nameExpr := "''"
	if sc.chatName != "" {
		nameExpr = quoteIdent(sc.chatName)
	}
	q := fmt.Sprintf("SELECT %s, %s FROM %s",
		quoteIdent(sc.chatJID), nameExpr, quoteIdent(sc.chatsTable))
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", sc.chatsTable, err)
	}
	defer rows.Close()
	for rows.Next() {
		var jid, name sql.NullString
		if err := rows.Scan(&jid, &name); err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}
		if jid.String == "" {
			continue
		}
		names[jid.String] = name.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query %s: %w", sc.chatsTable, err)
	}
	return names, nil
}

// messagesQuery builds the SELECT over the detected messages table; missing
// optional columns are selected as literals so Scan sees a fixed shape.
func messagesQuery(sc *schema) string {
	expr := func(col, missing string) string {
		if col == "" {
			return missing
		}
		return quoteIdent(col)
	}
	return fmt.Sprintf("SELECT %s, %s, %s, %s, %s, %s, %s FROM %s ORDER BY %s ASC",
		quoteIdent(sc.msgChatJID),
		expr(sc.msgID, "''"),
		expr(sc.msgSender, "''"),
		expr(sc.msgSenderName, "''"),
		quoteIdent(sc.msgTS),
		quoteIdent(sc.msgText),
		expr(sc.msgFromMe, "0"),
		quoteIdent(sc.msgTable),
		quoteIdent(sc.msgTS))
}

// quoteIdent quotes a SQLite identifier.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// tsToTime converts a wacli timestamp to time.Time. Values above 1e12 are
// Unix milliseconds, smaller positive values Unix seconds, <= 0 the zero time.
func tsToTime(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	if v > 1_000_000_000_000 {
		return time.UnixMilli(v).UTC()
	}
	return time.Unix(v, 0).UTC()
}

// phoneFromJID extracts the phone number from a 1:1 WhatsApp JID such as
// "6591234567@s.whatsapp.net"; it returns "" for groups and other JIDs.
func phoneFromJID(jid string) string {
	m := phoneJIDRe.FindStringSubmatch(jid)
	if m == nil {
		return ""
	}
	return m[1]
}
