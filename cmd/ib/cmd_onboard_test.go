package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mikaelchan95/inbox-brain/internal/config"
	"github.com/mikaelchan95/inbox-brain/internal/connector/email"
)

// TestOnboardDemoFlow scripts a full wizard run: profile answers, demo data
// as the only "inbox", offline rules as the provider. It must end in a
// working, classified state.
func TestOnboardDemoFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("IB_HOME", home)

	input := strings.Join([]string{
		"The Winery",                // business name
		"wine bar",                  // business type
		"tastings, private events",  // services
		"booking, invoice, tasting", // keywords
		"professional",              // tone
		"4",                         // connect menu: demo data
		"5",                         // connect menu: done
		"4",                         // provider: offline rules
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := runOnboard(strings.NewReader(input), &out, false); err != nil {
		t.Fatalf("runOnboard() error = %v\noutput:\n%s", err, out.String())
	}

	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	if got, want := cfg.Profile.BusinessName, "The Winery"; got != want {
		t.Errorf("BusinessName = %q, want %q", got, want)
	}
	if got, want := len(cfg.Profile.Services), 2; got != want {
		t.Errorf("len(Services) = %d, want %d", got, want)
	}
	if got, want := cfg.Profile.Tone, "professional"; got != want {
		t.Errorf("Tone = %q, want %q", got, want)
	}
	if got, want := cfg.AIProvider, "rules"; got != want {
		t.Errorf("AIProvider = %q, want %q", got, want)
	}

	for _, want := range []string{"Demo loaded", "need review", "You're all set", "ib classify review"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q\noutput:\n%s", want, out.String())
		}
	}
}

// TestOnboardExhaustedInput simulates a user hitting Ctrl-D immediately:
// every question takes its default and the wizard still finishes cleanly.
func TestOnboardExhaustedInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("IB_HOME", home)

	var out bytes.Buffer
	if err := runOnboard(strings.NewReader(""), &out, false); err != nil {
		t.Fatalf("runOnboard() error = %v\noutput:\n%s", err, out.String())
	}
	if _, err := config.Load(home); err != nil {
		t.Fatalf("config.Load() after defaults-only run: %v", err)
	}
	if !strings.Contains(out.String(), "You're all set") {
		t.Errorf("output missing completion message:\n%s", out.String())
	}
}

// TestOnboardEmailSkip backs out of the email step without an address; the
// wizard must continue instead of failing.
func TestOnboardEmailSkip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("IB_HOME", home)

	input := strings.Join([]string{
		"", "", "", "", "", // profile: all defaults
		"1", // connect menu: email
		"",  // email address: empty → skip
		"5", // connect menu: done
		"4", // provider: rules
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := runOnboard(strings.NewReader(input), &out, false); err != nil {
		t.Fatalf("runOnboard() error = %v\noutput:\n%s", err, out.String())
	}
	accounts, err := email.LoadAccounts(home)
	if err != nil {
		t.Fatalf("LoadAccounts() error = %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("len(accounts) = %d, want 0", len(accounts))
	}
	if !strings.Contains(out.String(), "You're all set") {
		t.Errorf("output missing completion message:\n%s", out.String())
	}
}
