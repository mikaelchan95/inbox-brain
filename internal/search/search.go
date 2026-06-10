// Package search runs one query across messages, actions and leads
// (spec §19). Personal/ignored chats are excluded from message results unless
// the caller explicitly opts in.
package search

import (
	"fmt"
	"strings"

	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// Results groups search hits by entity type.
type Results struct {
	Messages []store.SearchResult
	Actions  []model.Action
	Leads    []model.Lead
}

// Search runs a case-insensitive substring search for q. Messages come from
// store.SearchMessages (which honors includeIgnored per spec §19/§25);
// actions match on title or summary and leads on summary, filtered in Go.
// An empty q returns empty Results without error.
func Search(s *store.Store, q string, includeIgnored bool) (Results, error) {
	if q == "" {
		return Results{}, nil
	}
	var r Results

	msgs, err := s.SearchMessages(q, includeIgnored, 0)
	if err != nil {
		return Results{}, fmt.Errorf("search messages: %w", err)
	}
	r.Messages = msgs

	actions, err := s.ListActions(store.ActionFilter{})
	if err != nil {
		return Results{}, fmt.Errorf("search actions: %w", err)
	}
	lq := strings.ToLower(q)
	for _, a := range actions {
		if strings.Contains(strings.ToLower(a.Title), lq) ||
			strings.Contains(strings.ToLower(a.Summary), lq) {
			r.Actions = append(r.Actions, a)
		}
	}

	leads, err := s.ListLeads("")
	if err != nil {
		return Results{}, fmt.Errorf("search leads: %w", err)
	}
	for _, l := range leads {
		if strings.Contains(strings.ToLower(l.Summary), lq) {
			r.Leads = append(r.Leads, l)
		}
	}
	return r, nil
}
