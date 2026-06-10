package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikaelchan/inbox-brain/internal/config"
	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// setupHome points IB_HOME at a temp dir so no test ever touches the real
// ~/.inbox-brain, and clears ANTHROPIC_API_KEY so the offline rules provider
// is always selected.
func setupHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("IB_HOME", dir)
	t.Setenv("ANTHROPIC_API_KEY", "")
	return dir
}

// runCLI executes one command through the real run() entrypoint.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// mustRun runs a command and fails the test unless it exits 0.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, code := runCLI(t, args...)
	if code != 0 {
		t.Fatalf("ib %s: exit %d\nstderr: %s\nstdout: %s", strings.Join(args, " "), code, errOut, out)
	}
	return out
}

func openTestStore(t *testing.T, home string) *store.Store {
	t.Helper()
	st, err := store.Open(config.DBPath(home))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

// conversationIDs maps conversation titles to their internal ids.
func conversationIDs(t *testing.T, home string) map[string]string {
	t.Helper()
	st := openTestStore(t, home)
	defer st.Close()
	convs, err := st.ListConversations(store.ConversationFilter{})
	if err != nil {
		t.Fatalf("list conversations: %v", err)
	}
	ids := make(map[string]string, len(convs))
	for _, c := range convs {
		ids[c.Title] = c.ID
	}
	return ids
}

// assertSection asserts that name appears inside the review bucket starting
// at header (sections are separated by blank lines).
func assertSection(t *testing.T, out, header, name string) {
	t.Helper()
	hi := strings.Index(out, header)
	if hi < 0 {
		t.Fatalf("review output missing %q section:\n%s", header, out)
	}
	section := out[hi:]
	if end := strings.Index(section, "\n\n"); end >= 0 {
		section = section[:end]
	}
	if !strings.Contains(section, name) {
		t.Errorf("%q not listed under %q:\n%s", name, header, out)
	}
}

// TestEndToEndDemoFlow drives the full v0.1 journey from spec §26 against a
// temp IB_HOME: init → demo seed → classify → review → approve/mixed →
// extract (rules provider) → today → leaks → search → doctor.
func TestEndToEndDemoFlow(t *testing.T) {
	home := setupHome(t)

	// init --yes: creates home, config, DB; prints the privacy notice.
	out := mustRun(t, "init", "--yes")
	if !strings.Contains(out, home) {
		t.Errorf("init output does not mention data directory %s:\n%s", home, out)
	}
	if !strings.Contains(out, "Inbox Brain will send selected business-related message text") {
		t.Errorf("init output missing the external-AI privacy notice:\n%s", out)
	}
	if !strings.Contains(out, "ib demo seed --scenario tuition-center") {
		t.Errorf("init output missing the demo suggestion:\n%s", out)
	}

	// demo seed populates conversations and the review queue.
	out = mustRun(t, "demo", "seed", "--scenario", "tuition-center")
	if !strings.Contains(out, "7 conversation(s)") {
		t.Errorf("demo seed should report 7 conversations:\n%s", out)
	}
	if !strings.Contains(out, "ib classify review") {
		t.Errorf("demo seed output missing next step:\n%s", out)
	}

	// classify conversations re-runs the local classifier.
	out = mustRun(t, "classify", "conversations")
	if !strings.Contains(out, "Classified") {
		t.Errorf("classify conversations output: %s", out)
	}

	// classify review: Mrs Tan suggested, Mum ignored, Alex mixed — and no
	// message snippet text anywhere in the listing.
	review := mustRun(t, "classify", "review")
	assertSection(t, review, "Suggested business chats", "Mrs Tan")
	assertSection(t, review, "Ignored as personal", "Mum")
	assertSection(t, review, "Mixed chats", "Alex")
	for _, snippet := range []string{"Aunty Susan", "grandma", "zi char", "chope", "Primary 5", "Finance is chasing"} {
		if strings.Contains(review, snippet) {
			t.Errorf("classify review leaks message text %q:\n%s", snippet, review)
		}
	}

	ids := conversationIDs(t, home)
	for _, title := range []string{"Mrs Tan", "Alex", "Mum", "Family Group", "Football Group"} {
		if ids[title] == "" {
			t.Fatalf("seeded conversation %q not found; have %v", title, ids)
		}
	}

	// Approve Mrs Tan as business; mark Alex mixed.
	out = mustRun(t, "classify", "approve", ids["Mrs Tan"])
	if !strings.Contains(out, "Mrs Tan") {
		t.Errorf("approve confirmation missing chat name: %s", out)
	}
	out = mustRun(t, "classify", "mixed", ids["Alex"])
	if !strings.Contains(out, "Alex") {
		t.Errorf("mixed confirmation missing chat name: %s", out)
	}

	// extract --approved-only with the offline rules provider.
	out = mustRun(t, "extract", "--approved-only")
	if !strings.Contains(out, providerNote) {
		t.Errorf("extract should print the offline-provider note:\n%s", out)
	}

	// Actions exist ONLY for approved conversations (checked in the store).
	st := openTestStore(t, home)
	actions, err := st.ListActions(store.ActionFilter{})
	st.Close()
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) == 0 {
		t.Fatal("extract --approved-only created no actions")
	}
	approved := map[string]bool{ids["Mrs Tan"]: true, ids["Alex"]: true}
	personal := map[string]string{
		ids["Mum"]: "Mum", ids["Family Group"]: "Family Group", ids["Football Group"]: "Football Group",
	}
	for _, a := range actions {
		if name, bad := personal[a.ConversationID]; bad {
			t.Fatalf("action %s extracted from personal conversation %q — privacy gate broken", a.ID, name)
		}
		if !approved[a.ConversationID] {
			t.Errorf("action %s belongs to unapproved conversation %s", a.ID, a.ConversationID)
		}
	}

	// today: at least one action with an indented suggested reply.
	out = mustRun(t, "today")
	if !strings.Contains(out, "reply:") {
		t.Errorf("today output missing a suggested reply:\n%s", out)
	}

	// leaks: Mrs Tan's 30h-old unanswered question must be reported.
	out = mustRun(t, "leaks")
	if !strings.Contains(out, "unanswered") {
		t.Errorf("leaks should report the unanswered Mrs Tan question:\n%s", out)
	}
	out = mustRun(t, "leaks", "--json")
	var leakList []map[string]any
	if err := json.Unmarshal([]byte(out), &leakList); err != nil {
		t.Fatalf("leaks --json is not valid JSON: %v\n%s", err, out)
	}
	if len(leakList) < 1 {
		t.Errorf("leaks --json should report at least one leak: %s", out)
	}

	// actions --json: valid JSON, only approved conversations.
	out = mustRun(t, "actions", "--json")
	var actionList []model.Action
	if err := json.Unmarshal([]byte(out), &actionList); err != nil {
		t.Fatalf("actions --json is not valid JSON: %v\n%s", err, out)
	}
	if len(actionList) != len(actions) {
		t.Errorf("actions --json listed %d actions, store has %d open", len(actionList), len(actions))
	}

	// search: "trial class" finds Mrs Tan; "dinner" never surfaces ignored
	// (personal) conversations.
	out = mustRun(t, "search", "trial class")
	if !strings.Contains(out, "Mrs Tan") {
		t.Errorf("search 'trial class' should find Mrs Tan:\n%s", out)
	}
	out = mustRun(t, "search", "dinner")
	for _, name := range []string{"Mum", "Family Group", "Football Group"} {
		if strings.Contains(out, name) {
			t.Errorf("search 'dinner' leaked ignored conversation %q:\n%s", name, out)
		}
	}

	// doctor exits 0 on a healthy install.
	if _, errOut, code := runCLI(t, "doctor"); code != 0 {
		t.Errorf("doctor exited %d: %s", code, errOut)
	}
}

