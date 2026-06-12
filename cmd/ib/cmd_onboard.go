package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/mikaelchan95/inbox-brain/internal/config"
	"github.com/mikaelchan95/inbox-brain/internal/connector/demo"
	"github.com/mikaelchan95/inbox-brain/internal/connector/email"
	"github.com/mikaelchan95/inbox-brain/internal/connector/telegram"
	"github.com/mikaelchan95/inbox-brain/internal/connector/wacli"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// cmdOnboard handles "ib onboard": a guided, re-runnable setup wizard that
// takes a new user from nothing to a synced, classified inbox.
func cmdOnboard(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ib onboard")
	}
	return runOnboard(os.Stdin, stdout, stdinIsTTY())
}

// runOnboard is the testable wizard entrypoint. When the input runs out
// (Ctrl-D or a scripted stdin), every remaining question takes its default
// so the wizard always finishes in a working state.
func runOnboard(in io.Reader, out io.Writer, isTTY bool) error {
	w := &wizard{in: bufio.NewReader(in), out: out, isTTY: isTTY}

	w.say("")
	w.say(w.bold("👋 Welcome to Inbox Brain!"))
	w.say("")
	w.say("Inbox Brain turns messy Telegram, WhatsApp and email chats into")
	w.say("business actions — leads, bookings, quotes, payments, follow-ups.")
	w.say("")
	w.say(w.dim("Everything stays on this machine. Setup takes about 2 minutes,"))
	w.say(w.dim("Enter accepts the [default], and you can re-run ib onboard anytime."))
	w.say("")

	home := config.Home()
	cfg, err := config.Init(home)
	if err != nil {
		return err
	}
	st, err := store.Open(config.DBPath(home))
	if err != nil {
		return err
	}
	defer st.Close()
	ws, err := st.EnsureDefaultWorkspace()
	if err != nil {
		return err
	}
	e := &env{home: home, cfg: cfg, st: st, ws: ws}
	w.ok("Workspace ready in " + home)

	if err := w.stepProfile(cfg); err != nil {
		return err
	}
	emailConns, tgConn, seeded := w.stepChannels(e)
	if err := w.stepProvider(cfg); err != nil {
		return err
	}
	return w.stepFirstSync(e, emailConns, tgConn, seeded)
}

// --- step 1: business profile ----------------------------------------------

func (w *wizard) stepProfile(cfg *config.Config) error {
	w.title("Step 1 of 4 — Your business")
	w.say(w.dim("This teaches the classifier what \"business\" looks like for you."))
	w.say("")
	p := &cfg.Profile
	p.BusinessName = w.ask("What's your business called?", p.BusinessName)
	p.BusinessType = w.ask("What kind of business is it? (e.g. wine bar, tuition center)", p.BusinessType)
	p.Services = w.askList("What do you sell or offer?", p.Services)
	p.BusinessKeywords = w.askList("Words customers use when they want something (e.g. book, invoice, quote)", p.BusinessKeywords)
	p.Tone = w.ask("Tone for suggested replies (friendly, professional, casual)", p.Tone)
	if err := cfg.Save(); err != nil {
		return err
	}
	w.ok("Profile saved")
	return nil
}

// --- step 2: connect inboxes -------------------------------------------------

// stepChannels loops a connect menu until the user is done. It returns the
// connectors set up during this run so step 4 can sync them immediately.
func (w *wizard) stepChannels(e *env) (emailConns []*email.Connector, tgConn *telegram.Connector, seeded bool) {
	w.title("Step 2 of 4 — Connect your inboxes")
	if conns, err := e.st.ListConnectors(); err == nil && len(conns) > 0 {
		var have []string
		for _, c := range conns {
			have = append(have, c.Channel+" ("+c.Name+")")
		}
		w.say(w.dim("Already connected: " + strings.Join(have, ", ")))
	}
	for {
		w.say("")
		choice := w.choose("What would you like to connect?", []string{
			"Email — any IMAP mailbox: Gmail, Yahoo, or your own domain",
			"Telegram — a business bot via @BotFather",
			"WhatsApp — import an existing wacli database",
			"Demo data — try Inbox Brain without connecting anything",
			"Done connecting",
		}, 4)
		switch choice {
		case 0:
			if c := w.setupEmail(e); c != nil {
				emailConns = append(emailConns, c)
			}
		case 1:
			if c := w.setupTelegram(e); c != nil {
				tgConn = c
			}
		case 2:
			w.setupWacli(e)
		case 3:
			if w.setupDemo(e) {
				seeded = true
			}
		default:
			return emailConns, tgConn, seeded
		}
		if w.eof {
			return emailConns, tgConn, seeded
		}
	}
}

