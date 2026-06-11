package demo

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

func newTestStore(t *testing.T) (*store.Store, model.Workspace) {
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

// conversationsByTitle seeds nothing; it maps the stored demo conversations
// by title for easy lookup.
func conversationsByTitle(t *testing.T, s *store.Store) map[string]model.Conversation {
	t.Helper()
	convs, err := s.ListConversations(store.ConversationFilter{Channel: model.ChannelDemo})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	out := make(map[string]model.Conversation, len(convs))
	for _, c := range convs {
		out[c.Title] = c
	}
	return out
}

func TestScenarios(t *testing.T) {
	got := Scenarios()
	want := []string{"tuition-center", "design-studio"}
	if len(got) != len(want) {
		t.Fatalf("Scenarios() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Scenarios() = %v, want %v", got, want)
		}
	}
}

func TestSeedUnknownScenario(t *testing.T) {
	s, ws := newTestStore(t)
	_, err := Seed(s, ws, "coffee-shop")
	if err == nil {
		t.Fatal("Seed with unknown scenario: want error, got nil")
	}
	for _, name := range Scenarios() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not list valid scenario %q", err, name)
		}
	}
}

func TestSeed(t *testing.T) {
	tests := []struct {
		scenario      string
		conversations int
		messages      int
		perChat       map[string]int
		groups        map[string]bool
	}{
		{
			scenario:      "tuition-center",
			conversations: 7,
			messages:      30,
			perChat: map[string]int{
				"Mum":                   4,
				"Family Group":          4,
				"Football Group":        4,
				"Mrs Tan":               5,
				"Alex":                  7,
				"Design Referrals":      4,
				"Unknown +65 9123 4567": 2,
			},
			groups: map[string]bool{
				"Family Group":     true,
				"Football Group":   true,
				"Design Referrals": true,
			},
		},
		{
			scenario:      "design-studio",
			conversations: 5,
			messages:      15,
			perChat: map[string]int{
				"Jasmine Lim":           3,
				"Marcus (Hartono & Co)": 3,
				"Priya":                 3,
				"Wei Jie":               3,
				"Sarah":                 3,
			},
			groups: map[string]bool{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.scenario, func(t *testing.T) {
			s, ws := newTestStore(t)
			started := time.Now()

			sum, err := Seed(s, ws, tt.scenario)
			if err != nil {
				t.Fatalf("Seed: %v", err)
			}
			if sum.Conversations != tt.conversations || sum.Messages != tt.messages {
				t.Errorf("Seed summary = %+v, want {Conversations:%d Messages:%d}",
					sum, tt.conversations, tt.messages)
			}

			// Connector upserted with the demo channel/provider/name.
			conns, err := s.ListConnectors()
			if err != nil {
				t.Fatalf("list connectors: %v", err)
			}
			if len(conns) != 1 {
				t.Fatalf("got %d connectors, want 1", len(conns))
			}
			conn := conns[0]
			if conn.Channel != model.ChannelDemo || conn.Provider != model.ProviderManualDemo ||
				conn.Name != "demo: "+tt.scenario || conn.Status != model.ConnectorActive {
				t.Errorf("connector = %+v, want channel=demo provider=manual_demo name=%q status=active",
					conn, "demo: "+tt.scenario)
			}

			byTitle := conversationsByTitle(t, s)
			if len(byTitle) != tt.conversations {
				t.Fatalf("got %d conversations, want %d", len(byTitle), tt.conversations)
			}

			dedupeRe := regexp.MustCompile(`^manual_demo:` + regexp.QuoteMeta(conn.ID) + `:[a-z0-9-]+:\d+$`)
			for title, wantCount := range tt.perChat {
				conv, ok := byTitle[title]
				if !ok {
					t.Errorf("conversation %q not seeded", title)
					continue
				}
				if conv.IsGroup != tt.groups[title] {
					t.Errorf("%q IsGroup = %v, want %v", title, conv.IsGroup, tt.groups[title])
				}
				n, err := s.CountMessages(conv.ID)
				if err != nil {
					t.Fatalf("count messages for %q: %v", title, err)
				}
				if n != wantCount {
					t.Errorf("%q has %d messages, want %d", title, n, wantCount)
				}

				msgs, err := s.ListMessages(conv.ID, 0)
				if err != nil {
					t.Fatalf("list messages for %q: %v", title, err)
				}
				for _, m := range msgs {
					if m.Direction != model.DirectionInbound && m.Direction != model.DirectionOutbound {
						t.Errorf("%q message %q: invalid direction %q", title, m.Body, m.Direction)
					}
					if !dedupeRe.MatchString(m.DedupeKey) {
						t.Errorf("%q message %q: bad dedupe key %q", title, m.Body, m.DedupeKey)
					}
					if m.BodyFormat != "plain_text" {
						t.Errorf("%q message %q: body format %q, want plain_text", title, m.Body, m.BodyFormat)
					}
					if !m.OccurredAt.Before(started) {
						t.Errorf("%q message %q: occurred at %v, not strictly before seed time %v",
							title, m.Body, m.OccurredAt, started)
					}
					if m.Channel != model.ChannelDemo || m.Provider != model.ProviderManualDemo {
						t.Errorf("%q message %q: channel/provider = %q/%q", title, m.Body, m.Channel, m.Provider)
					}
				}
			}

			// Re-seeding is idempotent: adds 0 and changes nothing.
			again, err := Seed(s, ws, tt.scenario)
			if err != nil {
				t.Fatalf("re-seed: %v", err)
			}
			if again.Conversations != 0 || again.Messages != 0 {
				t.Errorf("re-seed summary = %+v, want {Conversations:0 Messages:0}", again)
			}
			byTitle = conversationsByTitle(t, s)
			if len(byTitle) != tt.conversations {
				t.Errorf("after re-seed: %d conversations, want %d", len(byTitle), tt.conversations)
			}
			for title, wantCount := range tt.perChat {
				n, err := s.CountMessages(byTitle[title].ID)
				if err != nil {
					t.Fatalf("count messages for %q: %v", title, err)
				}
				if n != wantCount {
					t.Errorf("after re-seed: %q has %d messages, want %d", title, n, wantCount)
				}
			}
			conns, err = s.ListConnectors()
			if err != nil {
				t.Fatalf("list connectors: %v", err)
			}
			if len(conns) != 1 {
				t.Errorf("after re-seed: %d connectors, want 1", len(conns))
			}
		})
	}
}

// TestTuitionCenterLeakSetup checks the spec §26 timing/content details the
// classifier and leak-detection demos depend on.
func TestTuitionCenterLeakSetup(t *testing.T) {
	s, ws := newTestStore(t)
	if _, err := Seed(s, ws, "tuition-center"); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	byTitle := conversationsByTitle(t, s)
	now := time.Now()

	// Mrs Tan: last message is an inbound question older than 24h with no
	// outbound reply after it (unanswered-question leak).
	mrsTan, err := s.ListMessages(byTitle["Mrs Tan"].ID, 0)
	if err != nil {
		t.Fatalf("list Mrs Tan messages: %v", err)
	}
	if len(mrsTan) == 0 {
		t.Fatal("Mrs Tan has no messages")
	}
	last := mrsTan[len(mrsTan)-1]
	if last.Direction != model.DirectionInbound {
		t.Errorf("Mrs Tan last message direction = %q, want inbound", last.Direction)
	}
	if !strings.Contains(strings.ToLower(last.Body), "saturday") {
		t.Errorf("Mrs Tan last message %q does not ask about Saturday", last.Body)
	}
	if age := now.Sub(last.OccurredAt); age <= 24*time.Hour {
		t.Errorf("Mrs Tan question age = %v, want > 24h", age)
	}

	// Alex: the landing-page quote ask is older than 48h (stale-quote leak).
	alex, err := s.ListMessages(byTitle["Alex"].ID, 0)
	if err != nil {
		t.Fatalf("list Alex messages: %v", err)
	}
	var quoteAsk *model.Message
	for i := range alex {
		if strings.Contains(alex[i].Body, "quote me for a landing page") {
			quoteAsk = &alex[i]
		}
	}
	if quoteAsk == nil {
		t.Fatal("Alex has no landing-page quote request message")
	}
	if age := now.Sub(quoteAsk.OccurredAt); age <= 48*time.Hour {
		t.Errorf("Alex quote ask age = %v, want > 48h", age)
	}

	// Unknown number: lead with no saved contact name, asks about logo design
	// and rate.
	unknown, err := s.ListMessages(byTitle["Unknown +65 9123 4567"].ID, 0)
	if err != nil {
		t.Fatalf("list Unknown messages: %v", err)
	}
	bodies := make([]string, 0, len(unknown))
	for _, m := range unknown {
		bodies = append(bodies, strings.ToLower(m.Body))
		if m.SenderName != "" {
			t.Errorf("Unknown sender name = %q, want empty (no saved name)", m.SenderName)
		}
		if m.SenderPhone != "+65 9123 4567" {
			t.Errorf("Unknown sender phone = %q, want +65 9123 4567", m.SenderPhone)
		}
	}
	all := strings.Join(bodies, " ")
	if !strings.Contains(all, "logo design") || !strings.Contains(all, "rate") {
		t.Errorf("Unknown messages %q missing logo design / rate ask", all)
	}
}

func TestSeedBothScenariosSameStore(t *testing.T) {
	s, ws := newTestStore(t)
	if _, err := Seed(s, ws, "tuition-center"); err != nil {
		t.Fatalf("seed tuition-center: %v", err)
	}
	sum, err := Seed(s, ws, "design-studio")
	if err != nil {
		t.Fatalf("seed design-studio: %v", err)
	}
	if sum.Conversations != 5 || sum.Messages != 15 {
		t.Errorf("design-studio summary = %+v, want {Conversations:5 Messages:15}", sum)
	}
	convs, err := s.ListConversations(store.ConversationFilter{Channel: model.ChannelDemo})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	if len(convs) != 12 {
		t.Errorf("got %d demo conversations, want 12", len(convs))
	}
	conns, err := s.ListConnectors()
	if err != nil {
		t.Fatalf("list connectors: %v", err)
	}
	if len(conns) != 2 {
		t.Errorf("got %d connectors, want 2 (one per scenario)", len(conns))
	}
}
