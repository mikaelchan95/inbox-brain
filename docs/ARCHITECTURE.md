# Inbox Brain — Architecture & Package Contracts

Single Go binary `ib` (CLI + embedded web dashboard). Local-first: all data in
SQLite under `~/.inbox-brain/` (override with `IB_HOME`). Product behavior is
defined in `docs/SPEC.md`; this file defines the code layout and the exact
exported API of each package so packages can be developed independently.

## Layout

```
cmd/ib/                      CLI entrypoint and subcommands, e2e test
internal/model/              domain types + constants (no deps) — DONE
internal/config/             home dir, config.json, business profile — DONE
internal/store/              SQLite open/migrate + repositories
internal/classify/           local rule-based classifier (pure logic)
internal/extract/            extraction pipeline + AI providers
internal/leaks/              revenue leak detection
internal/search/             search across approved data
internal/api/                HTTP JSON API + server-rendered dashboard
internal/connector/telegram/ Telegram Bot API connector
internal/connector/email/    IMAP connector (hosted domains, Gmail, Yahoo, ...)
internal/connector/wacli/    read-only wacli.db importer
internal/connector/demo/     demo scenario seeder
docs/                        SPEC.md (product), ARCHITECTURE.md (this file)
```

## Hard rules for all packages

- Go 1.23, stdlib only, plus the single dependency `modernc.org/sqlite`
  (registered as `database/sql` driver name `"sqlite"`). Do NOT edit
  `go.mod`/`go.sum`; do NOT add dependencies.
- Times are `time.Time` in Go, stored as Unix **milliseconds** (INTEGER) in
  SQLite. Zero time ⇄ 0.
- IDs come from `model.NewID(prefix)`; prefixes: `ws`, `conn`, `cust`, `conv`,
  `msg`, `cls`, `mcls`, `rule`, `act`, `lead`, `run`, `evt`.
- Errors: wrap with `fmt.Errorf("context: %w", err)`. No panics in library code.
- Every package ships `_test.go` table-driven tests. Tests use `t.TempDir()`
  for any DB/home dir; never touch the real `~/.inbox-brain`.
- Privacy invariant (spec §25): nothing from conversations classified
  `personal`, user-overridden personal, or unreviewed `unknown` may ever be
  passed to an extraction Provider. This is enforced in `internal/extract`
  and asserted by tests with a capturing fake provider.

## internal/store

`store.Store` wraps `*sql.DB`. `Open` applies embedded schema migrations
(versioned with `PRAGMA user_version`). All repository methods hang off
`*Store`. WAL mode + busy_timeout on open. Foreign keys ON.

