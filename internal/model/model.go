// Package model defines the core domain types shared by every Inbox Brain
// component. It has no dependencies beyond the standard library.
package model

import "time"

// Channels.
const (
	ChannelTelegram = "telegram"
	ChannelWhatsApp = "whatsapp"
	ChannelDemo     = "demo"
)

// Providers.
const (
	ProviderTelegramBotAPI = "telegram_bot_api"
	ProviderWacli          = "wacli"
	ProviderManualDemo     = "manual_demo"
)

// Conversation classification labels.
const (
	ConvBusiness = "business"
	ConvPersonal = "personal"
	ConvMixed    = "mixed"
	ConvUnknown  = "unknown"
)

// Message classification labels.
const (
	MsgBusiness  = "business"
	MsgPersonal  = "personal"
	MsgAmbiguous = "ambiguous"
)

// Classification sources.
const (
	SourceRules        = "rules"
	SourceAI           = "ai"
	SourceUserOverride = "user_override"
)

// Message directions.
const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"
	DirectionUnknown  = "unknown"
)

// Action types.
const (
	ActionNewLead        = "new_lead"
	ActionBookingRequest = "booking_request"
	ActionQuoteRequest   = "quote_request"
	ActionFollowUp       = "follow_up"
	ActionPaymentIssue   = "payment_issue"
	ActionComplaint      = "complaint"
	ActionUrgent         = "urgent"
	ActionGeneralTask    = "general_task"
	ActionNoAction       = "no_action"
)

// ActionTypes lists every valid action type, used for validation.
var ActionTypes = []string{
	ActionNewLead, ActionBookingRequest, ActionQuoteRequest, ActionFollowUp,
	ActionPaymentIssue, ActionComplaint, ActionUrgent, ActionGeneralTask, ActionNoAction,
}

// Action statuses.
const (
	StatusOpen      = "open"
	StatusDone      = "done"
	StatusDismissed = "dismissed"
	StatusSnoozed   = "snoozed"
)

// Lead statuses.
const (
	LeadOpen = "open"
	LeadWon  = "won"
	LeadLost = "lost"
)

// Connector statuses.
const (
	ConnectorActive   = "active"
	ConnectorDegraded = "degraded"
	ConnectorError    = "error"
)

// Classification rule types and rule actions.
const (
	RuleChatName    = "chat_name"
	RuleContactName = "contact_name"
	RuleKeyword     = "keyword"

	RuleAlwaysInclude = "always_include"
	RuleAlwaysIgnore  = "always_ignore"
)

// Extraction run statuses.
const (
	RunPending = "pending"
	RunSuccess = "success"
	RunFailed  = "failed"
)

