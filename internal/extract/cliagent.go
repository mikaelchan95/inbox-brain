package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// cliTimeout bounds one agent-CLI invocation. Agent CLIs are slower than a
// direct API call (they run their own agent loop), so this is generous.
const cliTimeout = 3 * time.Minute

// cliProvider delegates extraction to a locally installed agent CLI that the
// user has logged into with their AI subscription — no API key required.
// The CLI carries its own auth (e.g. Claude Code with a Claude Pro/Max login,
// Codex CLI with a ChatGPT login); inbox-brain only pipes the prompt to stdin
// and parses the JSON answer from stdout.
type cliProvider struct {
	name string   // provider name recorded on runs and audit events
	bin  string   // executable looked up on PATH
	args []string // fixed arguments; the prompt is written to stdin
}

// NewClaudeCLIProvider returns a Provider backed by the Claude Code CLI in
// print mode (`claude -p`), using the user's existing Claude subscription
// login. Run `claude` once to log in.
func NewClaudeCLIProvider() Provider {
	return &cliProvider{name: "claude-cli", bin: "claude", args: []string{"-p"}}
}

// NewCodexCLIProvider returns a Provider backed by the OpenAI Codex CLI
// (`codex exec`), using the user's existing ChatGPT subscription login.
// Run `codex login` once to log in.
func NewCodexCLIProvider() Provider {
	return &cliProvider{name: "codex-cli", bin: "codex", args: []string{"exec", "--skip-git-repo-check", "-"}}
}

// CLIProviderBinary returns the executable an aiProvider value depends on,
// or "" when the provider is not CLI-backed. Used by doctor and provider
// selection to check the binary is installed.
func CLIProviderBinary(aiProvider string) string {
	switch aiProvider {
	case "claude-cli":
		return "claude"
	case "codex-cli":
		return "codex"
	}
	return ""
}

func (c *cliProvider) Name() string { return c.name }

func (c *cliProvider) ExtractActions(ctx context.Context, in ProviderInput) (ProviderResult, error) {
	// Agent CLIs take a single prompt, so the system prompt and transcript
	// are concatenated. The transcript is the same pre-filtered context the
	// API provider would receive — the privacy gate runs before this point.
	prompt := anthropicSystemPrompt(in.Profile) + "\nTranscript:\n" + buildTranscript(in.Messages)

	out, err := c.run(ctx, prompt)
	if err != nil {
		return ProviderResult{}, err
	}
	res, perr := parseActionsFromCLIOutput(out)
	if perr != nil {
		// Invalid JSON: retry once, then fail the run (spec §24.2).
		out, err = c.run(ctx, prompt)
		if err != nil {
			return ProviderResult{}, err
		}
		res, perr = parseActionsFromCLIOutput(out)
		if perr != nil {
			return ProviderResult{}, fmt.Errorf("%s returned invalid JSON: %w", c.name, perr)
		}
	}
	return ValidateResult(res), nil
}

// run executes one CLI invocation with the prompt on stdin and returns stdout.
func (c *cliProvider) run(ctx context.Context, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin, c.args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := truncate(stderr.String(), 300)
		if detail == "" {
			detail = truncate(stdout.String(), 300)
		}
		return "", fmt.Errorf("%s (%s): %w: %s — is the CLI installed and logged in?", c.name, c.bin, err, detail)
	}
	return stdout.String(), nil
}

// parseActionsFromCLIOutput finds the model's {"actions": ...} answer in CLI
// output. Some agent CLIs echo the prompt (which itself contains the JSON
// template) and print session headers, so candidates are scanned from the
// END of the output backwards — the answer always comes last.
func parseActionsFromCLIOutput(text string) (ProviderResult, error) {
	for idx := strings.LastIndex(text, `"actions"`); idx >= 0; idx = strings.LastIndex(text[:idx], `"actions"`) {
		start := strings.LastIndex(text[:idx], "{")
		if start < 0 {
			continue
		}
		var res ProviderResult
		if err := json.NewDecoder(strings.NewReader(text[start:])).Decode(&res); err == nil {
			return res, nil
		}
	}
	return parseActionsJSON(text)
}
