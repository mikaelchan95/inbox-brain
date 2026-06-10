# Inbox Brain — How It Works

## Updated Architecture & Flow

## 1. Core Concept

Inbox Brain turns messy customer conversations into business actions.

For freelancers and small businesses, customer conversations often live inside personal messaging apps, especially WhatsApp. This means business messages are mixed with family, friends, social groups, personal errands, and private conversations.

Inbox Brain should not blindly process every message.

Instead, the system works in two stages:

```text
Stage 1: Classify conversations
Is this chat business, personal, mixed, or unknown?

Stage 2: Extract business actions
If business-related, extract leads, bookings, quotes, complaints, payments, and follow-ups.
```

The product should feel safe, controlled, and useful.

The user should feel:

> "This found business opportunities without invading my personal life."

---

## 2. High-Level Flow

```text
Telegram / WhatsApp via wacli / Demo data
        ↓
Raw message ingestion
        ↓
Message normalization
        ↓
Local message store
        ↓
Business vs personal classification
        ↓
User review / approval
        ↓
AI action extraction only on approved business content
        ↓
Actions, leads, follow-ups, complaints, reply drafts
        ↓
Dashboard + CLI
```

---

## 3. Main User Journey

## 3.1 First-time setup

The user installs Inbox Brain locally.

```bash
ib init
```

The system creates:

* local data directory
* SQLite database
* default workspace
* default business profile
* local configuration file

The user can then run a demo:

```bash
ib demo seed --scenario tuition-center
```

This loads sample messages and shows what the product does before connecting real inboxes.

---

## 3.2 Business profile setup

Before classifying real messages, Inbox Brain asks the user what kind of business they run.

Example questions:

```text
What kind of business do you run?
What services do you offer?
What words do customers usually use when asking about your work?
Which chats should always be ignored?
Which chats should always be included?
What tone should suggested replies use?
```

Example business profile:

```json
{
  "businessName": "Alex Design Studio",
  "businessType": "freelance designer",
  "services": [
    "logo design",
    "brand identity",
    "landing pages",
    "pitch decks"
  ],
  "businessKeywords": [
    "logo",
    "brand",
    "deck",
    "proposal",
    "invoice",
    "deadline",
    "quote",
    "rate"
  ],
  "alwaysIgnoreChats": [
    "Mum",
    "Family Group",
    "Football Group"
  ],
  "alwaysIncludeChats": [
    "Client Leads",
    "Design Referrals"
  ],
  "timezone": "Asia/Singapore",
  "tone": "friendly",
  "replyLanguage": "English"
}
```

This profile helps the system separate business messages from personal messages.

---

## 4. Connector Flow

## 4.1 Telegram flow

Telegram is a first-class v0.1 connector.

Flow:

```text
Telegram Bot API
        ↓
Inbox Brain Telegram connector
        ↓
Normalize Telegram update
        ↓
Store message
        ↓
Classify conversation/message
        ↓
Extract business actions if approved
```

The user configures:

```bash
TELEGRAM_BOT_TOKEN=
```

Then runs:

```bash
ib telegram connect
ib sync telegram --follow
```

Telegram messages are easier to classify because many Telegram bots/groups are already business-specific. Still, the same business/personal/mixed classification system should apply.

---

## 4.2 WhatsApp via wacli flow

WhatsApp support in v0.1 uses `wacli` as an external companion tool.

Inbox Brain should not fork `wacli` initially.

Inbox Brain should not write to `wacli.db`.

Inbox Brain should not read or copy WhatsApp session data.

Flow:

```text
User syncs WhatsApp with wacli
        ↓
Inbox Brain imports from wacli.db in read-only mode
        ↓
Messages are normalized
        ↓
Chats are classified locally first
        ↓
User reviews suggested business chats
        ↓
Only approved business/mixed chats are processed by AI
```

Command:

```bash
ib sync whatsapp-wacli --db ~/.wacli/wacli.db
```

Optional later (webhook mode):

