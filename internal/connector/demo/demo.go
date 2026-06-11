// Package demo seeds the store with embedded demo scenarios (spec §26) so
// the classification, extraction and leak-detection flows can be exercised
// without connecting a real channel. Seeding is idempotent: messages carry
// stable dedupe keys, so re-seeding adds nothing.
package demo

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

//go:embed scenarios/*.json
var scenarioFS embed.FS

// Summary reports what one Seed call added; an idempotent re-seed adds 0.
type Summary struct{ Conversations, Messages int }

// Scenarios lists the available demo scenario names.
func Scenarios() []string {
	return []string{"tuition-center", "design-studio"}
}

// scenarioFile is the embedded JSON shape: conversations with a title, group
// flag and contact, holding messages with a sender ("them" or "me"), text and
// a relative age in minutes.
type scenarioFile struct {
	Conversations []scenarioConversation `json:"conversations"`
}

type scenarioConversation struct {
	Title    string            `json:"title"`
	IsGroup  bool              `json:"isGroup"`
	Contact  scenarioContact   `json:"contact"`
	Messages []scenarioMessage `json:"messages"`
}

type scenarioContact struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type scenarioMessage struct {
	Sender     string `json:"sender"` // "them" or "me"
	Text       string `json:"text"`
	MinutesAgo int    `json:"minutesAgo"`
}

// Seed upserts the demo connector for scenario (channel demo, provider
// manual_demo, name "demo: <scenario>") and inserts the scenario's
// conversations and messages. Message times are computed relative to the
// seeding time from each message's minutesAgo. An unknown scenario returns an
// error listing the valid names.
func Seed(s *store.Store, ws model.Workspace, scenario string) (Summary, error) {
	if !slices.Contains(Scenarios(), scenario) {
		return Summary{}, fmt.Errorf("unknown scenario %q: valid scenarios are %s",
			scenario, strings.Join(Scenarios(), ", "))
	}
	data, err := scenarioFS.ReadFile("scenarios/" + scenario + ".json")
	if err != nil {
		return Summary{}, fmt.Errorf("read scenario %s: %w", scenario, err)
	}
	var sc scenarioFile
	if err := json.Unmarshal(data, &sc); err != nil {
		return Summary{}, fmt.Errorf("parse scenario %s: %w", scenario, err)
	}

	conn, err := s.UpsertConnector(model.Connector{
		WorkspaceID: ws.ID,
		Channel:     model.ChannelDemo,
		Provider:    model.ProviderManualDemo,
		Name:        "demo: " + scenario,
		Status:      model.ConnectorActive,
	})
	if err != nil {
		return Summary{}, fmt.Errorf("upsert demo connector: %w", err)
	}

	// Existing demo conversations for this connector, so Summary counts only
	// newly created ones on re-seed.
	demoConvs, err := s.ListConversations(store.ConversationFilter{Channel: model.ChannelDemo})
	if err != nil {
		return Summary{}, fmt.Errorf("list demo conversations: %w", err)
	}
	existing := make(map[string]bool, len(demoConvs))
	for _, c := range demoConvs {
		if c.ConnectorID == conn.ID {
			existing[c.ExternalID] = true
		}
	}

	now := time.Now()
	var sum Summary
	for _, sconv := range sc.Conversations {
		slug := slugify(sconv.Title)
		var lastAt time.Time
		for _, m := range sconv.Messages {
			if at := now.Add(-time.Duration(m.MinutesAgo) * time.Minute); at.After(lastAt) {
				lastAt = at
			}
		}
		conv, err := s.UpsertConversation(model.Conversation{
			WorkspaceID:   ws.ID,
			ConnectorID:   conn.ID,
			Channel:       model.ChannelDemo,
			ExternalID:    slug,
			Title:         sconv.Title,
			IsGroup:       sconv.IsGroup,
			LastMessageAt: lastAt,
		})
		if err != nil {
			return sum, fmt.Errorf("upsert conversation %q: %w", sconv.Title, err)
		}
		cust, err := s.UpsertCustomer(model.Customer{
			WorkspaceID: ws.ID,
			Channel:     model.ChannelDemo,
			ExternalID:  slug,
			Name:        sconv.Contact.Name,
			Phone:       sconv.Contact.Phone,
		})
		if err != nil {
			return sum, fmt.Errorf("upsert customer for %q: %w", sconv.Title, err)
		}
		if !existing[slug] {
			existing[slug] = true
			sum.Conversations++
		}
		for i, sm := range sconv.Messages {
			msg := model.Message{
				WorkspaceID:            ws.ID,
				ConversationID:         conv.ID,
				Channel:                model.ChannelDemo,
				Provider:               model.ProviderManualDemo,
				ConnectorID:            conn.ID,
				ConversationExternalID: slug,
				MessageExternalID:      fmt.Sprintf("%s-%d", slug, i),
				Body:                   sm.Text,
				BodyFormat:             "plain_text",
				OccurredAt:             now.Add(-time.Duration(sm.MinutesAgo) * time.Minute),
				DedupeKey:              fmt.Sprintf("%s:%s:%s:%d", model.ProviderManualDemo, conn.ID, slug, i),
			}
			switch sm.Sender {
			case "them":
				msg.Direction = model.DirectionInbound
				msg.CustomerID = cust.ID
				msg.SenderExternalID = slug
				msg.SenderName = sconv.Contact.Name
				msg.SenderPhone = sconv.Contact.Phone
			case "me":
				msg.Direction = model.DirectionOutbound
				msg.SenderExternalID = "me"
				msg.SenderName = "Me"
			default:
				return sum, fmt.Errorf("scenario %s, conversation %q, message %d: unknown sender %q",
					scenario, sconv.Title, i, sm.Sender)
			}
			inserted, err := s.InsertMessage(msg)
			if err != nil {
				return sum, fmt.Errorf("insert message %d of %q: %w", i, sconv.Title, err)
			}
			if inserted {
				sum.Messages++
			}
		}
	}
	return sum, nil
}

// slugify turns a conversation title into a stable external id / dedupe-key
// component: lowercase alphanumerics with single dashes between runs.
func slugify(s string) string {
	var b strings.Builder
	dash := true // suppresses leading dashes
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}
