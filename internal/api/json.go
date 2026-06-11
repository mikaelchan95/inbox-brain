package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/mikaelchan95/inbox-brain/internal/leaks"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/search"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

// ---------------------------------------------------------------------------
// JSON shapes (camelCase on the wire; zero times marshal as the RFC3339 zero)
// ---------------------------------------------------------------------------

type conversationJSON struct {
	ID            string    `json:"id"`
	Channel       string    `json:"channel"`
	Title         string    `json:"title"`
	ExternalID    string    `json:"externalId"`
	IsGroup       bool      `json:"isGroup"`
	LastMessageAt time.Time `json:"lastMessageAt"`
}

type classificationJSON struct {
	ID                 string    `json:"id"`
	ConversationID     string    `json:"conversationId"`
	Classification     string    `json:"classification"`
	BusinessConfidence float64   `json:"businessConfidence"`
	Source             string    `json:"source"`
	Reason             string    `json:"reason"`
	ReviewedByUser     bool      `json:"reviewedByUser"`
	UserOverride       string    `json:"userOverride"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type classificationItemJSON struct {
	Conversation   conversationJSON    `json:"conversation"`
	Classification *classificationJSON `json:"classification"`
	MessageCount   int                 `json:"messageCount"`
	LastMessageAt  time.Time           `json:"lastMessageAt"`
	Bucket         string              `json:"bucket"`
}

type messageClassificationJSON struct {
	ID                 string  `json:"id"`
	MessageID          string  `json:"messageId"`
	Classification     string  `json:"classification"`
	BusinessConfidence float64 `json:"businessConfidence"`
	Reason             string  `json:"reason"`
	Source             string  `json:"source"`
}

type actionJSON struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	MessageID      string    `json:"messageId"`
	Type           string    `json:"type"`
	Title          string    `json:"title"`
	Summary        string    `json:"summary"`
	SuggestedReply string    `json:"suggestedReply"`
	Confidence     float64   `json:"confidence"`
	Urgency        string    `json:"urgency"`
	Status         string    `json:"status"`
	SnoozedUntil   time.Time `json:"snoozedUntil"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type messageJSON struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Channel        string    `json:"channel"`
	SenderName     string    `json:"senderName"`
	Direction      string    `json:"direction"`
	Body           string    `json:"body"`
	OccurredAt     time.Time `json:"occurredAt"`
}