```text
wacli webhook
        ↓
Inbox Brain webhook endpoint
        ↓
Normalize message
        ↓
Classify
        ↓
Extract if allowed
```

Important: WhatsApp via `wacli` should be described as local/self-hosted/experimental. It is useful for validation and personal workflows, not the long-term production commercial connector.

---

## 5. Message Normalization

Every connector must output the same internal message format.

The AI extraction engine should not care whether a message came from Telegram, WhatsApp, or demo data.

Normalized message shape:

```go
type NormalizedMessage struct {
    ID                     string
    Channel                string // telegram, whatsapp, demo
    Provider               string // telegram_bot_api, wacli, manual_demo
    ConnectorID            string

    ConversationExternalID string
    MessageExternalID      string

    SenderExternalID       string
    SenderName             string
    SenderHandle           string
    SenderPhone            string

    Body                   string
    BodyFormat             string // plain_text, markdown, html, unknown

    Direction              string // inbound, outbound, unknown

    OccurredAt             time.Time
    IngestedAt             time.Time

    ReplyToExternalMessageID string

    Media                  []MessageMedia

    RawJSON                []byte
    DedupeKey              string
}
```

Dedupe key:

```text
provider:connector_id:conversation_external_id:message_external_id
```

If no stable message ID exists, generate a hash from:

```text
provider + conversation + sender + timestamp + body
```

---

## 6. Business vs Personal Classification

This is now a core part of the system.

The classifier decides whether a chat should be processed for business actions.

Inbox Brain should support four conversation labels:

```text
business
personal
mixed
unknown
```

And three message labels:

```text
business
personal
ambiguous
```

---

## 6.1 Why classification matters

Many freelancers use personal WhatsApp for business.

Example:

```text
Mum
Family group
Best friends
Football group
Old schoolmates
Clients
Potential leads
Referrals
Suppliers
```

Inbox Brain must avoid processing everything.

Instead, it should help the user separate work from life.

---

## 6.2 Classification layers

Inbox Brain should classify at three levels.

### Level 1 — Contact-level classification

Question:

> Is this contact likely business-related?

Signals:

* unknown number
* saved as client/customer/supplier/contractor
* appears in quote, booking, payment, or project conversations
* manually marked as business by user
* belongs to business-related group
* repeatedly asks about services

Possible labels:

```text
business_contact
personal_contact
mixed_contact
unknown_contact
```

---

### Level 2 — Conversation-level classification

Question:

> Is this chat thread mostly business?

Signals:

* pricing terms
* quote requests
* booking requests
* payment discussion
* invoices
* project deadlines
* service names
* appointment times
* delivery details
* customer complaints
* business group names
* formal tone

Possible labels:

```text
business_conversation
personal_conversation
mixed_conversation
unknown_conversation
```

---

### Level 3 — Message-level classification

Question:

> Is this specific message business-related?

This is important for mixed chats.

Example:

```text
Friend: Dinner Friday?
User: Cannot, busy this week.
Friend: Anyway can you design my company logo?
Friend: What is your rate?
```

The conversation is mixed.

The dinner messages are personal.

The logo/rate messages are business.

Possible labels:

```text
business_message
personal_message
ambiguous_message
```

---

## 7. Classification Modes

Inbox Brain should support three modes.

---

## 7.1 Manual Safe Mode

Default mode.

The user manually chooses which chats to process.

Flow:

```text
Import chat list
        ↓
Show conversations
        ↓
User selects Include / Ignore / Mixed
        ↓
Only selected chats are processed
```

This is safest for privacy.

Example UI:

```text
☑ Mrs Tan — include
☑ Design Referrals — include
☐ Mum — ignore
☐ Family Group — ignore
△ Alex — mixed
```

---

## 7.2 Assisted Detection Mode

Recommended main mode.

Inbox Brain scans chats locally and suggests likely business conversations.

Flow:

```text
Import recent messages locally
        ↓
Run local rule-based classifier
        ↓
Show suggested business chats
        ↓
User approves, ignores, or marks mixed
        ↓
AI extraction runs only on approved business content
```

Example output:

