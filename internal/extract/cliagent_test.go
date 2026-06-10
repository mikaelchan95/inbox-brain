package extract

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikaelchan/inbox-brain/internal/model"
)

// stubCLI writes an executable shell script to a temp dir and returns its
// path. The script consumes stdin (like a real CLI) and runs the given body.
func stubCLI(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent")
	script := "#!/bin/sh\ncat > /dev/null\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func cliInput() ProviderInput {
	return ProviderInput{
		Profile: model.BusinessProfile{BusinessName: "Alex Design Studio"},
		Messages: []model.Message{
			{SenderName: "Mrs Tan", Body: "Can I book a trial class?", Direction: model.DirectionInbound},
		},
	}
}

func TestCLIProviderExtractsActions(t *testing.T) {
	// Simulates an agent CLI that prints a session header and echoes the
	// prompt (which contains the JSON template) before the real answer.
	answer := `{"actions":[{"type":"booking_request","title":"Booking from Mrs Tan","summary":"Trial class","confidence":88,"urgency":"normal"}]}`
	bin := stubCLI(t, `
echo "Agent CLI v1.0 (session abc)"
echo 'prompt was: {"actions":[{"type":"...","title":"...","summary":"...","suggestedReply":"...","confidence":0,"urgency":"low|normal|high","messageExternalId":"..."}]}'
echo '`+answer+`'`)
	p := &cliProvider{name: "test-cli", bin: bin}

	res, err := p.ExtractActions(context.Background(), cliInput())
	if err != nil {
		t.Fatalf("ExtractActions: %v", err)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("actions = %d, want 1 (echoed template must not win)", len(res.Actions))
	}
	a := res.Actions[0]
	if a.Type != model.ActionBookingRequest || a.Confidence != 88 {
		t.Errorf("action = %+v", a)
	}
}

func TestCLIProviderCommandFailure(t *testing.T) {
	bin := stubCLI(t, `echo "not logged in" >&2; exit 1`)
	p := &cliProvider{name: "test-cli", bin: bin}

	_, err := p.ExtractActions(context.Background(), cliInput())
	if err == nil {
		t.Fatal("expected error from failing CLI")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error %q should surface the CLI's stderr", err)
	}
}

func TestCLIProviderInvalidJSON(t *testing.T) {
	bin := stubCLI(t, `echo "I could not find any business actions, sorry!"`)
	p := &cliProvider{name: "test-cli", bin: bin}

	_, err := p.ExtractActions(context.Background(), cliInput())
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v, want invalid JSON error after retry", err)
	}
}

func TestParseActionsFromCLIOutput(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		wantN   int
		wantErr bool
	}{
		{name: "bare json", text: `{"actions":[{"type":"new_lead"}]}`, wantN: 1},
		{name: "fenced json", text: "```json\n{\"actions\":[{\"type\":\"new_lead\"}]}\n```", wantN: 1},
		{name: "echoed template then answer",
			text:  `template: {"actions":[{"type":"..."}]}` + "\n" + `{"actions":[{"type":"new_lead"},{"type":"follow_up"}]}`,
			wantN: 2},
		{name: "empty actions", text: `{"actions":[]}`, wantN: 0},
		{name: "no json at all", text: "nothing here", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := parseActionsFromCLIOutput(tc.text)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", res)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(res.Actions) != tc.wantN {
				t.Errorf("actions = %d, want %d", len(res.Actions), tc.wantN)
			}
		})
	}
}
