# Inbox Brain

An AI business layer over messy personal and work inboxes — not a creepy AI
scanner of all private messages.

Inbox Brain turns customer conversations from Telegram and WhatsApp into
business actions (leads, bookings, quotes, complaints, payments, follow-ups)
while keeping personal chats private. It works in two stages:

1. **Classify** — a local, rule-based classifier labels every chat
   `business`, `personal`, `mixed`, or `unknown`. Nothing leaves your machine.
2. **Extract** — only chats you approve as business are sent (selectively) to
   an AI provider to extract actions and draft replies. Personal chats are
   never sent.

Everything is stored locally in SQLite under `~/.inbox-brain/`.

## Quick start

```bash
go build -o ib ./cmd/ib

./ib init                                  # create local data dir + profile
./ib demo seed --scenario tuition-center   # load sample data
./ib classify conversations                # run the local classifier
./ib classify review                       # see suggested business chats
./ib classify approve <conversation-id>    # approve chats for extraction
./ib extract --approved-only               # extract business actions
./ib today                                 # today's actions
./ib leaks                                 # revenue leak report
./ib dev                                   # local dashboard at :4173
```

## Connectors

**Telegram** (first-class):

```bash
export TELEGRAM_BOT_TOKEN=...
./ib telegram connect
./ib sync telegram --once     # or --follow
```

**WhatsApp via wacli** (local/self-hosted, experimental): syncs by reading an
existing [`wacli`](https://github.com/steipete/wacli) database in read-only
mode. Inbox Brain never writes to `wacli.db` and never touches WhatsApp
session data.

```bash
./ib sync whatsapp-wacli --db ~/.wacli/wacli.db
```

**Demo**: built-in scenarios so you can try the product without connecting
anything.

## AI provider

Set `ANTHROPIC_API_KEY` and `"aiProvider": "anthropic"` in
`~/.inbox-brain/config.json` to use Claude for extraction. Without a key,
Inbox Brain falls back to a deterministic rules-based extractor so the demo
works fully offline.

> Inbox Brain will send selected business-related message text to your
> configured AI provider for extraction. Personal and ignored chats are not
> sent by default.

## Privacy model

- Import and classification are 100% local.
- Personal, ignored, and unreviewed-unknown chats are never sent to any AI
  provider — enforced in the extraction pipeline and covered by tests.
- In mixed chats, only business-classified messages are included in AI context.
- Replies are drafts you copy manually; nothing is ever auto-sent.
- Your overrides (include / ignore / mixed) always win over the classifier.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

See `docs/SPEC.md` for product behavior and `docs/ARCHITECTURE.md` for the
package layout and contracts.