```go
type Store struct{ DB *sql.DB }
func Open(path string) (*Store, error)        // opens + migrates
func (s *Store) Close() error

// workspaces & settings
func (s *Store) EnsureDefaultWorkspace() (model.Workspace, error)
func (s *Store) GetSetting(key string) (string, error) // "" if missing
func (s *Store) SetSetting(key, value string) error

// connectors
func (s *Store) UpsertConnector(c model.Connector) (model.Connector, error) // unique (workspace_id, channel, provider, name)
func (s *Store) ListConnectors() ([]model.Connector, error)
func (s *Store) GetConnector(id string) (*model.Connector, error) // nil, nil when absent
func (s *Store) SetConnectorStatus(id, status, detail string) error

// customers
func (s *Store) UpsertCustomer(c model.Customer) (model.Customer, error) // unique (workspace_id, channel, external_id)
func (s *Store) GetCustomer(id string) (*model.Customer, error)

// conversations
type ConversationFilter struct {
    Channel        string // optional
    Classification string // optional: business|personal|mixed|unknown (joins classifications)
    Limit          int    // 0 = no limit
}
func (s *Store) UpsertConversation(c model.Conversation) (model.Conversation, error) // unique (connector_id, external_id); updates title/last_message_at
func (s *Store) GetConversation(id string) (*model.Conversation, error)
func (s *Store) ListConversations(f ConversationFilter) ([]model.Conversation, error) // newest last_message_at first

// messages
func (s *Store) InsertMessage(m model.Message) (bool, error) // false when dedupe_key already exists (no error)
func (s *Store) ListMessages(conversationID string, limit int) ([]model.Message, error) // chronological; limit 0 = all
func (s *Store) GetMessage(id string) (*model.Message, error)
func (s *Store) CountMessages(conversationID string) (int, error)

// classifications
func (s *Store) SaveConversationClassification(c model.ConversationClassification) error // upsert by conversation_id, preserves created_at
func (s *Store) GetConversationClassification(conversationID string) (*model.ConversationClassification, error)
func (s *Store) ListConversationClassifications() ([]model.ConversationClassification, error)
func (s *Store) SetUserOverride(conversationID, override string) error    // sets user_override + reviewed_by_user=1 + source=user_override
func (s *Store) MarkReviewed(conversationID string) error                  // reviewed_by_user=1, keeps label
func (s *Store) SaveMessageClassification(c model.MessageClassification) error // upsert by message_id
func (s *Store) GetMessageClassification(messageID string) (*model.MessageClassification, error)

// rules
func (s *Store) AddRule(r model.ClassificationRule) (model.ClassificationRule, error)
func (s *Store) ListRules() ([]model.ClassificationRule, error)
func (s *Store) DeleteRule(id string) error

// actions
type ActionFilter struct {
    Status         string // optional: open|done|dismissed|snoozed
    Type           string // optional
    ConversationID string // optional
    CreatedAfter   time.Time // optional (zero = ignore)
    Limit          int
}
func (s *Store) CreateAction(a model.Action) (model.Action, error)
func (s *Store) GetAction(id string) (*model.Action, error)
func (s *Store) UpdateActionStatus(id, status string) error // also clears snoozed_until unless status==snoozed
func (s *Store) SnoozeAction(id string, until time.Time) error
func (s *Store) ListActions(f ActionFilter) ([]model.Action, error) // newest first
func (s *Store) ActionExistsForMessage(messageID string) (bool, error)
func (s *Store) DeleteActionsForConversation(conversationID string) (int, error) // for "mark personal, remove derived actions"

// leads
func (s *Store) UpsertLead(l model.Lead) (model.Lead, error) // unique conversation_id; keeps earliest created_at
func (s *Store) ListLeads(status string) ([]model.Lead, error) // "" = all

// extraction runs
func (s *Store) CreateExtractionRun(r model.ExtractionRun) (model.ExtractionRun, error)
func (s *Store) FinishExtractionRun(r model.ExtractionRun) error // updates status/error/counts/finished_at by id
func (s *Store) ListExtractionRuns(limit int) ([]model.ExtractionRun, error)

// sync cursors
func (s *Store) GetSyncCursor(connectorID string) (string, error) // "" if missing
func (s *Store) SetSyncCursor(connectorID, cursor string) error

// audit
func (s *Store) AddAuditEvent(e model.AuditEvent) error
func (s *Store) ListAuditEvents(limit int) ([]model.AuditEvent, error)

// search
type SearchResult struct {
    MessageID        string
    ConversationID   string
    ConversationName string
    Channel          string
    SenderName       string
    Snippet          string
    OccurredAt       time.Time
}
func (s *Store) SearchMessages(q string, includeIgnored bool, limit int) ([]SearchResult, error)
// LIKE-based, case-insensitive. includeIgnored=false excludes conversations
// whose effective classification is personal (label or user_override) and
// unreviewed unknown ones.
```

Tables (Unix-millis INTEGER timestamps; media/raw stored as TEXT JSON):
`workspaces, connectors, customers, conversations, messages,
conversation_classifications (UNIQUE conversation_id), message_classifications
(UNIQUE message_id), classification_rules, actions, leads (UNIQUE
conversation_id), extraction_runs, sync_cursors (PRIMARY KEY connector_id),
audit_events, settings (key TEXT PRIMARY KEY, value TEXT)`.
The classification tables follow spec §21 exactly. Index
`messages(conversation_id, occurred_at)` and `actions(status, created_at)`.

## internal/classify

Pure local logic, depends only on `internal/model`. No I/O, no DB.

