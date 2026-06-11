package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/leaks"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// cmdActions lists all open actions, oldest first, as a human table or a JSON
// array with --json.
func cmdActions(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("actions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "output a JSON array")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib actions: %w", err)
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	actions, err := e.st.ListActions(store.ActionFilter{Status: model.StatusOpen})
	if err != nil {
		return err
	}
	// ListActions returns newest first; show oldest first.
	for i, j := 0, len(actions)-1; i < j; i, j = i+1, j-1 {
		actions[i], actions[j] = actions[j], actions[i]
	}

	if *asJSON {
		return printJSON(stdout, actions)
	}
	if len(actions) == 0 {
		fmt.Fprintln(stdout, "No open actions.")
		return nil
	}
	fmt.Fprintf(stdout, "%d open action(s), oldest first:\n", len(actions))
	for _, a := range actions {
		fmt.Fprintf(stdout, "  %s  %-15s  %s — %s, %s\n",
			a.ID, a.Type, a.Title, convTitle(e.st, a.ConversationID), age(a.CreatedAt))
	}
	return nil
}

// cmdLeaks lists detected revenue leaks as human lines or a JSON array.
func cmdLeaks(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("leaks", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "output a JSON array")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("ib leaks: %w", err)
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	found, err := leaks.Detect(e.st, time.Now())
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(stdout, found)
	}
	if len(found) == 0 {
		fmt.Fprintln(stdout, "No revenue leaks found.")
		return nil
	}
	fmt.Fprintf(stdout, "%d revenue leak(s):\n", len(found))
	for _, l := range found {
		fmt.Fprintf(stdout, "  [%-6s] %s (%s)\n", l.Severity, l.Description, l.Kind)
	}
	return nil
}

// printJSON writes v as indented JSON; nil slices become [].
func printJSON[T any](w io.Writer, items []T) error {
	if items == nil {
		items = []T{}
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}
