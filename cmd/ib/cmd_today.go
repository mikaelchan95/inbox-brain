package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mikaelchan/inbox-brain/internal/leaks"
	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// cmdToday shows open actions created in the last 24h (or all open actions if
// none are that recent), grouped by type, plus a one-line leak count.
func cmdToday(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: ib today")
	}
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	actions, err := e.st.ListActions(store.ActionFilter{
		Status:       model.StatusOpen,
		CreatedAfter: time.Now().Add(-24 * time.Hour),
	})
	if err != nil {
		return err
	}
	header := "Today's actions (created in the last 24h):"
	if len(actions) == 0 {
		actions, err = e.st.ListActions(store.ActionFilter{Status: model.StatusOpen})
		if err != nil {
			return err
		}
		header = "No actions created in the last 24h; showing all open actions:"
	}

	if len(actions) == 0 {
		fmt.Fprintln(stdout, "No open actions. Sync or extract first: ib extract --approved-only")
	} else {
		fmt.Fprintln(stdout, header)
		byType := map[string][]model.Action{}
		for _, a := range actions {
			byType[a.Type] = append(byType[a.Type], a)
		}
		for _, typ := range model.ActionTypes {
			group := byType[typ]
			if len(group) == 0 {
				continue
			}
			fmt.Fprintf(stdout, "\n%s (%d):\n", typeHeading(typ), len(group))
			for _, a := range group {
				fmt.Fprintf(stdout, "  %s — %s, %s\n", a.Title, convTitle(e.st, a.ConversationID), age(a.CreatedAt))
				if a.SuggestedReply != "" {
					fmt.Fprintf(stdout, "      reply: %s\n", a.SuggestedReply)
				}
			}
		}
	}

	found, err := leaks.Detect(e.st, time.Now())
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Fprintln(stdout, "\nNo revenue leaks.")
	} else {
		fmt.Fprintf(stdout, "\n%d revenue leak(s) — see them with: ib leaks\n", len(found))
	}
	return nil
}

// typeHeading renders an action type as a heading, e.g. "booking_request" →
// "Booking requests".
func typeHeading(t string) string {
	s := strings.ReplaceAll(t, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:] + "s"
}