```text
Suggested business chats: 14
Needs review: 9
Likely personal ignored: 182

1. Mrs Tan — 94% business
   Reason: Asked about trial class, pricing, and Saturday availability.
   Action: Include / Ignore / Mixed

2. Alex — 68% mixed
   Reason: Mentions invoice and logo project, but also casual messages.
   Action: Include / Ignore / Mixed

3. Family Group — 22% business
   Reason: Payment terms found, but mostly personal context.
   Action: Include / Ignore / Mixed
```

---

## 7.3 Auto Mode

Advanced mode only.

The system automatically processes chats above a confidence threshold.

Recommended thresholds:

```text
85–100: process automatically if Auto Mode is enabled
65–84: suggest as business, ask user to confirm
40–64: needs review
0–39: likely personal, ignore
```

Auto Mode should not be enabled by default.

---

## 8. Local-First Classification Pipeline

The classifier should run locally before any external AI processing.

This protects private messages.

Flow:

```text
Message imported locally
        ↓
Local rules scan metadata and message snippets
        ↓
Classify conversation as business/personal/mixed/unknown
        ↓
If uncertain, ask user to review
        ↓
Only approved business content goes to AI extraction
```

Personal chats should not be sent to external AI providers.

Unknown chats should not be sent to external AI providers by default.

Mixed chats should only send business-relevant message windows.

---

## 9. Classification Signals

## 9.1 Generic business signals

```text
price
quote
quotation
how much
rate
invoice
payment
deposit
receipt
booking
appointment
available
availability
slot
schedule
reschedule
service
package
delivery
order
refund
contract
proposal
deadline
client
customer
project
```

## 9.2 Freelancer-specific business signals

```text
logo
website
copywriting
design
editing
consultation
photoshoot
coaching
tuition
repair
cleaning
renovation
lesson
session
campaign
proposal
deck
video
content
brand
landing page
social media
ad campaign
```

## 9.3 Service-business signals

```text
trial class
class timing
session
appointment
cleaning slot
repair quote
consultation
booking fee
package price
reschedule
availability
location
address
deposit
balance payment
```

## 9.4 Personal signals

```text
mum
dad
family
bro
sis
dinner
lunch
birthday
holiday
joke
meme
football
party
wedding
baby
private emotional content
relationship language
casual friend slang
```

Personal signals should reduce business confidence but should not permanently exclude the chat. A friend can also become a client.

---

## 10. Classification Score

Each conversation should receive a business confidence score.

Example:

```json
{
  "conversationId": "conv_123",
  "classification": "mixed",
  "businessConfidence": 72,
  "reason": "Conversation contains project, invoice, and deadline terms, but also casual dinner planning.",
  "recommendedAction": "review"
}
```

Recommended thresholds:

```text
0–39: likely personal
40–64: unknown / needs review
65–84: likely business / ask user to confirm
85–100: business / eligible for auto-processing
```

Default behavior:

```text
>= 65: show in Suggested Business Chats
40–64: show in Needs Review
< 40: ignore unless user searches or manually includes
```

---

## 11. User Review Flow

Before extraction, the user reviews suggested business chats.

Dashboard page:

```text
/connectors/whatsapp/review
```

The page should show:

```text
Suggested Business Chats
Needs Review
Ignored as Personal
Mixed Chats
```

Each chat card should show:

* contact/group name
* channel
* confidence score
* classification label
* reason
* recent message snippets
* number of business-like signals found
* action buttons

Actions:

```text
Include
Ignore
Mark Mixed
Always Include
Always Ignore
Review Messages
```

---

## 12. User Overrides

User decisions should override classifier decisions.

Examples:

```text
Always ignore Family Group
Always include Design Referrals
Mark Alex as Mixed
Mark Mrs Tan as Business
```

These overrides should be stored and reused.

User override priority:

```text
user_override > always_include/always_ignore rule > local classifier > AI classifier
```

If the user marks a chat as personal, do not process it unless the user changes it.

---

## 13. Updated Processing Pipeline

Old pipeline:

