package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mikaelchan/inbox-brain/internal/config"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// privacyNotice is the external-AI warning from spec §25.
const privacyNotice = "Inbox Brain will send selected business-related message text to your\n" +
	"configured AI provider for extraction. Personal and ignored chats are\n" +
	"not sent by default."

// cmdInit creates the home directory, config and database. When stdin is a
// TTY and --yes is absent it interviews the user for the business profile
// (spec §3.2); empty answers keep the defaults.
func cmdInit(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	yes := fs.Bool("yes", false, "accept defaults without prompting")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib init: %w", err)
	}

	home := config.Home()
	cfg, err := config.Init(home)
	if err != nil {
		return err
	}
	if !*yes && stdinIsTTY() {
		if err := promptProfile(cfg, stdout); err != nil {
			return err
		}
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	st, err := store.Open(config.DBPath(home))
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := st.EnsureDefaultWorkspace(); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Inbox Brain initialized.\n")
	fmt.Fprintf(stdout, "  Data directory: %s\n", home)
	fmt.Fprintf(stdout, "  Database:       %s\n", config.DBPath(home))
	fmt.Fprintf(stdout, "  Config:         %s\n", home+string(os.PathSeparator)+"config.json")
	fmt.Fprintf(stdout, "\n%s\n", privacyNotice)
	fmt.Fprintf(stdout, "\nNext: try the demo: ib demo seed --scenario tuition-center\n")
	return nil
}

// stdinIsTTY reports whether stdin is an interactive terminal.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// promptProfile interviews the user for the spec §3.2 business profile
// questions, reading answers from stdin. Empty answers keep the defaults.
func promptProfile(cfg *config.Config, stdout io.Writer) error {
	r := bufio.NewReader(os.Stdin)
	ask := func(question, def string) string {
		if def != "" {
			fmt.Fprintf(stdout, "%s [%s]: ", question, def)
		} else {
			fmt.Fprintf(stdout, "%s: ", question)
		}
		line, err := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			return def
		}
		if line == "" {
			return def
		}
		return line
	}
	askList := func(question string, def []string) []string {
		answer := ask(question+" (comma-separated)", strings.Join(def, ", "))
		if answer == "" {
			return def
		}
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

	p := &cfg.Profile
	p.BusinessName = ask("What is your business called?", p.BusinessName)
	p.BusinessType = ask("What kind of business do you run?", p.BusinessType)
	p.Services = askList("What services do you offer?", p.Services)
	p.BusinessKeywords = askList("What words do customers usually use when asking about your work?", p.BusinessKeywords)
	p.AlwaysIgnoreChats = askList("Which chats should always be ignored?", p.AlwaysIgnoreChats)
	p.AlwaysIncludeChats = askList("Which chats should always be included?", p.AlwaysIncludeChats)
	p.Tone = ask("What tone should suggested replies use?", p.Tone)
	return nil
}
