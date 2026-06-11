// Package classify implements the local rule-based business/personal
// classifier (spec §6–§12). Pure logic: it depends only on internal/model
// and the standard library — no I/O, no database, no network.
package classify

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mikaelchan95/inbox-brain/internal/model"
)

// Scoring constants. Scores start from a neutral base; each distinct matched
// signal adds (business) or subtracts (personal) points with diminishing
// returns, then the result is clamped to 0–100.
const (
	baseScore = 50.0

	// tailWeight is the value of every distinct signal beyond the first
	// len(signalWeights) ones.
	tailWeight = 4.0

	// titleBusinessBonus / titlePersonalPenalty apply per distinct keyword
	// matched in the conversation title (spec: "Family Group", "Mum" subtract
	// extra; group names with business words add).
	titleBusinessBonus   = 10.0
	titlePersonalPenalty = 25.0

	// Forced results from always_include / always_ignore precedence (spec §12).
	forcedBusinessConfidence = 95.0
	forcedPersonalConfidence = 5.0
)

// signalWeights gives the points of the 1st..4th distinct matched signal;
// later signals are worth tailWeight each (diminishing returns).
var signalWeights = []float64{18, 14, 10, 7}

// Classifier scores conversations and messages using embedded keyword sets,
// the user's business profile, and persistent classification rules.
type Classifier struct {
	profile  model.BusinessProfile
	rules    []model.ClassificationRule
	business []string // merged business keywords, lowercased, deduped, sorted
	personal []string // personal keywords, lowercased, sorted
}

// New builds a Classifier. Profile BusinessKeywords and Services are merged
// (lowercased) into the embedded business keyword sets.
func New(profile model.BusinessProfile, rules []model.ClassificationRule) *Classifier {
	set := map[string]bool{}
	add := func(words []string) {
		for _, w := range words {
			w = strings.ToLower(strings.TrimSpace(w))
			if w != "" {
				set[w] = true
			}
		}
	}
	add(genericBusinessSignals)
	add(freelancerBusinessSignals)
	add(serviceBusinessSignals)
	add(profile.BusinessKeywords)
	add(profile.Services)
	business := make([]string, 0, len(set))
	for w := range set {
		business = append(business, w)
	}
	sort.Strings(business)

	personal := make([]string, 0, len(personalSignals))
	for _, w := range personalSignals {
		w = strings.ToLower(strings.TrimSpace(w))
		if w != "" {
			personal = append(personal, w)
		}
	}
	sort.Strings(personal)

	return &Classifier{profile: profile, rules: rules, business: business, personal: personal}
}

// ScoreConversation scores a whole thread from its messages (0–100) and
// returns a filled classification (ID left empty; caller assigns/persists,
// and timestamps are assigned at persistence time).
//
// Precedence (spec §12): always_include rules and profile AlwaysIncludeChats
// force business/95; always_ignore rules and profile AlwaysIgnoreChats force
// personal/5; otherwise keyword scoring. Mixed: ≥2 distinct business signals
// AND ≥2 distinct personal signals across the thread → mixed regardless of
// the score band.
func (c *Classifier) ScoreConversation(conv model.Conversation, msgs []model.Message) model.ConversationClassification {
	cls := model.ConversationClassification{
		ConversationID: conv.ID,
		Source:         model.SourceRules,
	}

	names := candidateNames(conv, msgs)
	if reason, ok := c.forcedMatch(model.RuleAlwaysInclude, c.profile.AlwaysIncludeChats, names); ok {
		cls.Classification = model.ConvBusiness
		cls.BusinessConfidence = forcedBusinessConfidence
		cls.Reason = reason
		return cls
	}
	if reason, ok := c.forcedMatch(model.RuleAlwaysIgnore, c.profile.AlwaysIgnoreChats, names); ok {
		cls.Classification = model.ConvPersonal
		cls.BusinessConfidence = forcedPersonalConfidence
		cls.Reason = reason
		return cls
	}

	bodies := make([]string, 0, len(msgs))
	for _, m := range msgs {
		bodies = append(bodies, m.Body)
	}
	text := strings.Join(bodies, "\n")

	biz := findMatches(text, c.business)
	pers := findMatches(text, c.personal)
	titleBiz := findMatches(conv.Title, c.business)
	titlePers := findMatches(conv.Title, c.personal)

	score := baseScore + signalPoints(len(biz)) - signalPoints(len(pers))
	score += float64(len(titleBiz)) * titleBusinessBonus
	score -= float64(len(titlePers)) * titlePersonalPenalty
	score = clampScore(math.Round(score))

	label := LabelForScore(score)
	if len(biz) >= 2 && len(pers) >= 2 {
		label = model.ConvMixed
	}

	cls.Classification = label
	cls.BusinessConfidence = score
	cls.Reason = buildReason(biz, pers, titleBiz, titlePers)
	return cls
}

// ScoreMessage scores one message for mixed-chat filtering, using the same
// keyword machinery on the single message body. ID left empty; caller
// assigns/persists.
func (c *Classifier) ScoreMessage(msg model.Message) model.MessageClassification {
	biz := findMatches(msg.Body, c.business)
	pers := findMatches(msg.Body, c.personal)
	score := clampScore(math.Round(baseScore + signalPoints(len(biz)) - signalPoints(len(pers))))
	return model.MessageClassification{
		MessageID:          msg.ID,
		Classification:     MessageLabelForScore(score),
		BusinessConfidence: score,
		Reason:             buildReason(biz, pers, nil, nil),
		Source:             model.SourceRules,
	}
}

