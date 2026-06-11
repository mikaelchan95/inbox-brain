package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

var base = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return base.Add(d) }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func wantTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestOpenMigrateAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbox.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version = %d, want %d", version, len(migrations))
	}
	ws, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: migrations are idempotent and data persists.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if err := s2.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("user_version after reopen: %v", err)
	}
	if version != len(migrations) {
		t.Fatalf("user_version after reopen = %d, want %d", version, len(migrations))
	}
	ws2, err := s2.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("EnsureDefaultWorkspace after reopen: %v", err)
	}
	if ws2.ID != ws.ID {
		t.Errorf("workspace id changed across reopen: %q != %q", ws2.ID, ws.ID)
	}
}

func TestEnsureDefaultWorkspaceIsStable(t *testing.T) {
	s := newTestStore(t)
	a, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if a.ID == "" || a.Name == "" || a.CreatedAt.IsZero() {
		t.Fatalf("incomplete workspace: %+v", a)
	}
	b, err := s.EnsureDefaultWorkspace()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if b.ID != a.ID {
		t.Errorf("got new workspace %q, want existing %q", b.ID, a.ID)
	}
}

func TestSettings(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSetting("missing")
	if err != nil || got != "" {
		t.Fatalf("missing key: got (%q, %v), want (\"\", nil)", got, err)
	}
	if err := s.SetSetting("k", "v1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := s.SetSetting("k", "v2"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}
	got, err = s.GetSetting("k")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "v2" {
		t.Errorf("GetSetting = %q, want %q", got, "v2")
	}
}

func TestConnectorRoundTripAndUpsert(t *testing.T) {
	s := newTestStore(t)
	in := model.Connector{
		WorkspaceID:  "ws_1",
		Channel:      model.ChannelTelegram,
		Provider:     model.ProviderTelegramBotAPI,
		Name:         "my-bot",
		Status:       model.ConnectorActive,
		StatusDetail: "ok",
		CreatedAt:    at(0),
		UpdatedAt:    at(time.Minute),
	}
	first, err := s.UpsertConnector(in)
	if err != nil {
		t.Fatalf("UpsertConnector: %v", err)
	}
	if first.ID == "" {
		t.Fatal("no id assigned")
	}
	got, err := s.GetConnector(first.ID)
	if err != nil {
		t.Fatalf("GetConnector: %v", err)
	}
	if got == nil {
		t.Fatal("GetConnector returned nil for existing connector")
	}
	if got.WorkspaceID != in.WorkspaceID || got.Channel != in.Channel ||
		got.Provider != in.Provider || got.Name != in.Name ||
		got.Status != in.Status || got.StatusDetail != in.StatusDetail {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "CreatedAt", got.CreatedAt, in.CreatedAt)
	wantTime(t, "UpdatedAt", got.UpdatedAt, in.UpdatedAt)

	// Upsert same unique key: id/created_at preserved, status updated.
	in.Status = model.ConnectorDegraded
	in.StatusDetail = "rate limited"
	in.UpdatedAt = at(2 * time.Minute)
	second, err := s.UpsertConnector(in)
	if err != nil {
		t.Fatalf("UpsertConnector again: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created new row: %q != %q", second.ID, first.ID)
	}
	wantTime(t, "CreatedAt preserved", second.CreatedAt, at(0))
	wantTime(t, "UpdatedAt", second.UpdatedAt, at(2*time.Minute))
	if second.Status != model.ConnectorDegraded || second.StatusDetail != "rate limited" {
		t.Errorf("status not updated: %+v", second)
	}

	all, err := s.ListConnectors()
	if err != nil {
		t.Fatalf("ListConnectors: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListConnectors len = %d, want 1", len(all))
	}
}

func TestGetConnectorAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetConnector("conn_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestSetConnectorStatus(t *testing.T) {
	s := newTestStore(t)
	c, err := s.UpsertConnector(model.Connector{
		WorkspaceID: "ws_1", Channel: model.ChannelDemo, Provider: model.ProviderManualDemo,
		Name: "demo", Status: model.ConnectorActive,
	})
	if err != nil {
		t.Fatalf("UpsertConnector: %v", err)
	}
	if err := s.SetConnectorStatus(c.ID, model.ConnectorError, "boom"); err != nil {
		t.Fatalf("SetConnectorStatus: %v", err)
	}
	got, err := s.GetConnector(c.ID)
	if err != nil || got == nil {
		t.Fatalf("GetConnector: (%v, %v)", got, err)
	}
	if got.Status != model.ConnectorError || got.StatusDetail != "boom" {
		t.Errorf("status not updated: %+v", got)
	}
}

func TestCustomerUpsert(t *testing.T) {
	s := newTestStore(t)
	in := model.Customer{
		WorkspaceID: "ws_1", Channel: model.ChannelWhatsApp, ExternalID: "6591234567@s.whatsapp.net",
		Name: "Mrs Tan", Handle: "mrstan", Phone: "+65 9123 4567",
		CreatedAt: at(0), UpdatedAt: at(0),
	}
	first, err := s.UpsertCustomer(in)
	if err != nil {
		t.Fatalf("UpsertCustomer: %v", err)
	}
	got, err := s.GetCustomer(first.ID)
	if err != nil || got == nil {
		t.Fatalf("GetCustomer: (%v, %v)", got, err)
	}
	if got.Name != in.Name || got.Handle != in.Handle || got.Phone != in.Phone ||
		got.Channel != in.Channel || got.ExternalID != in.ExternalID {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "CreatedAt", got.CreatedAt, at(0))

	// Empty fields must not erase stored values; non-empty fields win.
	second, err := s.UpsertCustomer(model.Customer{
		WorkspaceID: "ws_1", Channel: model.ChannelWhatsApp, ExternalID: "6591234567@s.whatsapp.net",
		Name: "Mrs Tan (Tuition)", Handle: "", Phone: "",
		UpdatedAt: at(time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertCustomer again: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created new row: %q != %q", second.ID, first.ID)
	}
	if second.Name != "Mrs Tan (Tuition)" {
		t.Errorf("Name = %q, want updated", second.Name)
	}
	if second.Handle != "mrstan" || second.Phone != "+65 9123 4567" {
		t.Errorf("empty values erased data: %+v", second)
	}
	wantTime(t, "CreatedAt preserved", second.CreatedAt, at(0))
	wantTime(t, "UpdatedAt", second.UpdatedAt, at(time.Hour))
}

func TestGetCustomerAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetCustomer("cust_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestConversationUpsert(t *testing.T) {
	s := newTestStore(t)
	in := model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelTelegram,
		ExternalID: "12345", Title: "Mrs Tan", IsGroup: false,
		LastMessageAt: at(time.Hour), CreatedAt: at(0), UpdatedAt: at(0),
	}
	first, err := s.UpsertConversation(in)
	if err != nil {
		t.Fatalf("UpsertConversation: %v", err)
	}
	got, err := s.GetConversation(first.ID)
	if err != nil || got == nil {
		t.Fatalf("GetConversation: (%v, %v)", got, err)
	}
	if got.Title != "Mrs Tan" || got.Channel != model.ChannelTelegram || got.IsGroup {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "LastMessageAt", got.LastMessageAt, at(time.Hour))

	// Older message + empty title: keeps both.
	second, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelTelegram,
		ExternalID: "12345", Title: "", LastMessageAt: at(30 * time.Minute),
		UpdatedAt: at(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertConversation older: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("upsert created new row: %q != %q", second.ID, first.ID)
	}
	if second.Title != "Mrs Tan" {
		t.Errorf("empty title erased stored title: %q", second.Title)
	}
	wantTime(t, "LastMessageAt monotonic", second.LastMessageAt, at(time.Hour))
	wantTime(t, "CreatedAt preserved", second.CreatedAt, at(0))

	// Newer message + new title: updates both.
	third, err := s.UpsertConversation(model.Conversation{
		WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: model.ChannelTelegram,
		ExternalID: "12345", Title: "Mrs Tan (P5 Math)", LastMessageAt: at(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertConversation newer: %v", err)
	}
	if third.Title != "Mrs Tan (P5 Math)" {
		t.Errorf("Title = %q, want updated", third.Title)
	}
	wantTime(t, "LastMessageAt updated", third.LastMessageAt, at(3*time.Hour))
}

func TestGetConversationAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetConversation("conv_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestListConversations(t *testing.T) {
	s := newTestStore(t)
	mk := func(ext, channel, title string, last time.Time) model.Conversation {
		c, err := s.UpsertConversation(model.Conversation{
			WorkspaceID: "ws_1", ConnectorID: "conn_1", Channel: channel,
			ExternalID: ext, Title: title, LastMessageAt: last,
		})
		if err != nil {
			t.Fatalf("UpsertConversation %s: %v", title, err)
		}
		return c
	}
	tg := mk("1", model.ChannelTelegram, "TG Biz", at(3*time.Hour))
	waBiz := mk("2", model.ChannelWhatsApp, "WA Biz", at(2*time.Hour))
	waPers := mk("3", model.ChannelWhatsApp, "WA Personal", at(1*time.Hour))
	waOver := mk("4", model.ChannelWhatsApp, "WA Override", at(4*time.Hour))

	save := func(convID, label, override string) {
		err := s.SaveConversationClassification(model.ConversationClassification{
			ConversationID: convID, Classification: label, BusinessConfidence: 50,
			Source: model.SourceRules, UserOverride: override,
		})
		if err != nil {
			t.Fatalf("SaveConversationClassification: %v", err)
		}
	}
	save(tg.ID, model.ConvBusiness, "")
	save(waBiz.ID, model.ConvBusiness, "")
	save(waPers.ID, model.ConvPersonal, "")
	save(waOver.ID, model.ConvPersonal, model.ConvBusiness) // override wins

	tests := []struct {
		name   string
		filter ConversationFilter
		want   []string
	}{
		{"all newest first", ConversationFilter{}, []string{waOver.ID, tg.ID, waBiz.ID, waPers.ID}},
		{"channel", ConversationFilter{Channel: model.ChannelWhatsApp}, []string{waOver.ID, waBiz.ID, waPers.ID}},
		{"classification business uses effective label", ConversationFilter{Classification: model.ConvBusiness}, []string{waOver.ID, tg.ID, waBiz.ID}},
		{"classification personal excludes overridden", ConversationFilter{Classification: model.ConvPersonal}, []string{waPers.ID}},
		{"channel and classification", ConversationFilter{Channel: model.ChannelWhatsApp, Classification: model.ConvBusiness}, []string{waOver.ID, waBiz.ID}},
		{"limit", ConversationFilter{Limit: 2}, []string{waOver.ID, tg.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListConversations(tt.filter)
			if err != nil {
				t.Fatalf("ListConversations: %v", err)
			}
			var ids []string
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestInsertMessageRoundTripAndDedupe(t *testing.T) {
	s := newTestStore(t)
	in := model.Message{
		WorkspaceID: "ws_1", ConversationID: "conv_1", CustomerID: "cust_1",
		Channel: model.ChannelTelegram, Provider: model.ProviderTelegramBotAPI, ConnectorID: "conn_1",
		ConversationExternalID: "12345", MessageExternalID: "67",
		SenderExternalID: "999", SenderName: "Mrs Tan", SenderHandle: "mrstan", SenderPhone: "+65 9123 4567",
		Body: "Can I get a quote for the trial class?", BodyFormat: "plain_text",
		Direction:  model.DirectionInbound,
		OccurredAt: at(time.Hour), IngestedAt: at(time.Hour + time.Minute),
		ReplyToExternalMessageID: "66",
		Media: []model.MessageMedia{
			{Type: "image", URL: "https://example.test/x.jpg", FileName: "x.jpg", MimeType: "image/jpeg"},
		},
		RawJSON:   []byte(`{"update_id":1}`),
		DedupeKey: "telegram_bot_api:conn_1:12345:67",
	}
	ok, err := s.InsertMessage(in)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if !ok {
		t.Fatal("first insert returned false")
	}

	// Same dedupe key: (false, nil), no second row.
	dup := in
	dup.ID = ""
	dup.Body = "different body, same dedupe key"
	ok, err = s.InsertMessage(dup)
	if err != nil {
		t.Fatalf("duplicate InsertMessage: %v", err)
	}
	if ok {
		t.Fatal("duplicate insert returned true, want false")
	}
	n, err := s.CountMessages("conv_1")
	if err != nil {
		t.Fatalf("CountMessages: %v", err)
	}
	if n != 1 {
		t.Fatalf("CountMessages = %d, want 1", n)
	}

	msgs, err := s.ListMessages("conv_1", 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("ListMessages len = %d, want 1", len(msgs))
	}
	got := msgs[0]
	if got.Body != in.Body || got.SenderName != in.SenderName || got.SenderHandle != in.SenderHandle ||
		got.SenderPhone != in.SenderPhone || got.Direction != in.Direction ||
		got.BodyFormat != in.BodyFormat || got.DedupeKey != in.DedupeKey ||
		got.ReplyToExternalMessageID != in.ReplyToExternalMessageID ||
		got.ConversationExternalID != in.ConversationExternalID ||
		got.MessageExternalID != in.MessageExternalID || got.CustomerID != in.CustomerID {
		t.Errorf("round trip mismatch: %+v", got)
	}
	wantTime(t, "OccurredAt", got.OccurredAt, in.OccurredAt)
	wantTime(t, "IngestedAt", got.IngestedAt, in.IngestedAt)
	if !reflect.DeepEqual(got.Media, in.Media) {
		t.Errorf("Media = %+v, want %+v", got.Media, in.Media)
	}
	if string(got.RawJSON) != string(in.RawJSON) {
		t.Errorf("RawJSON = %q, want %q", got.RawJSON, in.RawJSON)
	}

	byID, err := s.GetMessage(got.ID)
	if err != nil || byID == nil {
		t.Fatalf("GetMessage: (%v, %v)", byID, err)
	}
	if byID.Body != in.Body {
		t.Errorf("GetMessage body mismatch: %q", byID.Body)
	}
}

func TestGetMessageAbsent(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetMessage("msg_nope")
	if err != nil || got != nil {
		t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
	}
}

func TestListMessagesChronologicalAndLimit(t *testing.T) {
	s := newTestStore(t)
	// Insert out of order; expect chronological output.
	times := []time.Duration{3 * time.Hour, time.Hour, 2 * time.Hour}
	for i, d := range times {
		_, err := s.InsertMessage(model.Message{
			WorkspaceID: "ws_1", ConversationID: "conv_1",
			Channel: model.ChannelDemo, Provider: model.ProviderManualDemo, ConnectorID: "conn_1",
			Body: "m", OccurredAt: at(d), IngestedAt: at(d),
			DedupeKey: model.HashDedupeKey("demo", "conv_1", "s", at(d).String(), string(rune('a'+i))),
		})
		if err != nil {
			t.Fatalf("InsertMessage %d: %v", i, err)
		}
	}
	msgs, err := s.ListMessages("conv_1", 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("len = %d, want 3", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].OccurredAt.Before(msgs[i-1].OccurredAt) {
			t.Errorf("messages not chronological: %v before %v", msgs[i].OccurredAt, msgs[i-1].OccurredAt)
		}
	}
	limited, err := s.ListMessages("conv_1", 2)
	if err != nil {
		t.Fatalf("ListMessages limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited len = %d, want 2", len(limited))
	}
	wantTime(t, "first limited", limited[0].OccurredAt, at(time.Hour))
}

func TestSyncCursors(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSyncCursor("conn_1")
	if err != nil || got != "" {
		t.Fatalf("missing cursor: got (%q, %v), want (\"\", nil)", got, err)
	}
	if err := s.SetSyncCursor("conn_1", "100"); err != nil {
		t.Fatalf("SetSyncCursor: %v", err)
	}
	if err := s.SetSyncCursor("conn_1", "200"); err != nil {
		t.Fatalf("SetSyncCursor overwrite: %v", err)
	}
	got, err = s.GetSyncCursor("conn_1")
	if err != nil {
		t.Fatalf("GetSyncCursor: %v", err)
	}
	if got != "200" {
		t.Errorf("cursor = %q, want %q", got, "200")
	}
}
