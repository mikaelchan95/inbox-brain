package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mikaelchan/inbox-brain/internal/config"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// cmdDoctor checks the local installation and reports one OK/WARN/FAIL line
// per check. Warnings (missing optional pieces) keep exit code 0; only a
// broken config or database exits 1.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: ib doctor")
		return 1
	}
	broken := false
	report := func(level, check, detail string) {
		fmt.Fprintf(stdout, "%-4s %s: %s\n", level, check, detail)
	}

	home := config.Home()
	if fi, err := os.Stat(home); err == nil && fi.IsDir() {
		report("OK", "home directory", home)
	} else {
		report("WARN", "home directory", home+" does not exist (run: ib init)")
	}

	cfg, err := config.Load(home)
	switch {
	case errors.Is(err, config.ErrNotInitialized):
		report("WARN", "config", "not initialized (run: ib init)")
	case err != nil:
		report("FAIL", "config", err.Error())
		broken = true
	default:
		report("OK", "config", fmt.Sprintf("profile %q (%s)", cfg.Profile.BusinessName, cfg.Profile.BusinessType))
	}

	dbPath := config.DBPath(home)
	if _, err := os.Stat(dbPath); err != nil {
		report("WARN", "database", dbPath+" does not exist (run: ib init)")
	} else if st, err := store.Open(dbPath); err != nil {
		report("FAIL", "database", err.Error())
		broken = true
	} else {
		report("OK", "database", dbPath)
		checkStore(st, report)
		st.Close()
	}

	if os.Getenv("TELEGRAM_BOT_TOKEN") != "" {
		report("OK", "telegram", "TELEGRAM_BOT_TOKEN is set")
	} else {
		report("WARN", "telegram", "TELEGRAM_BOT_TOKEN not set (needed for: ib telegram connect)")
	}
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY") != ""
	if anthropicKey {
		report("OK", "anthropic", "ANTHROPIC_API_KEY is set")
	} else {
		report("WARN", "anthropic", "ANTHROPIC_API_KEY not set (offline rules extractor will be used)")
	}

	wacliPath := defaultWacliDB()
	if _, err := os.Stat(wacliPath); err == nil {
		report("OK", "wacli", "wacli.db found at "+wacliPath)
	} else {
		report("WARN", "wacli", "no wacli.db at "+wacliPath+" (optional; run wacli sync first)")
	}

	provider := "rules (offline)"
	if cfg != nil && anthropicKey && cfg.AIProvider == "anthropic" {
		provider = fmt.Sprintf("anthropic (model %s)", cfg.AnthropicModel)
	}
	report("OK", "extraction provider", provider)

	if broken {
		fmt.Fprintln(stderr, "doctor found problems")
		return 1
	}
	return 0
}

// checkStore reports the workspace and data counts of an open store.
func checkStore(st *store.Store, report func(level, check, detail string)) {
	ws, err := st.EnsureDefaultWorkspace()
	if err != nil {
		report("WARN", "workspace", err.Error())
		return
	}
	report("OK", "workspace", ws.ID)

	convs, err := st.ListConversations(store.ConversationFilter{})
	if err != nil {
		report("WARN", "data", err.Error())
		return
	}
	messages := 0
	for _, c := range convs {
		n, err := st.CountMessages(c.ID)
		if err != nil {
			report("WARN", "data", err.Error())
			return
		}
		messages += n
	}
	report("OK", "data", fmt.Sprintf("%d conversation(s), %d message(s)", len(convs), messages))
}