type connectorJSON struct {
	ID           string `json:"id"`
	Channel      string `json:"channel"`
	Provider     string `json:"provider"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	StatusDetail string `json:"statusDetail"`
}

type leakJSON struct {
	Kind             string    `json:"kind"`
	Severity         string    `json:"severity"`
	ConversationID   string    `json:"conversationId"`
	ConversationName string    `json:"conversationName"`
	ActionID         string    `json:"actionId,omitempty"`
	Description      string    `json:"description"`
	Since            time.Time `json:"since"`
}

type leadJSON struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Status         string    `json:"status"`
	Summary        string    `json:"summary"`
	CreatedAt      time.Time `json:"createdAt"`
}

type searchMessageJSON struct {
	MessageID        string    `json:"messageId"`
	ConversationID   string    `json:"conversationId"`
	ConversationName string    `json:"conversationName"`
	Channel          string    `json:"channel"`
	SenderName       string    `json:"senderName"`
	Snippet          string    `json:"snippet"`
	OccurredAt       time.Time `json:"occurredAt"`
}

type searchResponseJSON struct {
	Messages []searchMessageJSON `json:"messages"`
	Actions  []actionJSON        `json:"actions"`
	Leads    []leadJSON          `json:"leads"`
}

func toConversationJSON(c model.Conversation) conversationJSON {
	return conversationJSON{
		ID:            c.ID,
		Channel:       c.Channel,
		Title:         c.Title,
		ExternalID:    c.ExternalID,
		IsGroup:       c.IsGroup,
		LastMessageAt: c.LastMessageAt,
	}
}

func toClassificationJSON(c *model.ConversationClassification) *classificationJSON {
	if c == nil {
		return nil
	}
	return &classificationJSON{
		ID:                 c.ID,
		ConversationID:     c.ConversationID,
		Classification:     c.Classification,
		BusinessConfidence: c.BusinessConfidence,
		Source:             c.Source,
		Reason:             c.Reason,
		ReviewedByUser:     c.ReviewedByUser,
		UserOverride:       c.UserOverride,
		UpdatedAt:          c.UpdatedAt,
	}
}

func toActionJSON(a model.Action) actionJSON {
	return actionJSON{
		ID:             a.ID,
		ConversationID: a.ConversationID,
		MessageID:      a.MessageID,
		Type:           a.Type,
		Title:          a.Title,
		Summary:        a.Summary,
		SuggestedReply: a.SuggestedReply,
		Confidence:     a.Confidence,
		Urgency:        a.Urgency,
		Status:         a.Status,
		SnoozedUntil:   a.SnoozedUntil,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
	}
}

func toMessageJSON(m model.Message) messageJSON {
	return messageJSON{
		ID:             m.ID,
		ConversationID: m.ConversationID,
		Channel:        m.Channel,
		SenderName:     m.SenderName,
		Direction:      m.Direction,
		Body:           m.Body,
		OccurredAt:     m.OccurredAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck // nothing left to do once headers are sent
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (sv *server) internalError(w http.ResponseWriter, err error) {
	writeError(w, http.StatusInternalServerError, err.Error())
}

// ---------------------------------------------------------------------------
// Classification endpoints
// ---------------------------------------------------------------------------

func (sv *server) apiClassificationList(w http.ResponseWriter, _ *http.Request) {
	items, err := sv.classifiedConversations()
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]classificationItemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, classificationItemJSON{
			Conversation:   toConversationJSON(it.Conv),
			Classification: toClassificationJSON(it.Cls),
			MessageCount:   it.MessageCount,
			LastMessageAt:  it.Conv.LastMessageAt,
			Bucket:         it.Bucket,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (sv *server) apiReviewVerb(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if !reviewVerbs[verb] {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := r.PathValue("id")
	found, err := sv.applyReviewAction(id, verb)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	cls, err := sv.store.GetConversationClassification(id)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"classification": toClassificationJSON(cls),
	})
}

func (sv *server) apiMessageOverride(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msg, err := sv.store.GetMessage(id)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	if msg == nil {
		writeError(w, http.StatusNotFound, "message not found")
		return
	}
	var body struct {
		Classification string `json:"classification"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var confidence float64
	switch body.Classification {
	case model.MsgBusiness:
		confidence = 100
	case model.MsgAmbiguous:
		confidence = 50
	case model.MsgPersonal:
		confidence = 0
	default:
		writeError(w, http.StatusBadRequest, "classification must be business, personal or ambiguous")
		return
	}
	if err := sv.store.SaveMessageClassification(model.MessageClassification{
		MessageID:          id,
		Classification:     body.Classification,
		BusinessConfidence: confidence,
		Reason:             "user override",
		Source:             model.SourceUserOverride,
	}); err != nil {
		sv.internalError(w, err)
		return
	}
	saved, err := sv.store.GetMessageClassification(id)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, messageClassificationJSON{
		ID:                 saved.ID,
		MessageID:          saved.MessageID,
		Classification:     saved.Classification,
		BusinessConfidence: saved.BusinessConfidence,
		Reason:             saved.Reason,
		Source:             saved.Source,
	})
}

// ---------------------------------------------------------------------------
// Action endpoints
// ---------------------------------------------------------------------------

func (sv *server) apiActionsList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	actions, err := sv.store.ListActions(store.ActionFilter{
		Status: q.Get("status"),
		Type:   q.Get("type"),
	})
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]actionJSON, 0, len(actions))
	for _, a := range actions {
		out = append(out, toActionJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (sv *server) apiActionVerb(w http.ResponseWriter, r *http.Request) {
	verb := r.PathValue("verb")
	if !actionVerbs[verb] {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	id := r.PathValue("id")
	until := time.Now().Add(24 * time.Hour)
	if verb == "snooze" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "read body: "+err.Error())
			return
		}
		if len(bytes.TrimSpace(data)) > 0 {
			var body struct {
				Until string `json:"until"`
			}
			if err := json.Unmarshal(data, &body); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON body")
				return
			}
			if body.Until != "" {
				t, err := time.Parse(time.RFC3339, body.Until)
				if err != nil {
					writeError(w, http.StatusBadRequest, "until must be RFC3339")
					return
				}
				until = t
			}
		}
	}
	found, err := sv.applyActionVerb(id, verb, until)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	updated, err := sv.store.GetAction(id)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toActionJSON(*updated))
}

// ---------------------------------------------------------------------------
// Conversation endpoints
// ---------------------------------------------------------------------------