```text
message imported
        ↓
AI extraction
        ↓
action created
```

New pipeline:

```text
message imported
        ↓
conversation classification
        ↓
user approval if needed
        ↓
message-level classification for mixed chats
        ↓
AI action extraction
        ↓
action created
```

Pseudocode:

```go
func ProcessMessage(ctx context.Context, msg Message) error {
    convClass := ClassificationRepo.GetConversationClassification(ctx, msg.ConversationID)

    if convClass == nil {
        convClass = LocalClassifier.ClassifyConversation(ctx, msg.ConversationID)
        ClassificationRepo.SaveConversationClassification(ctx, convClass)
    }

    if convClass.UserOverride == "personal" {
        return nil
    }

    if convClass.UserOverride == "business" {
        return ExtractionPipeline.ExtractAction(ctx, msg.ID)
    }

    switch convClass.Label {
    case "personal":
        return nil

    case "business":
        if convClass.BusinessConfidence >= 85 || convClass.ReviewedByUser {
            return ExtractionPipeline.ExtractAction(ctx, msg.ID)
        }

        ReviewQueue.Add(ctx, msg.ConversationID)
        return nil

    case "mixed":
        msgClass := LocalClassifier.ClassifyMessage(ctx, msg)

        if msgClass.Label == "business" && msgClass.BusinessConfidence >= 65 {
            return ExtractionPipeline.ExtractAction(ctx, msg.ID)
        }

        return nil

    case "unknown":
        ReviewQueue.Add(ctx, msg.ConversationID)
        return nil
    }

    return nil
}
```

---

## 14. Mixed Chat Handling

Mixed chats are common for freelancers.

Examples:

```text
Friend who becomes client
Old colleague asking for paid work
Family member asking for business help
Casual contact asking for a quote
```

For mixed chats:

* store the conversation as mixed
* classify each message
* only extract business-relevant messages
* avoid summarizing personal content
* suggested replies should only reference business context
* do not expose personal snippets unnecessarily in action cards

Example:

```text
Conversation: Alex
Classification: Mixed

Ignored:
- dinner plans
- jokes
- personal updates

Processed:
- "Can you quote me for a landing page?"
- "When can you finish the first draft?"
- "Send me the invoice."
```

---

## 15. AI Extraction After Classification

Once a chat or message is approved as business-related, Inbox Brain extracts actions.

Possible action types:

```text
new_lead
booking_request
quote_request
follow_up
payment_issue
complaint
urgent
general_task
no_action
```

Extraction flow:

```text
Business-approved message
        ↓
Load recent business-relevant context
        ↓
Run deterministic rule hints
        ↓
Send limited context to AI provider
        ↓
Validate JSON output
        ↓
Apply confidence thresholds
        ↓
Create or update business action
```

Important:

For mixed chats, only include business-relevant messages in the AI context.

Do not include personal dinner plans, family discussion, jokes, emotional/private content, or unrelated messages.

---

## 16. Suggested Replies

Inbox Brain generates reply drafts, not automatic replies.

Flow:

```text
Action created
        ↓
Suggested reply generated
        ↓
User reviews
        ↓
User copies reply
        ↓
User sends manually in Telegram/WhatsApp
        ↓
User marks action done
```

No auto-send in v0.1.

This keeps the system safe and trustworthy.

---

## 17. Daily Usage Flow

After setup, the user's normal daily flow should be:

```text
1. Open Inbox Brain
2. See Today's Actions
3. Review high-priority items
4. Copy suggested replies
5. Reply manually in WhatsApp/Telegram
6. Mark actions done
7. Review revenue leaks
```

Dashboard homepage should show:

```text
Today's Actions
New Leads
Booking Requests
Quote Requests
Complaints
Overdue Follow-ups
Revenue Leaks
Connector Health
```

---

## 18. Revenue Leak Detection

Revenue leak detection should run after classification and extraction.

It should only consider business-approved chats.

Leaks include:

