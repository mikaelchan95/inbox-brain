package store

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// SearchResult is one search hit: a message plus enough conversation context
// to display it.
type SearchResult struct {
	MessageID        string
	ConversationID   string
	ConversationName string
	Channel          string
	SenderName       string
	Snippet          string
	OccurredAt       time.Time
}

// SearchMessages runs a case-insensitive LIKE search over message bodies and
// conversation titles. When includeIgnored is false (spec §19/§25) it
// excludes messages from conversations whose effective classification is
// personal (user_override = personal, or no override and label personal),
// from unreviewed unknown conversations without an override, and from
// conversations that have no classification row at all. Results are newest
// first; limit <= 0 returns all.
func (s *Store) SearchMessages(q string, includeIgnored bool, limit int) ([]SearchResult, error) {
	pattern := "%" + strings.ToLower(q) + "%"
	query := `SELECT m.id, m.conversation_id, COALESCE(c.title, ''), COALESCE(c.external_id, ''),
	                 m.channel, m.sender_name, m.body, m.occurred_at
	          FROM messages m
	          LEFT JOIN conversations c ON c.id = m.conversation_id`
	if !includeIgnored {
		query += ` JOIN conversation_classifications cc ON cc.conversation_id = m.conversation_id
		           LEFT JOIN message_classifications mc ON mc.message_id = m.id`
	}
	query += ` WHERE (LOWER(m.body) LIKE ? OR LOWER(COALESCE(c.title, '')) LIKE ?)`
	args := []any{pattern, pattern}
	if !includeIgnored {
		// Also keep personal-classified messages inside mixed chats out of
		// default search (spec §14: don't expose personal snippets).
		query += ` AND NOT (COALESCE(cc.user_override, '') = 'personal'
		                    OR (COALESCE(cc.user_override, '') = '' AND cc.classification = 'personal'))
		           AND NOT (cc.classification = 'unknown' AND cc.reviewed_by_user = 0
		                    AND COALESCE(cc.user_override, '') = '')
		           AND COALESCE(mc.classification, '') != 'personal'`
	}
	query += ` ORDER BY m.occurred_at DESC, m.id ASC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		var title, externalID, body string
		var occurred int64
		if err := rows.Scan(&r.MessageID, &r.ConversationID, &title, &externalID,
			&r.Channel, &r.SenderName, &body, &occurred); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.ConversationName = title
		if r.ConversationName == "" {
			r.ConversationName = r.SenderName
		}
		if r.ConversationName == "" {
			r.ConversationName = externalID
		}
		r.Snippet = snippet(body, q)
		r.OccurredAt = fromMillis(occurred)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search messages: %w", err)
	}
	return out, nil
}

// snippetWindow is the approximate snippet length in characters.
const snippetWindow = 120

// snippet returns ~120 characters of body centered on the first
// case-insensitive occurrence of q (the whole body when it is short enough,
// or the leading window when q only matched the conversation title). Matching
// is done rune-by-rune: strings.ToLower can change a string's byte length, so
// byte offsets from the lowered string must not index the original.
func snippet(body, q string) string {
	runes := []rune(body)
	if len(runes) <= snippetWindow {
		return body
	}
	matchStart, matchLen := runeMatch(runes, []rune(q))
	if matchStart < 0 {
		return string(runes[:snippetWindow])
	}
	start := matchStart - (snippetWindow-matchLen)/2
	if start < 0 {
		start = 0
	}
	end := start + snippetWindow
	if end > len(runes) {
		end = len(runes)
		start = end - snippetWindow
		if start < 0 {
			start = 0
		}
	}
	return string(runes[start:end])
}

// runeMatch finds the first case-insensitive occurrence of q in body, both as
// rune slices, returning the rune offset and match length (-1 when absent).
func runeMatch(body, q []rune) (start, length int) {
	if len(q) == 0 || len(q) > len(body) {
		return -1, 0
	}
	lower := func(rs []rune) []rune {
		out := make([]rune, len(rs))
		for i, r := range rs {
			out[i] = unicode.ToLower(r)
		}
		return out
	}
	b, qq := lower(body), lower(q)
	for i := 0; i+len(qq) <= len(b); i++ {
		match := true
		for j := range qq {
			if b[i+j] != qq[j] {
				match = false
				break
			}
		}
		if match {
			return i, len(qq)
		}
	}
	return -1, 0
}