func TestUnknownCommand(t *testing.T) {
	setupHome(t)
	_, errOut, code := runCLI(t, "frobnicate")
	if code != 1 {
		t.Errorf("unknown command exited %d, want 1", code)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr should name the unknown command: %s", errOut)
	}
}

func TestNoArgsShowsUsage(t *testing.T) {
	setupHome(t)
	_, errOut, code := runCLI(t)
	if code != 1 {
		t.Errorf("no args exited %d, want 1", code)
	}
	if !strings.Contains(errOut, "Usage: ib") {
		t.Errorf("stderr should show usage: %s", errOut)
	}
}

func TestHelpListsEveryCommand(t *testing.T) {
	setupHome(t)
	out, _, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("help exited %d", code)
	}
	for _, cmd := range []string{
		"init", "demo seed", "telegram connect", "sync telegram", "sync whatsapp-wacli",
		"classify conversations", "classify review", "classify approve", "classify ignore",
		"classify mixed", "extract --approved-only", "today", "actions", "leaks",
		"search", "dev", "doctor",
	} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help output missing %q", cmd)
		}
	}
}

func TestExtractRequiresApprovedOnly(t *testing.T) {
	setupHome(t)
	mustRun(t, "init", "--yes")
	_, errOut, code := runCLI(t, "extract")
	if code != 1 {
		t.Errorf("extract without --approved-only exited %d, want 1", code)
	}
	if !strings.Contains(errOut, "--approved-only") {
		t.Errorf("error should explain the required flag: %s", errOut)
	}
}

