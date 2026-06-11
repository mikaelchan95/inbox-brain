package main

import (
	"fmt"
	"io"

	"github.com/mikaelchan95/inbox-brain/internal/search"
)

// cmdSearch searches messages, actions and leads for one query. Ignored
// (personal) chats are excluded unless cfg.SearchIncludeIgnored is enabled
// (spec §19).
func cmdSearch(args []string, stdout io.Writer) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf(`usage: ib search QUERY (e.g. ib search "quote")`)
	}
	q := args[0]

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	res, err := search.Search(e.st, q, e.cfg.SearchIncludeIgnored)
	if err != nil {
		return err
	}
	if len(res.Messages)+len(res.Actions)+len(res.Leads) == 0 {
		fmt.Fprintf(stdout, "No results for %q.\n", q)
		return nil
	}

	fmt.Fprintf(stdout, "Messages (%d):\n", len(res.Messages))
	for _, m := range res.Messages {
		sender := m.SenderName
		if sender == "" {
			sender = "me"
		}
		fmt.Fprintf(stdout, "  [%s] %s: %s (%s, %s)\n",
			m.ConversationName, sender, m.Snippet, m.Channel, age(m.OccurredAt))
	}

	fmt.Fprintf(stdout, "\nActions (%d):\n", len(res.Actions))
	for _, a := range res.Actions {
		fmt.Fprintf(stdout, "  %s  %-15s  %s — %s\n",
			a.ID, a.Type, a.Title, convTitle(e.st, a.ConversationID))
	}

	fmt.Fprintf(stdout, "\nLeads (%d):\n", len(res.Leads))
	for _, l := range res.Leads {
		fmt.Fprintf(stdout, "  %s  %-6s  %s — %s\n",
			l.ID, l.Status, l.Summary, convTitle(e.st, l.ConversationID))
	}
	return nil
}