```go
type Classifier struct{ /* profile, rules, keyword sets */ }
func New(profile model.BusinessProfile, rules []model.ClassificationRule) *Classifier

// ScoreConversation scores a whole thread from its messages (0–100) and
// returns a filled classification (ID left empty; caller assigns/persists).
// Order of precedence inside: always_include/always_ignore rules and profile
// AlwaysIgnoreChats/AlwaysIncludeChats matched against conversation title and
// customer name → forced result (confidence 95/5, reason says rule matched);
// otherwise keyword scoring.
func (c *Classifier) ScoreConversation(conv model.Conversation, msgs []model.Message) model.ConversationClassification

// ScoreMessage scores one message for mixed-chat filtering.
func (c *Classifier) ScoreMessage(msg model.Message) model.MessageClassification

// LabelForScore maps a 0–100 score to business/unknown/mixed/personal labels:
// >= ThresholdSuggest(65) → business; ThresholdReview(40)–64 → unknown;
// < 40 → personal. "mixed" is produced by ScoreConversation when BOTH strong
// business and strong personal signals are present.
func LabelForScore(score float64) string

// MessageLabelForScore: >= 65 business, < 40 personal, else ambiguous.
func MessageLabelForScore(score float64) string
```

Scoring approach (keep deterministic and explainable):
- Keyword sets from spec §9.1–9.4 compiled in, lowercased word/phrase matching.
- Profile `BusinessKeywords` and `Services` add to the business set.
- Each distinct business signal adds points (with diminishing returns); each
  personal signal subtracts. Scale to 0–100, clamp.
- `Reason` is human-readable: "Mentions quote, invoice, deadline; also dinner
  plans" — list up to ~5 matched signals.
- Mixed detection: ≥2 distinct business signals AND ≥2 distinct personal
  signals → label mixed (score stays as computed).
- Conversation title matching personal words ("mum", "family", "football")
  subtracts; group names with business words add.

## internal/extract

```go
// ProviderInput is the ONLY data ever sent to an AI provider.
type ProviderInput struct {
    Profile      model.BusinessProfile
    Conversation model.Conversation
    Messages     []model.Message // pre-filtered business-relevant context only
}
type ExtractedAction struct {
    Type           string  `json:"type"`            // must be in model.ActionTypes
    Title          string  `json:"title"`
    Summary        string  `json:"summary"`
    SuggestedReply string  `json:"suggestedReply"`
    Confidence     float64 `json:"confidence"`      // 0–100
    Urgency        string  `json:"urgency"`         // low|normal|high
    MessageExternalID string `json:"messageExternalId,omitempty"` // anchor message if known
}
type ProviderResult struct{ Actions []ExtractedAction }
type Provider interface {
    Name() string
    ExtractActions(ctx context.Context, in ProviderInput) (ProviderResult, error)
}

func NewAnthropicProvider(apiKey, model string) Provider // Anthropic Messages API over net/http, strict JSON out, validated
func NewRulesProvider() Provider                          // deterministic keyword heuristics, works offline

// ValidateResult rejects unknown action types, out-of-range confidence,
// empty titles; drops no_action entries. Returns cleaned result.
func ValidateResult(r ProviderResult) ProviderResult

type Pipeline struct {
    Store      *store.Store
    Classifier *classify.Classifier
    Provider   Provider
    Profile    model.BusinessProfile
    AutoMode   bool
    Out        io.Writer // progress output; never nil after NewPipeline
}
func NewPipeline(s *store.Store, c *classify.Classifier, p Provider, profile model.BusinessProfile, autoMode bool) *Pipeline

// ClassifyAll (re)classifies every conversation that has no user override and
// was not reviewed; persists results; returns count. Used by `ib classify
// conversations` and after sync.
func (p *Pipeline) ClassifyAll(ctx context.Context) (int, error)