func TestCommandsFailBeforeInit(t *testing.T) {
	setupHome(t)
	for _, args := range [][]string{
		{"today"}, {"actions"}, {"leaks"}, {"classify", "review"},
		{"search", "quote"}, {"extract", "--approved-only"}, {"demo", "seed"},
	} {
		_, errOut, code := runCLI(t, args...)
		if code != 1 {
			t.Errorf("ib %s before init exited %d, want 1", strings.Join(args, " "), code)
		}
		if !strings.Contains(errOut, "not initialized") {
			t.Errorf("ib %s before init: stderr should say not initialized, got: %s",
				strings.Join(args, " "), errOut)
		}
	}
}

func TestInitYesIsIdempotent(t *testing.T) {
	home := setupHome(t)
	mustRun(t, "init", "--yes")

	// Customize the config; a second init must keep it.
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Profile.BusinessName = "Marker Tuition"
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}

	mustRun(t, "init", "--yes")
	cfg, err = config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if cfg.Profile.BusinessName != "Marker Tuition" {
		t.Errorf("second init clobbered the config: business name = %q", cfg.Profile.BusinessName)
	}
}

func TestDemoSeedUnknownScenario(t *testing.T) {
	setupHome(t)
	mustRun(t, "init", "--yes")
	_, errOut, code := runCLI(t, "demo", "seed", "--scenario", "no-such-scenario")
	if code != 1 {
		t.Errorf("unknown scenario exited %d, want 1", code)
	}
	if !strings.Contains(errOut, "no-such-scenario") {
		t.Errorf("error should name the bad scenario: %s", errOut)
	}
}

func TestActionsJSONEmpty(t *testing.T) {
	setupHome(t)
	mustRun(t, "init", "--yes")
	out := mustRun(t, "actions", "--json")
	var actionList []model.Action
	if err := json.Unmarshal([]byte(out), &actionList); err != nil {
		t.Fatalf("actions --json is not valid JSON: %v\n%s", err, out)
	}
	if len(actionList) != 0 {
		t.Errorf("fresh install should have no actions: %s", out)
	}
}

func TestClassifyVerdictUnknownConversation(t *testing.T) {
	setupHome(t)
	mustRun(t, "init", "--yes")
	for _, verdict := range []string{"approve", "ignore", "mixed"} {
		_, errOut, code := runCLI(t, "classify", verdict, "conv_does_not_exist")
		if code != 1 {
			t.Errorf("classify %s on missing conversation exited %d, want 1", verdict, code)
		}
		if !strings.Contains(errOut, "not found") {
			t.Errorf("classify %s: error should say not found: %s", verdict, errOut)
		}
	}
}

func TestSearchRequiresQuery(t *testing.T) {
	setupHome(t)
	mustRun(t, "init", "--yes")
	if _, _, code := runCLI(t, "search"); code != 1 {
		t.Errorf("search without a query exited %d, want 1", code)
	}
}

func TestDoctorUninitializedStillExitsZero(t *testing.T) {
	setupHome(t)
	out, _, code := runCLI(t, "doctor")
	if code != 0 {
		t.Errorf("doctor on a fresh machine exited %d, want 0 (warnings only)\n%s", code, out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("doctor should warn about the missing install:\n%s", out)
	}
}

// TestClassifyIgnoreExcludesFromSearch covers spec §24.5/§25: ignoring a chat
// hides it from default search.
func TestClassifyIgnoreExcludesFromSearch(t *testing.T) {
	home := setupHome(t)
	mustRun(t, "init", "--yes")
	mustRun(t, "demo", "seed", "--scenario", "tuition-center")
	ids := conversationIDs(t, home)

	// Approve then ignore Design Referrals; it must vanish from search.
	mustRun(t, "classify", "approve", ids["Design Referrals"])
	out := mustRun(t, "search", "bakery")
	if !strings.Contains(out, "Design Referrals") {
		t.Fatalf("approved chat should be searchable:\n%s", out)
	}
	mustRun(t, "classify", "ignore", ids["Design Referrals"])
	out = mustRun(t, "search", "bakery")
	if strings.Contains(out, "Design Referrals") {
		t.Errorf("ignored chat still appears in search:\n%s", out)
	}
}