// Workspace is the top-level container; v0.1 uses a single default workspace.
type Workspace struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Connector is a configured message source (telegram bot, wacli import, demo).
type Connector struct {
	ID           string
	WorkspaceID  string
	Channel      string // telegram, whatsapp, demo
	Provider     string // telegram_bot_api, wacli, manual_demo
	Name         string
	Status       string // active, degraded, error
	StatusDetail string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Customer is a contact or group participant seen in conversations.
type Customer struct {
	ID          string
	WorkspaceID string
	Channel     string
	ExternalID  string
	Name        string
	Handle      string
	Phone       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Conversation is a chat thread (1:1 or group) on a channel.
type Conversation struct {
	ID            string
	WorkspaceID   string
	ConnectorID   string
	Channel       string
	ExternalID    string
	Title         string
	IsGroup       bool
	LastMessageAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// MessageMedia describes an attachment on a message.
type MessageMedia struct {
	Type     string `json:"type"` // image, video, audio, document, sticker, other
	URL      string `json:"url,omitempty"`
	FileName string `json:"fileName,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Message is the normalized message shape every connector must produce.
type Message struct {
	ID          string
	WorkspaceID string
	// ConversationID is the internal conversations.id this message belongs to.
	ConversationID string
	CustomerID     string

	Channel     string // telegram, whatsapp, demo
	Provider    string // telegram_bot_api, wacli, manual_demo
	ConnectorID string

	ConversationExternalID string
	MessageExternalID      string

	SenderExternalID string
	SenderName       string
	SenderHandle     string
	SenderPhone      string

	Body       string
	BodyFormat string // plain_text, markdown, html, unknown

	Direction string // inbound, outbound, unknown

	OccurredAt time.Time
	IngestedAt time.Time

	ReplyToExternalMessageID string

	Media []MessageMedia

	RawJSON   []byte
	DedupeKey string
}

// ConversationClassification records the business/personal verdict for a chat.
type ConversationClassification struct {
	ID                 string
	ConversationID     string
	Classification     string // business, personal, mixed, unknown
	BusinessConfidence float64
	Source             string // rules, ai, user_override
	Reason             string
	ReviewedByUser     bool
	UserOverride       string // "", business, personal, mixed
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// MessageClassification records the verdict for one message inside a mixed chat.
type MessageClassification struct {
	ID                 string
	MessageID          string
	Classification     string // business, personal, ambiguous
	BusinessConfidence float64
	Reason             string
	Source             string // rules, ai, user_override
	CreatedAt          time.Time
}

// ClassificationRule is a persistent user rule (always include/ignore patterns).
type ClassificationRule struct {
	ID          string
	WorkspaceID string
	RuleType    string // chat_name, contact_name, keyword
	Pattern     string
	Action      string // always_include, always_ignore
	CreatedAt   time.Time
}

// Action is a business to-do extracted from approved conversations.
type Action struct {
	ID             string
	WorkspaceID    string
	ConversationID string
	MessageID      string
	CustomerID     string
	Type           string // new_lead, booking_request, ...
	Title          string
	Summary        string
	SuggestedReply string
	Confidence     float64
	Urgency        string // low, normal, high
	Status         string // open, done, dismissed, snoozed
	SnoozedUntil   time.Time
	Source         string // ai provider name or "rules"
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Lead tracks a potential customer derived from a new_lead action.
type Lead struct {
	ID             string
	WorkspaceID    string
	ConversationID string
	CustomerID     string
	ActionID       string
	Status         string // open, won, lost
	Summary        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ExtractionRun records one attempt to extract actions from a conversation.
type ExtractionRun struct {
	ID             string
	WorkspaceID    string
	ConversationID string
	Provider       string
	Status         string // pending, success, failed
	Error          string
	InputMessages  int
	ActionsCreated int
	StartedAt      time.Time
	FinishedAt     time.Time
}

// AuditEvent records significant system behavior for transparency.
type AuditEvent struct {
	ID          string
	WorkspaceID string
	EventType   string // e.g. classification_saved, user_override, extraction_run, ai_context_sent
	Subject     string // id of the affected entity
	Detail      string
	CreatedAt   time.Time
}

// BusinessProfile describes the user's business; it drives classification
// scoring and the tone of suggested replies.
type BusinessProfile struct {
	BusinessName       string   `json:"businessName"`
	BusinessType       string   `json:"businessType"`
	Services           []string `json:"services"`
	BusinessKeywords   []string `json:"businessKeywords"`
	AlwaysIgnoreChats  []string `json:"alwaysIgnoreChats"`
	AlwaysIncludeChats []string `json:"alwaysIncludeChats"`
	Timezone           string   `json:"timezone"`
	Tone               string   `json:"tone"`
	ReplyLanguage      string   `json:"replyLanguage"`
}

// Classification confidence thresholds (spec §7.3, §10).
const (
	ThresholdAuto    = 85 // >= 85: eligible for auto-processing
	ThresholdSuggest = 65 // 65–84: suggest as business, ask user to confirm
	ThresholdReview  = 40 // 40–64: needs review; < 40: likely personal
)