// setupEmail connects one IMAP account, retrying on bad credentials.
func (w *wizard) setupEmail(e *env) *email.Connector {
	w.say("")
	w.say(w.bold("📧 Email"))
	w.say(w.dim("Gmail/Yahoo/iCloud need an app password (search \"app password\" in"))
	w.say(w.dim("your account security settings). Your own domain uses its normal password."))
	for {
		addr := strings.ToLower(w.ask("Email address", ""))
		if addr == "" || !strings.Contains(addr, "@") {
			if addr != "" {
				w.warn("That doesn't look like an email address.")
			}
			return nil
		}
		host := email.DefaultHost(addr)
		if host != "" {
			w.ok("Recognized provider — IMAP server is " + host)
		}
		host = w.ask("IMAP server", host)
		if host == "" {
			w.warn("An IMAP server is needed (often mail.yourdomain.com). Skipping email for now.")
			return nil
		}
		password := w.askSecret("Password")
		if password == "" {
			w.warn("No password given. Skipping email for now.")
			return nil
		}
		account := email.Account{Address: addr, Host: host, Port: 993, Password: password}
		w.say(w.dim("Connecting to " + account.Addr() + "..."))
		c, err := email.Connect(e.st, account)
		if err != nil {
			w.warn(err.Error())
			if !w.askYesNo("Try again?", true) {
				return nil
			}
			continue
		}
		if err := email.UpsertAccount(e.home, account); err != nil {
			w.warn(err.Error())
			return nil
		}
		w.ok("Connected " + addr + " " + w.dim("(saved to "+email.AccountsPath(e.home)+", readable only by you)"))
		return c
	}
}

// setupTelegram walks through creating a bot and validates the token.
func (w *wizard) setupTelegram(e *env) *telegram.Connector {
	w.say("")
	w.say(w.bold("✈️ Telegram"))
	w.say("  1. Open Telegram and message " + w.bold("@BotFather"))
	w.say("  2. Send " + w.bold("/newbot") + " and follow the prompts")
	w.say("  3. Copy the token it gives you (looks like 123456:ABC-DEF...)")
	w.say("")
	token := w.ask("Bot token", "")
	if token == "" {
		w.warn("No token given. Skipping Telegram for now.")
		return nil
	}
	c, err := telegram.Connect(e.st, token)
	if err != nil {
		w.warn(err.Error())
		return nil
	}
	w.ok("Connected Telegram bot @" + c.ConnectorRow.Name)
	w.say(w.dim("Syncs read the token from the environment — add this to your shell profile:"))
	w.say("  " + w.bold("export TELEGRAM_BOT_TOKEN="+token))
	return c
}

// setupWacli imports WhatsApp history from an existing wacli database.
func (w *wizard) setupWacli(e *env) {
	w.say("")
	w.say(w.bold("💬 WhatsApp (via wacli)"))
	w.say(w.dim("Needs github.com/steipete/wacli already synced; the database is read-only."))
	path := w.ask("Path to wacli.db", defaultWacliDB())
	if _, err := os.Stat(path); err != nil {
		w.warn("No database at " + path + " — run wacli sync first, then re-run ib onboard.")
		return
	}
	sum, err := wacli.Import(context.Background(), e.st, e.ws, path)
	if err != nil {
		w.warn(err.Error())
		return
	}
	w.ok(fmt.Sprintf("Imported %d conversation(s), %d message(s)", sum.Conversations, sum.Messages))
}

// setupDemo seeds the default demo scenario.
func (w *wizard) setupDemo(e *env) bool {
	sum, err := demo.Seed(e.st, e.ws, "tuition-center")
	if err != nil {
		w.warn(err.Error())
		return false
	}
	w.ok(fmt.Sprintf("Demo loaded: %d conversation(s), %d message(s)", sum.Conversations, sum.Messages))
	return true
}

// --- step 3: AI provider ------------------------------------------------------

func (w *wizard) stepProvider(cfg *config.Config) error {
	w.title("Step 3 of 4 — AI for extraction")
	w.say(w.dim(privacyNotice))
	w.say("")

	_, hasClaude := lookPathOK("claude")
	_, hasCodex := lookPathOK("codex")
	hasKey := os.Getenv("ANTHROPIC_API_KEY") != ""
	mark := func(found bool, label string) string {
		if found {
			return label + " " + w.green("(detected ✓)")
		}
		return label + " " + w.dim("(not detected)")
	}
	options := []string{
		mark(hasClaude, "Claude Code — uses your Claude subscription, no API key"),
		mark(hasCodex, "Codex CLI — uses your ChatGPT subscription, no API key"),
		mark(hasKey, "Anthropic API — pay-per-use via ANTHROPIC_API_KEY"),
		"Offline rules — no AI, works immediately, less smart",
	}
	def := 3
	switch {
	case hasClaude:
		def = 0
	case hasCodex:
		def = 1
	case hasKey:
		def = 2
	}
	providers := []string{"claude-cli", "codex-cli", "anthropic", "rules"}
	cfg.AIProvider = providers[w.choose("How should actions be extracted?", options, def)]
	if err := cfg.Save(); err != nil {
		return err
	}
	configFile := filepath.Join(cfg.HomeDir(), "config.json")
	w.ok("Extraction provider: " + cfg.AIProvider + " " + w.dim("(change anytime in "+configFile+")"))
	return nil
}

