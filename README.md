<h1 align="center">Inbox Brain</h1>

<p align="center">
  Turn messy Telegram, WhatsApp &amp; email chats into business actions —
  leads, bookings, quotes, complaints, payments, follow-ups —
  <b>without giving an AI access to your personal messages.</b>
</p>

<p align="center">
  <a href="https://go.dev/dl/"><img src="https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white" alt="Go 1.25+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/mikaelchan95/inbox-brain" alt="MIT License"></a>
  <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey" alt="macOS | Linux | Windows">
</p>

---

Inbox Brain is an AI business layer over messy personal and work inboxes — not a creepy AI scanner of all private messages. It works in two stages:

1. **Classify (100% local)** — a rule-based classifier labels every chat `business`, `personal`, `mixed`, or `unknown`. Nothing leaves your machine.
2. **Extract (approved-only)** — only chats **you** approve as business are sent to an AI provider to extract actions and draft replies. Personal chats are never sent.

Everything is stored locally in SQLite under `~/.inbox-brain/`. Single static binary, no CGO, no external database.

## Quick start

```bash
# 1. Install (needs Go 1.25+, see Installation below)
go install github.com/mikaelchan95/inbox-brain/cmd/ib@latest

# 2. Run the guided setup — that's it
ib onboard
```

`ib onboard` walks you through everything in about 2 minutes: your business profile, connecting inboxes (email, Telegram, WhatsApp — or demo data to just try it), picking an AI provider (it detects what you have installed), and a first sync so you land in a working, classified inbox. It's safe to re-run anytime to change settings.

Prefer to drive manually?

```bash
ib init
ib demo seed --scenario tuition-center   # or: design-studio
ib classify conversations                # local classifier, nothing sent anywhere
ib classify review                       # see suggested business chats
ib classify approve <conversation-id>    # approve chats for extraction
ib extract --approved-only               # extract business actions
ib today                                 # today's open actions
ib leaks                                 # revenue leak report
ib dev                                   # local dashboard at http://localhost:4173
```

Without an AI provider configured, extraction falls back to a deterministic rules engine — the whole demo works fully offline.

## Installation

### 1. Install Go 1.25+