func (sv *server) apiConversationsList(w http.ResponseWriter, _ *http.Request) {
	convs, err := sv.store.ListConversations(store.ConversationFilter{})
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]conversationJSON, 0, len(convs))
	for _, c := range convs {
		out = append(out, toConversationJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

// loadConversation writes a 404/500 response and returns nil when the
// conversation cannot be served.
func (sv *server) loadConversation(w http.ResponseWriter, id string) *model.Conversation {
	conv, err := sv.store.GetConversation(id)
	if err != nil {
		sv.internalError(w, err)
		return nil
	}
	if conv == nil {
		writeError(w, http.StatusNotFound, "conversation not found")
		return nil
	}
	return conv
}

func (sv *server) apiConversation(w http.ResponseWriter, r *http.Request) {
	conv := sv.loadConversation(w, r.PathValue("id"))
	if conv == nil {
		return
	}
	writeJSON(w, http.StatusOK, toConversationJSON(*conv))
}

func (sv *server) apiConversationMessages(w http.ResponseWriter, r *http.Request) {
	conv := sv.loadConversation(w, r.PathValue("id"))
	if conv == nil {
		return
	}
	msgs, err := sv.store.ListMessages(conv.ID, 0)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]messageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessageJSON(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (sv *server) apiConversationActions(w http.ResponseWriter, r *http.Request) {
	conv := sv.loadConversation(w, r.PathValue("id"))
	if conv == nil {
		return
	}
	actions, err := sv.store.ListActions(store.ActionFilter{ConversationID: conv.ID})
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]actionJSON, 0, len(actions))
	for _, a := range actions {
		out = append(out, toActionJSON(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Connector endpoints
// ---------------------------------------------------------------------------

func (sv *server) apiConnectorsList(w http.ResponseWriter, _ *http.Request) {
	conns, err := sv.store.ListConnectors()
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]connectorJSON, 0, len(conns))
	for _, c := range conns {
		out = append(out, connectorJSON{
			ID:           c.ID,
			Channel:      c.Channel,
			Provider:     c.Provider,
			Name:         c.Name,
			Status:       c.Status,
			StatusDetail: c.StatusDetail,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiConnectorSync acknowledges a sync request. In v0.1 syncs run via the
// CLI (ib sync), so this is a stub that only validates the connector id.
func (sv *server) apiConnectorSync(w http.ResponseWriter, r *http.Request) {
	conn, err := sv.store.GetConnector(r.PathValue("id"))
	if err != nil {
		sv.internalError(w, err)
		return
	}
	if conn == nil {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sync requested"})
}

func (sv *server) apiWebhookStub(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, "webhook mode not available in v0.1; use ib sync")
}

// ---------------------------------------------------------------------------
// Search & leaks
// ---------------------------------------------------------------------------

func (sv *server) apiSearch(w http.ResponseWriter, r *http.Request) {
	res, err := search.Search(sv.store, r.URL.Query().Get("q"), sv.cfg.SearchIncludeIgnored)
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := searchResponseJSON{
		Messages: make([]searchMessageJSON, 0, len(res.Messages)),
		Actions:  make([]actionJSON, 0, len(res.Actions)),
		Leads:    make([]leadJSON, 0, len(res.Leads)),
	}
	for _, m := range res.Messages {
		out.Messages = append(out.Messages, searchMessageJSON{
			MessageID:        m.MessageID,
			ConversationID:   m.ConversationID,
			ConversationName: m.ConversationName,
			Channel:          m.Channel,
			SenderName:       m.SenderName,
			Snippet:          m.Snippet,
			OccurredAt:       m.OccurredAt,
		})
	}
	for _, a := range res.Actions {
		out.Actions = append(out.Actions, toActionJSON(a))
	}
	for _, l := range res.Leads {
		out.Leads = append(out.Leads, leadJSON{
			ID:             l.ID,
			ConversationID: l.ConversationID,
			Status:         l.Status,
			Summary:        l.Summary,
			CreatedAt:      l.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (sv *server) apiLeaks(w http.ResponseWriter, _ *http.Request) {
	found, err := leaks.Detect(sv.store, time.Now())
	if err != nil {
		sv.internalError(w, err)
		return
	}
	out := make([]leakJSON, 0, len(found))
	for _, l := range found {
		out = append(out, leakJSON{
			Kind:             l.Kind,
			Severity:         l.Severity,
			ConversationID:   l.ConversationID,
			ConversationName: l.ConversationName,
			ActionID:         l.ActionID,
			Description:      l.Description,
			Since:            l.Since,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