```text
Customer asked a question, no reply after 24h
Quote request open after 48h
Booking request not handled
Complaint open after 12h
Lead open longer than 3 days
Payment issue unresolved
```

Flow:

```text
Business conversations
        ↓
Open actions
        ↓
Timing analysis
        ↓
Detect stale opportunities
        ↓
Show revenue leaks
```

CLI:

```bash
ib leaks
ib leaks --json
```

---

## 19. Search Flow

Search should work across:

* business chats
* mixed chats
* actions
* leads
* message snippets

Personal chats that were marked ignored should not appear in default business search.

However, the user may enable "include ignored chats" locally if needed.

Default search behavior:

```text
Search business-approved data only
```

Advanced local option:

```text
Include personal/ignored chats
```

This should be off by default.

---

## 20. Data Storage Flow

All imported data is stored locally in SQLite.

Core tables:

```text
workspaces
connectors
customers
conversations
messages
conversation_classifications
message_classifications
classification_rules
actions
leads
extraction_runs
sync_cursors
audit_events
settings
```

Classification tables are required because business/personal separation is now a core system behavior.

---

## 21. Classification Data Model

Conversation classification:

```sql
CREATE TABLE conversation_classifications (
  id TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL,
  classification TEXT NOT NULL,
  business_confidence REAL NOT NULL,
  source TEXT NOT NULL,
  reason TEXT,
  reviewed_by_user INTEGER NOT NULL DEFAULT 0,
  user_override TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

Allowed classification values:

```text
business
personal
mixed
unknown
```

Allowed sources:

```text
rules
ai
user_override
```

Message classification:

```sql
CREATE TABLE message_classifications (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL,
  classification TEXT NOT NULL,
  business_confidence REAL NOT NULL,
  reason TEXT,
  source TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
```

Allowed message classification values:

```text
business
personal
ambiguous
```

Classification rules:

```sql
CREATE TABLE classification_rules (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  rule_type TEXT NOT NULL,
  pattern TEXT NOT NULL,
  action TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
```

Example rules:

```text
Always include chats named "Client Leads"
Always ignore chats named "Family Group"
Always include messages containing "quote"
Always include messages containing "invoice"
Always ignore contacts named "Mum"
```

---

## 22. API Flow

Classification endpoints:

```http
GET  /api/classification/conversations
POST /api/classification/conversations/:id/approve
POST /api/classification/conversations/:id/ignore
POST /api/classification/conversations/:id/mark-mixed
POST /api/classification/conversations/:id/always-include
POST /api/classification/conversations/:id/always-ignore
POST /api/classification/messages/:id/override
```

Action endpoints:

```http
GET  /api/actions
POST /api/actions/:id/done
POST /api/actions/:id/dismiss
POST /api/actions/:id/snooze
POST /api/actions/:id/reopen
```

Conversation endpoints:

```http
GET /api/conversations
GET /api/conversations/:id
GET /api/conversations/:id/messages
GET /api/conversations/:id/actions
```

Connector endpoints:

```http
GET  /api/connectors
POST /api/connectors/:id/sync
POST /api/connectors/telegram/webhook
POST /api/connectors/wacli/webhook
```

Search endpoint:

```http
GET /api/search?q=
```

---

## 23. CLI Flow

Core commands:

```bash
ib init
ib demo seed --scenario tuition-center
ib dev
ib sync telegram --once
ib sync telegram --follow
ib sync whatsapp-wacli --db ~/.wacli/wacli.db
ib classify conversations
ib classify review
ib classify approve <conversation-id>
ib classify ignore <conversation-id>
ib classify mixed <conversation-id>
ib extract --approved-only
ib today
ib leaks
ib actions
ib search "quote"
ib doctor
```

Important behavior:

```text
ib extract --approved-only
```

should only process:

* business conversations
* reviewed business conversations
* business messages inside mixed conversations

It should not process:

* personal conversations
* ignored conversations
* unknown unreviewed conversations
* ambiguous messages inside mixed chats

---

## 24. Error Handling Flow

## 24.1 Classification uncertainty

If classifier is unsure:

```text
Do not process automatically.
Send conversation to review queue.
```

## 24.2 AI provider unavailable

If AI fails:

```text
Keep messages stored.
Keep classifications stored.
Mark extraction as failed.
Allow retry.
Do not block future ingestion.
```

## 24.3 wacli database unavailable

If `wacli.db` missing or unreadable:

```text
Show actionable error.
Suggest running wacli sync first.
Do not crash app.
Continue with other connectors.
```

## 24.4 Telegram unavailable

If Telegram API fails:

```text
Retry with backoff.
Mark connector degraded.
Continue running dashboard and other connectors.
```

## 24.5 Personal chat accidentally detected

If user marks a chat personal:

```text
Stop processing future messages.
Hide from business dashboard.
Optionally delete derived actions.
Store user override.
```

---

## 25. Privacy Flow

Default privacy behavior:

```text
1. Import messages locally.
2. Run local classification first.
3. Ask user to review likely business chats.
4. Send only approved business context to AI.
5. Do not send personal chats to AI.
6. Do not auto-send messages.
```

External AI warning:

```text
Inbox Brain will send selected business-related message text to your configured AI provider for extraction. Personal and ignored chats are not sent by default.
```

For mixed chats:

```text
Only business-relevant messages should be included in the AI context.
```

For ignored chats:

```text
Do not extract.
Do not include in default search.
Do not show in dashboard.
```

---

## 26. Updated End-to-End Example

### Scenario

Freelancer imports personal WhatsApp through `wacli`.

The app sees these chats:

```text
Mum
Family Group
Football Group
Mrs Tan
Alex
Design Referrals
Unknown +65 9123 4567
```

### Step 1 — Local classifier

Inbox Brain classifies:

```text
Mum — personal, 5% business
Family Group — personal, 18% business
Football Group — personal, 12% business
Mrs Tan — business, 94% business
Alex — mixed, 68% business
Design Referrals — business, 91% business
Unknown +65 9123 4567 — business, 87% business
```

### Step 2 — User review

User confirms:

```text
Include Mrs Tan
Mark Alex as Mixed
Include Design Referrals
Include Unknown +65 9123 4567
Ignore Mum
Ignore Family Group
Ignore Football Group
```

### Step 3 — Extraction

Inbox Brain processes only:

```text
Mrs Tan
business messages inside Alex
Design Referrals
Unknown +65 9123 4567
```

### Step 4 — Actions created

Dashboard shows:

```text
Today's Actions

1. Booking request from Mrs Tan
   She asked for a Saturday trial class.
   Suggested reply available.

2. Quote request from Alex
   He asked for landing page pricing.
   Suggested reply available.

3. New lead from Unknown +65 9123 4567
   Asked if you are available for logo design.
   Suggested reply available.
```

### Step 5 — User acts

User copies replies into WhatsApp manually and marks actions done.

---

## 27. Product Behavior Summary

Inbox Brain should follow these rules:

```text
Do not read everything as business.
Do not send everything to AI.
Do not auto-reply.
Do not expose personal chats by default.

First classify.
Then ask for review.
Then extract business actions.
Then let the user decide what to send.
```

This is the correct behavior for freelancers using personal WhatsApp.

The product becomes:

> An AI business layer over messy personal and work inboxes.

Not:

> A creepy AI scanner of all private messages.

---

## 28. Final Updated System Flow

```text
1. User connects Telegram or imports WhatsApp via wacli.
2. Inbox Brain stores raw normalized messages locally.
3. Local classifier scans conversations.
4. Conversations are labeled business, personal, mixed, or unknown.
5. User reviews suggested business and mixed chats.
6. User overrides are saved.
7. Business-approved messages enter extraction pipeline.
8. Mixed chats are filtered at message level.
9. AI receives only approved business context.
10. AI returns validated business actions.
11. Dashboard shows today's actions, leads, bookings, quotes, complaints, and revenue leaks.
12. User copies suggested replies manually.
13. User marks actions done, dismissed, or snoozed.
14. System learns from user overrides for future classification.
```