func lookPathOK(bin string) (string, bool) {
	p, err := exec.LookPath(bin)
	return p, err == nil
}

// --- step 4: first sync ---------------------------------------------------------

func (w *wizard) stepFirstSync(e *env, emailConns []*email.Connector, tgConn *telegram.Connector, seeded bool) error {
	w.title("Step 4 of 4 — First sync")
	synced := seeded
	ctx := context.Background()
	for _, c := range emailConns {
		w.say(w.dim("Syncing " + c.Account.Address + " (last 30 days)..."))
		n, err := c.SyncOnce(ctx)
		if err != nil {
			w.warn(err.Error())
			continue
		}
		w.ok(fmt.Sprintf("%s: %d message(s)", c.Account.Address, n))
		synced = true
	}
	if tgConn != nil {
		n, err := tgConn.SyncOnce(ctx)
		if err != nil {
			w.warn(err.Error())
		} else {
			w.ok(fmt.Sprintf("Telegram: %d message(s)", n))
			synced = true
		}
	}
	if synced {
		if err := classifyAfterSync(e, w.out); err != nil {
			return err
		}
	} else {
		w.say(w.dim("Nothing to sync yet — connect an inbox or load demo data when ready."))
	}

	w.say("")
	w.say(w.bold("🎉 You're all set!") + " Where to go next:")
	w.say("")
	w.say("  " + w.bold("ib classify review") + "   approve which chats are business")
	w.say("  " + w.bold("ib today") + "             your open actions at a glance")
	w.say("  " + w.bold("ib dev") + "               local dashboard (http://localhost:" + strconv.Itoa(e.cfg.Port) + ")")
	w.say("  " + w.bold("ib sync email --follow") + "  keep email flowing in")
	w.say("")
	w.say(w.dim("Only chats YOU approve are ever sent to an AI. Re-run ib onboard anytime."))
	return nil
}

// --- interactive plumbing ---------------------------------------------------

// wizard bundles the interactive I/O for ib onboard. After the input is
// exhausted (eof is set) every question silently returns its default.
type wizard struct {
	in    *bufio.Reader
	out   io.Writer
	isTTY bool
	eof   bool
}

func (w *wizard) say(s string) {
	fmt.Fprintln(w.out, s)
}

func (w *wizard) title(s string) {
	w.say("")
	w.say(w.bold(w.cyan(s)))
	w.say(w.cyan(strings.Repeat("─", len([]rune(s)))))
}

func (w *wizard) ok(s string)   { w.say(w.green("✓ ") + s) }
func (w *wizard) warn(s string) { w.say(w.yellow("! ") + s) }

// readLine reads one trimmed input line; "" once the input is exhausted.
func (w *wizard) readLine() string {
	if w.eof {
		return ""
	}
	line, err := w.in.ReadString('\n')
	if err != nil {
		w.eof = true
	}
	return strings.TrimSpace(line)
}

// ask poses a question; Enter (or exhausted input) keeps the default.
func (w *wizard) ask(question, def string) string {
	if def != "" {
		fmt.Fprintf(w.out, "%s [%s]: ", question, def)
	} else {
		fmt.Fprintf(w.out, "%s: ", question)
	}
	if answer := w.readLine(); answer != "" {
		return answer
	}
	return def
}

// askList asks for a comma-separated list; Enter keeps the default.
func (w *wizard) askList(question string, def []string) []string {
	answer := w.ask(question+" (comma-separated)", strings.Join(def, ", "))
	var out []string
	for _, part := range strings.Split(answer, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if out == nil {
		return def
	}
	return out
}

func (w *wizard) askYesNo(question string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	answer := strings.ToLower(w.ask(question+" ("+hint+")", ""))
	if answer == "" {
		return def
	}
	return answer == "y" || answer == "yes"
}

// choose renders a numbered menu and returns the selected index.
func (w *wizard) choose(question string, options []string, def int) int {
	w.say(w.bold(question))
	for i, opt := range options {
		w.say(fmt.Sprintf("  %d. %s", i+1, opt))
	}
	for {
		answer := w.ask("Choose", strconv.Itoa(def+1))
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= len(options) {
			return n - 1
		}
		w.warn(fmt.Sprintf("Please enter a number between 1 and %d.", len(options)))
		if w.eof {
			return def
		}
	}
}

// askSecret reads a password without echoing it when stdin is a terminal;
// otherwise it falls back to a plain line read (tests, pipes).
func (w *wizard) askSecret(question string) string {
	fmt.Fprintf(w.out, "%s: ", question)
	if w.isTTY {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w.out)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return w.readLine()
}

// --- colors -------------------------------------------------------------------

func (w *wizard) color(code, s string) string {
	if !w.isTTY {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (w *wizard) bold(s string) string   { return w.color("1", s) }
func (w *wizard) dim(s string) string    { return w.color("2", s) }
func (w *wizard) green(s string) string  { return w.color("32", s) }
func (w *wizard) yellow(s string) string { return w.color("33", s) }
func (w *wizard) cyan(s string) string   { return w.color("36", s) }
