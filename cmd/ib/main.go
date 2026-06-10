// Command ib is the Inbox Brain CLI: it classifies chats as business or
// personal and extracts business actions from approved conversations
// (spec §23). All state lives under config.Home() (~/.inbox-brain or $IB_HOME).
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/classify"
	"github.com/mikaelchan/inbox-brain/internal/config"
	"github.com/mikaelchan/inbox-brain/internal/extract"
	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run dispatches one CLI invocation. It is the testable entrypoint: exit code
// 0 on success, 1 on error (errors written to stderr).
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 1
	}
	cmd, rest := args[0], args[1:]

	var err error
	switch cmd {
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	case "init":
		err = cmdInit(rest, stdout)
	case "demo":
		err = cmdDemo(rest, stdout)
	case "classify":
		err = cmdClassify(rest, stdout)
	case "extract":
		err = cmdExtract(rest, stdout)
	case "today":
		err = cmdToday(rest, stdout)
	case "actions":
		err = cmdActions(rest, stdout)
	case "leaks":
		err = cmdLeaks(rest, stdout)
	case "search":
		err = cmdSearch(rest, stdout)
	case "telegram":
		err = cmdTelegram(rest, stdout)
	case "sync":
		err = cmdSync(rest, stdout)
	case "dev":
		err = cmdDev(rest, stdout)
	case "doctor":
		return cmdDoctor(rest, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
		usage(stderr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Inbox Brain — turn messy chats into business actions.

Usage: ib <command> [flags]

Setup
  init [--yes]                        create the data directory, config and database
  demo seed --scenario NAME           load a demo scenario (default: tuition-center)
  doctor                              check the local installation

Connect & sync
  telegram connect                    register a Telegram bot (needs TELEGRAM_BOT_TOKEN)
  sync telegram [--once|--follow]     fetch new Telegram messages
  sync whatsapp-wacli --db PATH       import WhatsApp messages from a wacli.db

Classify & extract
  classify conversations              run the local business/personal classifier
  classify review                     list chats waiting for review
  classify approve <conversation-id>  approve a chat as business
  classify ignore <conversation-id>   ignore a chat as personal
  classify mixed <conversation-id>    mark a chat as mixed
  extract --approved-only             extract actions from approved chats

Use
  today                               today's open actions and leak count
  actions [--json]                    all open actions, oldest first
  leaks [--json]                      revenue leaks
  search QUERY                        search messages, actions and leads
  dev [--port N]                      run the local dashboard

  help                                show this help
`)
}

// env bundles everything an initialized command needs.
type env struct {
	home string
	cfg  *config.Config
	st   *store.Store
	ws   model.Workspace
}

// openEnv loads the config and opens the store. It fails with
// config.ErrNotInitialized (a friendly message) before ib init was run.
func openEnv() (*env, error) {
	home := config.Home()
	cfg, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(config.DBPath(home))
	if err != nil {
		return nil, err
	}
	ws, err := st.EnsureDefaultWorkspace()
	if err != nil {
		st.Close()
		return nil, err
	}
	return &env{home: home, cfg: cfg, st: st, ws: ws}, nil
}

func (e *env) close() {
	if e.st != nil {
		e.st.Close()
	}
}

// newPipeline builds the extraction pipeline with the user's profile and
// stored classification rules. Progress output goes to out.
func (e *env) newPipeline(p extract.Provider, out io.Writer) (*extract.Pipeline, error) {
	rules, err := e.st.ListRules()
	if err != nil {
		return nil, fmt.Errorf("load classification rules: %w", err)
	}
	pl := extract.NewPipeline(e.st, classify.New(e.cfg.Profile, rules), p, e.cfg.Profile, e.cfg.AutoMode)
	if e.cfg.AutoThreshold > 0 {
		pl.AutoThreshold = e.cfg.AutoThreshold
	}
	pl.Out = out
	return pl, nil
}

// providerNote is printed whenever the offline rules extractor is selected.
const providerNote = "using offline rules extractor (set aiProvider to anthropic, claude-cli, or codex-cli to use AI)"

// chooseProvider selects the extraction provider from cfg.AIProvider:
// "anthropic" needs ANTHROPIC_API_KEY; "claude-cli" / "codex-cli" use a
// locally installed agent CLI logged into the user's AI subscription (no
// API key). Anything unavailable falls back to the offline rules provider
// with a printed note.
func chooseProvider(cfg *config.Config, stdout io.Writer) extract.Provider {
	switch cfg.AIProvider {
	case "anthropic":
		if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
			return extract.NewAnthropicProvider(key, cfg.AnthropicModel)
		}
		fmt.Fprintln(stdout, "ANTHROPIC_API_KEY not set — "+providerNote)
	case "claude-cli", "codex-cli":
		bin := extract.CLIProviderBinary(cfg.AIProvider)
		if _, err := exec.LookPath(bin); err == nil {
			if cfg.AIProvider == "claude-cli" {
				return extract.NewClaudeCLIProvider()
			}
			return extract.NewCodexCLIProvider()
		}
		fmt.Fprintf(stdout, "%s not found on PATH — %s\n", bin, providerNote)
	default:
		fmt.Fprintln(stdout, providerNote)
	}
	return extract.NewRulesProvider()
}

// effectiveLabel is the classification after applying any user override
// (spec §12 precedence).
func effectiveLabel(c *model.ConversationClassification) string {
	if c == nil {
		return ""
	}
	if c.UserOverride != "" {
		return c.UserOverride
	}
	return c.Classification
}

// convDisplayName renders a conversation for humans: title, else external id,
// else internal id.
func convDisplayName(c model.Conversation) string {
	if c.Title != "" {
		return c.Title
	}
	if c.ExternalID != "" {
		return c.ExternalID
	}
	return c.ID
}

// convTitle looks a conversation up by id and returns its display name; the
// id itself when the conversation is unknown.
func convTitle(st *store.Store, id string) string {
	c, err := st.GetConversation(id)
	if err != nil || c == nil {
		return id
	}
	return convDisplayName(*c)
}

// age renders how long ago t was, e.g. "5m", "3h", "2d".
func age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