| OS | Command |
|---|---|
| **macOS** | `brew install go` |
| **Linux** | `sudo snap install go --classic` (or download from [go.dev/dl](https://go.dev/dl/)) |
| **Windows** | `winget install GoLang.Go` (or the [installer](https://go.dev/dl/)) |

### 2. Install Inbox Brain

```bash
go install github.com/mikaelchan95/inbox-brain/cmd/ib@latest
```

The `ib` binary lands in `$(go env GOPATH)/bin` (defaults: `~/go/bin` on macOS/Linux, `%USERPROFILE%\go\bin` on Windows). If `ib` isn't found, add that directory to your PATH:

```bash
# macOS / Linux (zsh/bash)
export PATH="$PATH:$(go env GOPATH)/bin"
```

```powershell
# Windows (PowerShell)
[Environment]::SetEnvironmentVariable("Path", $env:Path + ";$env:USERPROFILE\go\bin", "User")
```

### Or build from source

```bash
git clone https://github.com/mikaelchan95/inbox-brain.git
cd inbox-brain
go build -o ib ./cmd/ib    # produces ./ib (ib.exe on Windows)
```

### 3. Verify

```bash
ib doctor
```

## 🤖 For AI agents

Installing or evaluating Inbox Brain from Claude Code, Codex, Cursor, or any coding agent? This block is non-interactive end to end:

```bash
git clone https://github.com/mikaelchan95/inbox-brain.git && cd inbox-brain
go build -o ib ./cmd/ib
export IB_HOME=$(mktemp -d)                     # sandbox: keep data out of ~/.inbox-brain
./ib init --yes                                 # --yes skips the interactive profile interview
./ib demo seed --scenario tuition-center
./ib classify conversations
./ib classify review                            # prints conversation ids — approve the business ones
./ib extract --approved-only
./ib actions --json                             # structured output
./ib doctor                                     # success criterion: exit code 0
```

Notes for agents:

- `IB_HOME` overrides the data directory (`~/.inbox-brain` by default) — use a temp dir for throwaway runs.
- `ib actions --json` and `ib leaks --json` emit machine-readable output.
- Product behavior is specified in [`docs/SPEC.md`](docs/SPEC.md); package layout and contracts in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).
- Verify changes with `go build ./... && go vet ./... && go test ./...`.

## Connect your real inboxes

> `ib onboard` sets all of these up interactively — the commands below are the manual equivalents.

**Telegram** (first-class):

```bash
export TELEGRAM_BOT_TOKEN=...
ib telegram connect
ib sync telegram --once     # or --follow
```

**Email via IMAP** (any provider): works with self-hosted domains as well as Gmail, Yahoo, Outlook and iCloud — for those, generate an app password and skip `--host`. Mail is fetched read-only (messages stay unread) and conversations group by correspondent. Accounts are stored in a `0600` file under `~/.inbox-brain`.

```bash
export IMAP_PASSWORD=...
ib email add --user inbox@thewinery.com.sg --host imap.thewinery.com.sg
ib email add --user you@gmail.com          # host inferred for well-known providers
ib sync email --once        # or --follow
```

**WhatsApp via wacli** (local/self-hosted, experimental): syncs by reading an existing [`wacli`](https://github.com/steipete/wacli) database in read-only mode. Inbox Brain never writes to `wacli.db` and never touches WhatsApp session data.

```bash
ib sync whatsapp-wacli --db ~/.wacli/wacli.db
```

**Demo**: built-in scenarios (`tuition-center`, `design-studio`) so you can try the product without connecting anything.

## AI provider

Pick one of three ways to power extraction (set `aiProvider` in `~/.inbox-brain/config.json`):

| `aiProvider` | What you need |
|---|---|
| `claude-cli` | [Claude Code](https://claude.com/claude-code) installed and logged in with your Claude subscription (`claude`, then log in once). No API key. |
| `codex-cli` | [Codex CLI](https://github.com/openai/codex) installed and logged in with your ChatGPT subscription (`codex login`). No API key. |
| `anthropic` | `ANTHROPIC_API_KEY` set in the environment (pay-per-use API). |

With `claude-cli` or `codex-cli`, Inbox Brain pipes the approved transcript to the locally installed CLI, which carries your subscription login — nothing to configure beyond logging in once. Run `ib doctor` to check your setup.

Without any of these, Inbox Brain falls back to a deterministic rules-based extractor so the demo works fully offline.

> Inbox Brain will send selected business-related message text to your configured AI provider for extraction. Personal and ignored chats are not sent by default.

## Command reference

| Command | What it does |
|---|---|
| `ib onboard` | Guided setup: profile, inboxes, AI provider, first sync |
| `ib init [--yes]` | Create the data directory, config and database |
| `ib demo seed --scenario NAME` | Load a demo scenario (`tuition-center`, `design-studio`) |
| `ib doctor` | Check the local installation |
| `ib telegram connect` | Register a Telegram bot (needs `TELEGRAM_BOT_TOKEN`) |
| `ib email add --user ADDR [--host H]` | Register an IMAP mailbox (needs `IMAP_PASSWORD`) |
| `ib email list` | List configured email accounts |
| `ib sync telegram [--once\|--follow]` | Fetch new Telegram messages |
| `ib sync email [--once\|--follow]` | Fetch new email messages |
| `ib sync whatsapp-wacli --db PATH` | Import WhatsApp messages from a `wacli.db` |
| `ib classify conversations` | Run the local business/personal classifier |
| `ib classify review` | List chats waiting for review |
| `ib classify approve\|ignore\|mixed <id>` | Override a chat's classification |
| `ib classify approve --all` | Approve every suggested business chat at once |
| `ib extract --approved-only` | Extract actions from approved chats |
| `ib today` | Today's open actions and leak count |
| `ib actions [--json]` | All open actions, oldest first |
| `ib leaks [--json]` | Revenue leaks |
| `ib search QUERY` | Search messages, actions and leads |
| `ib dev [--port N]` | Run the local dashboard |

## Privacy model

- Import and classification are 100% local.
- Personal, ignored, and unreviewed-unknown chats are never sent to any AI provider — enforced in the extraction pipeline and covered by tests.
- In mixed chats, only business-classified messages are included in AI context.
- Replies are drafts you copy manually; nothing is ever auto-sent.
- Your overrides (include / ignore / mixed) always win over the classifier.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

See [`docs/SPEC.md`](docs/SPEC.md) for product behavior and [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the package layout and contracts.

## License

[MIT](LICENSE)