// LabelForScore maps a 0–100 score to a conversation label:
// >= ThresholdSuggest(65) → business; ThresholdReview(40)–64 → unknown;
// < 40 → personal. ("mixed" is produced by ScoreConversation when both strong
// business and strong personal signals are present.)
func LabelForScore(score float64) string {
	switch {
	case score >= model.ThresholdSuggest:
		return model.ConvBusiness
	case score >= model.ThresholdReview:
		return model.ConvUnknown
	default:
		return model.ConvPersonal
	}
}

// MessageLabelForScore maps a 0–100 score to a message label:
// >= 65 business, < 40 personal, else ambiguous.
func MessageLabelForScore(score float64) string {
	switch {
	case score >= model.ThresholdSuggest:
		return model.MsgBusiness
	case score < model.ThresholdReview:
		return model.MsgPersonal
	default:
		return model.MsgAmbiguous
	}
}

// match records one distinct matched keyword and where it first occurred.
type match struct {
	keyword string
	pos     int
}

// findMatches returns the distinct keywords matched in text on word
// boundaries, ordered by first occurrence (ties broken by keyword) so that
// reasons are deterministic and read naturally.
func findMatches(text string, keywords []string) []match {
	text = strings.ToLower(text)
	var out []match
	for _, kw := range keywords {
		if pos, ok := phraseIndex(text, kw); ok {
			out = append(out, match{keyword: kw, pos: pos})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pos != out[j].pos {
			return out[i].pos < out[j].pos
		}
		return out[i].keyword < out[j].keyword
	})
	return out
}

// phraseIndex finds the first occurrence of phrase in text where both ends
// fall on word boundaries ("rate" must not match inside "celebrate";
// multi-word phrases like "how much" are matched as substrings on word
// boundaries). Both inputs must already be lowercase.
func phraseIndex(text, phrase string) (int, bool) {
	if phrase == "" {
		return 0, false
	}
	start := 0
	for {
		i := strings.Index(text[start:], phrase)
		if i < 0 {
			return 0, false
		}
		i += start
		if boundaryBefore(text, i) && boundaryAfter(text, i+len(phrase)) {
			return i, true
		}
		start = i + 1
	}
}

func boundaryBefore(text string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:i])
	return !isWordChar(r)
}

func boundaryAfter(text string, j int) bool {
	if j >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[j:])
	return !isWordChar(r)
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// signalPoints returns the total points for n distinct signals with
// diminishing returns: the first signals are worth signalWeights, every
// further one tailWeight.
func signalPoints(n int) float64 {
	var pts float64
	for i := 0; i < n; i++ {
		if i < len(signalWeights) {
			pts += signalWeights[i]
		} else {
			pts += tailWeight
		}
	}
	return pts
}

func clampScore(s float64) float64 {
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// candidateNames returns the conversation title plus the distinct sender
// names seen in the thread; always_include/always_ignore patterns are matched
// against these case-insensitively.
func candidateNames(conv model.Conversation, msgs []model.Message) []string {
	names := []string{conv.Title}
	seen := map[string]bool{}
	for _, m := range msgs {
		key := strings.ToLower(strings.TrimSpace(m.SenderName))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, m.SenderName)
	}
	return names
}

// forcedMatch checks the profile chat list and the chat_name/contact_name
// rules with the given action against the candidate names. It returns a
// human-readable reason naming the matched rule.
func (c *Classifier) forcedMatch(action string, profileChats []string, names []string) (string, bool) {
	listName := "always-include"
	if action == model.RuleAlwaysIgnore {
		listName = "always-ignore"
	}
	for _, p := range profileChats {
		for _, n := range names {
			if equalNames(p, n) {
				return fmt.Sprintf("Chat %q is in the profile %s list", p, listName), true
			}
		}
	}
	for _, r := range c.rules {
		if r.Action != action {
			continue
		}
		if r.RuleType != model.RuleChatName && r.RuleType != model.RuleContactName {
			continue
		}
		for _, n := range names {
			if equalNames(r.Pattern, n) {
				kind := "chat name"
				if r.RuleType == model.RuleContactName {
					kind = "contact name"
				}
				return fmt.Sprintf("Matched %s rule (%s %q)", listName, kind, r.Pattern), true
			}
		}
	}
	return "", false
}

func equalNames(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// buildReason renders a human-readable explanation listing up to ~5 matched
// signals, e.g. "Mentions quote, invoice, deadline; also dinner, football".
func buildReason(biz, pers, titleBiz, titlePers []match) string {
	bizWords := matchWords(biz)
	persWords := matchWords(pers)

	var s string
	switch {
	case len(bizWords) > 0 && len(persWords) > 0:
		s = fmt.Sprintf("Mentions %s; also %s", joinCapped(bizWords, 3), joinCapped(persWords, 2))
	case len(bizWords) > 0:
		s = "Mentions " + joinCapped(bizWords, 5)
	case len(persWords) > 0:
		s = "Mostly personal: " + joinCapped(persWords, 5)
	default:
		s = "No business or personal signals found"
	}

	if len(titlePers) > 0 {
		s += "; chat name looks personal (" + joinCapped(matchWords(titlePers), 2) + ")"
	} else if len(titleBiz) > 0 {
		s += "; chat name looks business-related (" + joinCapped(matchWords(titleBiz), 2) + ")"
	}
	return s
}

func matchWords(ms []match) []string {
	words := make([]string, 0, len(ms))
	for _, m := range ms {
		words = append(words, m.keyword)
	}
	return words
}

func joinCapped(words []string, n int) string {
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, ", ")
}
