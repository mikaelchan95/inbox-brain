package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/leaks"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/search"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// actionView pairs an action with its conversation's display name.
type actionView struct {
	Action           model.Action
	ConversationName string
}

func (sv *server) actionViews(actions []model.Action) ([]actionView, error) {
	names := map[string]string{}
	out := make([]actionView, 0, len(actions))
	for _, a := range actions {
		name, ok := names[a.ConversationID]
		if !ok {
			conv, err := sv.store.GetConversation(a.ConversationID)
			if err != nil {
				return nil, err
			}
			name = a.ConversationID
			if conv != nil {
				name = displayName(*conv)
			}
			names[a.ConversationID] = name
		}
		out = append(out, actionView{Action: a, ConversationName: name})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Home (spec §17)
// ---------------------------------------------------------------------------

type actionGroup struct {
	Title   string
	Actions []actionView
}

type homeData struct {
	OpenCount  int
	Groups     []actionGroup
	Leaks      []leaks.Leak
	Connectors []model.Connector
}

// homeGroupOrder fixes the order of "Today's Actions" groups: the five
// headline groups from spec §17 first, then the remaining action types so no
// open action is ever hidden.
var homeGroupOrder = []struct{ actionType, title string }{
	{model.ActionNewLead, "New Leads"},
	{model.ActionBookingRequest, "Booking Requests"},
	{model.ActionQuoteRequest, "Quote Requests"},
	{model.ActionComplaint, "Complaints"},
	{model.ActionFollowUp, "Follow-ups"},
	{model.ActionPaymentIssue, "Payment Issues"},
	{model.ActionUrgent, "Urgent"},
	{model.ActionGeneralTask, "General Tasks"},
}

func (sv *server) pageHome(w http.ResponseWriter, _ *http.Request) {
	open, err := sv.store.ListActions(store.ActionFilter{Status: model.StatusOpen})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := sv.actionViews(open)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byType := map[string][]actionView{}
	for _, v := range views {
		byType[v.Action.Type] = append(byType[v.Action.Type], v)
	}
	var groups []actionGroup
	for _, g := range homeGroupOrder {
		if vs := byType[g.actionType]; len(vs) > 0 {
			groups = append(groups, actionGroup{Title: g.title, Actions: vs})
		}
	}
	lks, err := leaks.Detect(sv.store, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conns, err := sv.store.ListConnectors()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sv.render(w, "home", homeData{OpenCount: len(open), Groups: groups, Leaks: lks, Connectors: conns})
}

// ---------------------------------------------------------------------------
// Review queue (spec §11)
// ---------------------------------------------------------------------------

type reviewCard struct {
	Conversation model.Conversation
	Name         string
	Label        string
	Confidence   float64
	Reason       string
	MessageCount int
	Snippets     []string // never populated for the ignored section (spec §25)
}

type reviewSection struct {
	Title string
	Cards []reviewCard
}

type reviewData struct {
	Sections []reviewSection
}

func (sv *server) pageReview(w http.ResponseWriter, _ *http.Request) {
	items, err := sv.classifiedConversations()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byBucket := map[string][]reviewCard{}
	for _, it := range items {
		if it.Bucket == bucketReviewed {
			continue // already handled by the user; not part of the queue
		}
		card := reviewCard{
			Conversation: it.Conv,
			Name:         displayName(it.Conv),
			Label:        effectiveLabel(it.Cls),
			MessageCount: it.MessageCount,
		}
		if it.Cls != nil {
			card.Confidence = it.Cls.BusinessConfidence
			card.Reason = it.Cls.Reason
		}
		// Privacy (spec §25): never show snippets from chats ignored as personal.
		if it.Bucket != bucketIgnored {
			sn, err := sv.recentSnippets(it.Conv.ID, 3)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			card.Snippets = sn
		}
		byBucket[it.Bucket] = append(byBucket[it.Bucket], card)
	}
	data := reviewData{Sections: []reviewSection{
		{Title: "Suggested Business Chats", Cards: byBucket[bucketSuggested]},
		{Title: "Needs Review", Cards: byBucket[bucketNeedsReview]},
		{Title: "Mixed Chats", Cards: byBucket[bucketMixed]},
		{Title: "Ignored as Personal", Cards: byBucket[bucketIgnored]},
	}}
	sv.render(w, "review", data)
}

// recentSnippets returns the last n messages of a conversation rendered as
// short "Sender: body" lines.
func (sv *server) recentSnippets(conversationID string, n int) ([]string, error) {
	msgs, err := sv.store.ListMessages(conversationID, 0)
	if err != nil {
		return nil, err
	}
	if len(msgs) > n {
		msgs = msgs[len(msgs)-n:]
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		s := m.Body
		if m.Channel == model.ChannelEmail {
			subject, visible, quoted := parseEmailBody(m.Body)
			s = visible
			if s == "" {
				s = subject
			}
			if s == "" {
				s = quoted // quote-only reply with no subject: better than nothing
			}
		}
		if m.SenderName != "" {
			s = m.SenderName + ": " + s
		}
		s = strings.Join(strings.Fields(s), " ") // one line per snippet
		out = append(out, truncate(s, 120))
	}
	return out, nil
}

func (sv *server) formReview(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if !reviewVerbs[verb] {
		http.NotFound(w, r)
		return
	}
	found, err := sv.applyReviewAction(r.PathValue("id"), verb)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	// Ignoring marks the chat personal: always land on /review rather than
	// back on the conversation page, which would re-render the personal
	// messages just hidden (spec §25).
	target := "/review"
	if verb != "ignore" && verb != "always-ignore" {
		target = redirectTarget(r, "/review")
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Conversations
// ---------------------------------------------------------------------------

type conversationRow struct {
	Conversation model.Conversation
	Name         string
	Label        string
	Confidence   float64
	MessageCount int
}

type conversationsData struct {
	Rows []conversationRow
}

func (sv *server) pageConversations(w http.ResponseWriter, _ *http.Request) {
	items, err := sv.classifiedConversations()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]conversationRow, 0, len(items))
	for _, it := range items {
		row := conversationRow{
			Conversation: it.Conv,
			Name:         displayName(it.Conv),
			Label:        effectiveLabel(it.Cls),
			MessageCount: it.MessageCount,
		}
		if it.Cls != nil {
			row.Confidence = it.Cls.BusinessConfidence
		}
		rows = append(rows, row)
	}
	sv.render(w, "conversations", conversationsData{Rows: rows})
}

type messageView struct {
	Message model.Message
	Chip    string // per-message classification label; only set for mixed chats
	Sender  string // display name for the meta row
	Subject string // email subject, set only when it changes within the thread
	Body    string // visible text (for email: quoted history split off)
	Quoted  string // collapsed quoted/forwarded history; "" when none
}

// senderLabel names a message's sender: the stored name when present (shared
// mailboxes keep the real author), then "You" for outbound, then the handle.
func senderLabel(m model.Message) string {
	switch {
	case m.SenderName != "":
		return m.SenderName
	case m.Direction == model.DirectionOutbound:
		return "You"
	case m.SenderHandle != "":
		return m.SenderHandle
	}
	return m.Direction
}

type conversationData struct {
	Conversation   model.Conversation
	Classification *model.ConversationClassification
	Name           string
	Label          string
	Confidence     float64
	Reason         string
	Messages       []messageView
	Actions        []actionView
}

func (sv *server) pageConversation(w http.ResponseWriter, r *http.Request) {
	conv, err := sv.store.GetConversation(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if conv == nil {
		http.NotFound(w, r)
		return
	}
	cls, err := sv.store.GetConversationClassification(conv.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	label := effectiveLabel(cls)
	msgs, err := sv.store.ListMessages(conv.ID, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]messageView, 0, len(msgs))
	prevSubject := ""
	for _, m := range msgs {
		v := messageView{Message: m, Sender: senderLabel(m), Body: m.Body}
		if m.Channel == model.ChannelEmail {
			subject, visible, quoted := parseEmailBody(m.Body)
			v.Body, v.Quoted = visible, quoted
			if subject != "" {
				// Repeat an unchanged subject when there is no body text
				// at all — never render an empty bubble.
				ns := normalizeSubject(subject)
				if ns != prevSubject || visible == "" {
					v.Subject = subject
				}
				if ns != "" {
					prevSubject = ns
				}
			}
			if v.Subject == "" && v.Body == "" && v.Quoted == "" {
				v.Body = m.Body // parser found nothing displayable; show as stored
			}
		}
		if label == model.ConvMixed {
			mc, err := sv.store.GetMessageClassification(m.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if mc != nil {
				v.Chip = mc.Classification
			}
		}
		views = append(views, v)
	}
	actions, err := sv.store.ListActions(store.ActionFilter{ConversationID: conv.ID})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actionViews, err := sv.actionViews(actions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := conversationData{
		Conversation:   *conv,
		Classification: cls,
		Name:           displayName(*conv),
		Label:          label,
		Messages:       views,
		Actions:        actionViews,
	}
	if cls != nil {
		data.Confidence = cls.BusinessConfidence
		data.Reason = cls.Reason
	}
	sv.render(w, "conversation", data)
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

type actionsData struct {
	Actions []actionView
}

func (sv *server) pageActions(w http.ResponseWriter, _ *http.Request) {
	actions, err := sv.store.ListActions(store.ActionFilter{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := sv.actionViews(actions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sv.render(w, "actions", actionsData{Actions: views})
}

func (sv *server) formAction(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if !actionVerbs[verb] {
		http.NotFound(w, r)
		return
	}
	found, err := sv.applyActionVerb(r.PathValue("id"), verb, time.Now().Add(24*time.Hour))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, redirectTarget(r, "/actions"), http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Leaks & search
// ---------------------------------------------------------------------------

type leaksData struct {
	Leaks []leaks.Leak
}

func (sv *server) pageLeaks(w http.ResponseWriter, _ *http.Request) {
	lks, err := leaks.Detect(sv.store, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sv.render(w, "leaks", leaksData{Leaks: lks})
}

type searchData struct {
	Query   string
	Results search.Results
}

func (sv *server) pageSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	res, err := search.Search(sv.store, q, sv.cfg.SearchIncludeIgnored)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sv.render(w, "search", searchData{Query: q, Results: res})
}