type Summary struct {
    ConversationsProcessed int
    ConversationsSkipped   int
    ActionsCreated         int
    Failures               int
}
// ProcessApproved implements spec §13 gating exactly:
//   user_override=personal → skip; user_override=business → extract;
//   label personal → skip; label business → extract only if
//   confidence >= 85 (and AutoMode) or ReviewedByUser; label mixed → extract
//   message-classified business messages (score >= 65) only; label unknown →
//   skip (review queue).
// For mixed chats the ProviderInput.Messages contains ONLY business-labeled
// messages. Skips messages that already have actions
// (ActionExistsForMessage). Creates a Lead for each new_lead action. Records
// an ExtractionRun per conversation and an audit event describing how many
// messages were sent to which provider.
func (p *Pipeline) ProcessApproved(ctx context.Context) (Summary, error)

// ProcessConversation runs extraction for one approved conversation id
// (same gating; returns error if conversation is not eligible).
func (p *Pipeline) ProcessConversation(ctx context.Context, conversationID string) (int, error)
```

Anthropic call: POST https://api.anthropic.com/v1/messages, headers
`x-api-key`, `anthropic-version: 2023-06-01`. System prompt built from the
profile (business type/services/tone/reply language), user content is the
message transcript; instruct model to return ONLY a JSON object
`{"actions":[...]}`. Strip markdown fences defensively; on invalid JSON retry
once, then fail the run (spec §24.2 — failures recorded, ingestion not blocked).

## internal/leaks

```go
type Leak struct {
    Kind           string    // unanswered_question, stale_quote, stale_booking, stale_complaint, stale_lead, payment_unresolved
    Severity       string    // low|medium|high
    ConversationID string
    ConversationName string
    ActionID       string    // optional
    Description    string
    Since          time.Time
}
func Detect(s *store.Store, now time.Time) ([]Leak, error)
```
Rules (spec §18), business-approved conversations only: open quote_request >
48h; open booking_request > 24h; open complaint > 12h; open lead > 72h; open
payment_issue > 24h; last inbound business message with no later outbound reply
for > 24h → unanswered_question.

## internal/search

```go
type Results struct {
    Messages []store.SearchResult
    Actions  []model.Action
    Leads    []model.Lead
}
func Search(s *store.Store, q string, includeIgnored bool) (Results, error)
```

## internal/connector/telegram

```go
type Connector struct{ Token string; Store *store.Store; Workspace model.Workspace; ConnectorRow model.Connector; BaseURL string /* override in tests */ }
func Connect(s *store.Store, token string) (*Connector, error) // calls getMe, upserts connector row
func (c *Connector) SyncOnce(ctx context.Context) (int, error) // getUpdates from stored cursor, normalize+store, advance cursor
func (c *Connector) Follow(ctx context.Context) error          // long-poll loop, backoff on failure, marks connector degraded after repeated errors
func NormalizeUpdate(raw json.RawMessage, connectorID, workspaceID string) (*Normalized, error)
type Normalized struct{ Conversation model.Conversation; Customer model.Customer; Message model.Message }
```
Dedupe key: `telegram_bot_api:<connector_id>:<chat_id>:<message_id>`.

## internal/connector/email

```go
type Account struct{ Address, Host string; Port int; Password, Folder string; InitialDays int } // JSON in email_accounts.json (0600)
func LoadAccounts(home string) ([]Account, error)
func SaveAccounts(home string, accounts []Account) error
func DefaultHost(address string) string // IMAP server for well-known domains (gmail, yahoo, ...), "" otherwise
type Connector struct{ Account Account; Store *store.Store; Workspace model.Workspace; ConnectorRow model.Connector /* unexported dial override in tests */ }
func Connect(s *store.Store, account Account) (*Connector, error) // logs in + selects folder, upserts connector row
func (c *Connector) SyncOnce(ctx context.Context) (int, error)    // UID search from stored cursor, fetch with BODY.PEEK (read-only), normalize+store, advance cursor
func (c *Connector) Follow(ctx context.Context) error             // poll loop (60s), backoff on failure, marks connector degraded after repeated errors
func NormalizeEmail(raw []byte, uid imap.UID, uidValidity uint32, internalDate time.Time, account Account, connectorID, workspaceID string) (*Normalized, error)
```
One connector row per account (`channel=email`, `provider=imap`, name=address).
Conversations group by counterpart address (the sender for inbound mail, the
first recipient for mail sent by the account itself). The subject is prefixed
onto the body; text/plain is preferred with stripped text/html as fallback;
attachments become Media entries (metadata only). Cursor:
`<uidvalidity>:<lastuid>` — a UIDVALIDITY change resets it and dedupe keys
absorb the refetch. First sync imports the last InitialDays days (default 30).
Tests run against go-imap's in-memory IMAP server. Dedupe key:
`imap:<connector_id>:<counterpart_address>:<message_id_header>`.

## internal/connector/wacli

```go
type ImportSummary struct{ Conversations, Messages, Skipped int }
func Import(ctx context.Context, s *store.Store, ws model.Workspace, dbPath string) (ImportSummary, error)
```
Opens wacli.db read-only (`file:...?mode=ro&immutable=0`), never writes.
Detects the wacli schema by inspecting sqlite_master for known table shapes
(chats/messages tables); if unrecognized or missing, returns an actionable
error telling the user to run `wacli sync` first (spec §24.3). Tests build a
synthetic wacli.db fixture. Dedupe key:
`wacli:<connector_id>:<chat_jid>:<message_id>`.

## internal/connector/demo

```go
func Seed(s *store.Store, ws model.Workspace, scenario string) (Summary, error)
type Summary struct{ Conversations, Messages int }
func Scenarios() []string // e.g. tuition-center, design-studio
```
Embedded JSON scenario data. `tuition-center` mirrors spec §26: chats Mum,
Family Group, Football Group, Mrs Tan (trial class/pricing/Saturday), Alex
(mixed: dinner + landing-page quote + invoice), Design Referrals, Unknown
+65 9123 4567 (logo design lead). Timestamps relative to seeding time (recent,
some > 24/48h old so leaks demo works). Seeding is idempotent (dedupe keys).

## internal/api

```go
func NewServer(s *store.Store, cfg *config.Config) http.Handler
```
Go 1.22+ `http.ServeMux` patterns. JSON endpoints exactly as spec §22 (use
`{id}` path params). Plus dashboard HTML (html/template + embed.FS):

```
GET /                      → dashboard home: today's actions, counts by type, leaks, connector health
GET /review                → classification review queue (suggested/needs-review/ignored/mixed)
GET /conversations         GET /conversations/{id}
GET /actions               GET /leaks      GET /search?q=
POST form fallbacks for review actions (approve/ignore/mixed) so the dashboard works without JS
```
Design: mobile-first quiet utility. System font stack, white/neutral-gray
surfaces, ONE accent color (indigo-600 #4f46e5), small caps section labels,
single-column layout, large tap targets. No gradients, no serif/decorative
fonts, no external assets (all CSS embedded, no CDN).

## cmd/ib

Stdlib `flag` with a subcommand switch (no cobra). Commands per spec §23:
`onboard, init, demo seed --scenario X, dev, telegram connect, email
add|list, sync telegram --once|--follow, sync email --once|--follow
[--account ADDR], sync whatsapp-wacli --db PATH, classify
conversations|review|approve|ignore|mixed, extract --approved-only, today,
leaks [--json], actions [--json], search QUERY, doctor`.
`ib onboard` is the guided setup wizard (cmd_onboard.go): re-runnable,
prefills from the existing config, falls back to defaults when input is
exhausted (Ctrl-D / scripts), hides passwords via golang.org/x/term on a
TTY, detects claude/codex/ANTHROPIC_API_KEY for the provider step, and ends
with a first sync + classification of whatever was connected.
`ib init` prompts for the business profile interactively when stdin is a TTY
(accept-defaults with --yes). `ib doctor` checks: home dir, DB opens, config
valid, telegram token present, ANTHROPIC_API_KEY present, wacli.db readable.
Provider selection: `ANTHROPIC_API_KEY` set and cfg.AIProvider=="anthropic" →
Anthropic; otherwise rules provider with a printed note.
An end-to-end test runs: init → demo seed → classify → review approvals →
extract (fake/rules provider) → today → leaks, all against a temp IB_HOME.
