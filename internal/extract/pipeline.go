package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mikaelchan/inbox-brain/internal/classify"
	"github.com/mikaelchan/inbox-brain/internal/model"
	"github.com/mikaelchan/inbox-brain/internal/store"
)

// maxContextMessages caps how many eligible messages are sent to a provider
// per conversation (the most recent ones win).
const maxContextMessages = 30

// Pipeline classifies conversations and extracts actions from approved
// business content.
type Pipeline struct {
	Store      *store.Store
	Classifier *classify.Classifier
	Provider   Provider
	Profile    model.BusinessProfile
	AutoMode   bool
	Out        io.Writer // progress output; never nil after NewPipeline
}

// NewPipeline wires a Pipeline; Out defaults to os.Stdout.
func NewPipeline(s *store.Store, c *classify.Classifier, p Provider, profile model.BusinessProfile, autoMode bool) *Pipeline {
	return &Pipeline{Store: s, Classifier: c, Provider: p, Profile: profile, AutoMode: autoMode, Out: os.Stdout}
}

// ClassifyAll (re)classifies every conversation that has no user override and
// was not reviewed by the user (never clobbering user decisions), persists
// the results, and returns how many conversations were (re)classified.
func (p *Pipeline) ClassifyAll(ctx context.Context) (int, error) {
	convs, err := p.Store.ListConversations(store.ConversationFilter{})
	if err != nil {
		return 0, fmt.Errorf("classify all: %w", err)
	}
	count := 0
	for _, conv := range convs {
		if err := ctx.Err(); err != nil {
			return count, fmt.Errorf("classify all: %w", err)
		}
		existing, err := p.Store.GetConversationClassification(conv.ID)
		if err != nil {
			return count, fmt.Errorf("classify all: %w", err)
		}
		if existing != nil && (existing.UserOverride != "" || existing.ReviewedByUser) {
			continue
		}
		msgs, err := p.Store.ListMessages(conv.ID, 0)
		if err != nil {
			return count, fmt.Errorf("classify all: %w", err)
		}
		cls := p.Classifier.ScoreConversation(conv, msgs)
		if err := p.Store.SaveConversationClassification(cls); err != nil {
			return count, fmt.Errorf("classify all: %w", err)
		}
		count++
	}
	return count, nil
}

// Summary reports what one ProcessApproved pass did.
type Summary struct {
	ConversationsProcessed int
	ConversationsSkipped   int
	ActionsCreated         int
	Failures               int
}

// ProcessApproved runs extraction over every conversation that passes the
// spec §13 privacy gate:
//
//	user_override personal → skip; user_override business → full thread;
//	label personal → skip; label business → only if reviewed by the user or
//	(AutoMode and confidence >= model.ThresholdAuto), otherwise it stays in
//	the review queue; label/override mixed → business-labeled messages only;
//	label unknown or no classification → skip.
//
// Provider failures mark the run failed and processing continues with the
// other conversations (spec §24.2).
func (p *Pipeline) ProcessApproved(ctx context.Context) (Summary, error) {
	var sum Summary
	convs, err := p.Store.ListConversations(store.ConversationFilter{})
	if err != nil {
		return sum, fmt.Errorf("process approved: %w", err)
	}
	for _, conv := range convs {
		if err := ctx.Err(); err != nil {
			return sum, fmt.Errorf("process approved: %w", err)
		}
		cls, err := p.Store.GetConversationClassification(conv.ID)
		if err != nil {
			return sum, fmt.Errorf("process approved: %w", err)
		}
		ok, mixed := eligibility(cls, p.AutoMode)
		if !ok {
			sum.ConversationsSkipped++
			continue
		}
		created, processed, err := p.extractConversation(ctx, conv, mixed)
		var pf *providerFailedError
		if errors.As(err, &pf) {
			sum.Failures++
			fmt.Fprintf(p.Out, "extraction failed for %s: %v\n", convName(conv), err)
			continue
		}
		if err != nil {
			return sum, fmt.Errorf("process approved: %w", err)
		}
		if !processed {
			sum.ConversationsSkipped++
			continue
		}
		sum.ConversationsProcessed++
		sum.ActionsCreated += created
		fmt.Fprintf(p.Out, "extracted %d action(s) from %s\n", created, convName(conv))
	}
	return sum, nil
}

// ProcessConversation runs extraction for one approved conversation id (same
// gating as ProcessApproved) and returns the number of actions created. It
// returns an error when the conversation is missing or not eligible.
func (p *Pipeline) ProcessConversation(ctx context.Context, conversationID string) (int, error) {
	conv, err := p.Store.GetConversation(conversationID)
	if err != nil {
		return 0, fmt.Errorf("process conversation: %w", err)
	}
	if conv == nil {
		return 0, fmt.Errorf("process conversation: conversation %s not found", conversationID)
	}
	cls, err := p.Store.GetConversationClassification(conversationID)
	if err != nil {
		return 0, fmt.Errorf("process conversation: %w", err)
	}
	ok, mixed := eligibility(cls, p.AutoMode)
	if !ok {
		return 0, fmt.Errorf("conversation %s is not approved for extraction", conversationID)
	}
	created, _, err := p.extractConversation(ctx, *conv, mixed)
	if err != nil {
		return created, fmt.Errorf("process conversation %s: %w", conversationID, err)
	}
	return created, nil
}

// eligibility implements the spec §13 privacy gate. It reports whether the
// conversation may be extracted at all and whether it must be filtered at the
// message level (mixed).
func eligibility(cls *model.ConversationClassification, autoMode bool) (ok, mixed bool) {
	if cls == nil {
		return false, false // never classified → never extracted
	}
	switch cls.UserOverride {
	case "":
		// no override → fall through to the classifier label below
	case model.ConvBusiness:
		return true, false
	case model.ConvMixed:
		return true, true
	default:
		return false, false // personal (or anything unexpected): never extract
	}
	switch cls.Classification {
	case model.ConvBusiness:
		if cls.ReviewedByUser || (autoMode && cls.BusinessConfidence >= model.ThresholdAuto) {
			return true, false
		}
		return false, false // stays in the review queue
	case model.ConvMixed:
		return true, true
	default:
		return false, false // personal, unknown
	}
}

// providerFailedError marks per-conversation provider failures that must not
// abort ProcessApproved (spec §24.2).
type providerFailedError struct{ err error }

func (e *providerFailedError) Error() string { return e.err.Error() }
func (e *providerFailedError) Unwrap() error { return e.err }

// eligibleMessages returns the messages allowed into provider context for an
// approved conversation. Mixed conversations are filtered at the message
// level: every message is scored with Classifier.ScoreMessage and persisted,
// and only business-labeled messages with confidence >=
// model.ThresholdSuggest pass, in both directions (spec §14, §25). Non-mixed
// eligible conversations keep the full thread, including the user's outbound
// replies, for context.
func (p *Pipeline) eligibleMessages(conv model.Conversation, mixed bool) ([]model.Message, error) {
	msgs, err := p.Store.ListMessages(conv.ID, 0)
	if err != nil {
		return nil, fmt.Errorf("list messages for %s: %w", conv.ID, err)
	}
	if !mixed {
		return msgs, nil
	}
	var out []model.Message
	for _, m := range msgs {
		mc := p.Classifier.ScoreMessage(m)
		if err := p.Store.SaveMessageClassification(mc); err != nil {
			return nil, fmt.Errorf("save message classification for %s: %w", m.ID, err)
		}
		if mc.Classification == model.MsgBusiness && mc.BusinessConfidence >= model.ThresholdSuggest {
			out = append(out, m)
		}
	}
	return out, nil
}

// extractConversation runs one extraction over an already-approved
// conversation. processed is false when there was nothing to do (no eligible
// inbound message without an existing action). Provider failures are returned
// as *providerFailedError after the run is marked failed.
func (p *Pipeline) extractConversation(ctx context.Context, conv model.Conversation, mixed bool) (created int, processed bool, err error) {
	msgs, err := p.eligibleMessages(conv, mixed)
	if err != nil {
		return 0, false, err
	}

	// Candidates are eligible inbound messages; skip the conversation when
	// every one of them already has an action anchored to it.
	var latestInbound *model.Message
	pending := false
	for i := range msgs {
		m := &msgs[i]
		if m.Direction != model.DirectionInbound {
			continue
		}
		latestInbound = m
		if !pending {
			exists, err := p.Store.ActionExistsForMessage(m.ID)
			if err != nil {
				return 0, false, err
			}
			if !exists {
				pending = true
			}
		}
	}
	if latestInbound == nil || !pending {
		return 0, false, nil
	}

	window := msgs
	if len(window) > maxContextMessages {
		window = window[len(window)-maxContextMessages:]
	}

	run, err := p.Store.CreateExtractionRun(model.ExtractionRun{
		WorkspaceID:    conv.WorkspaceID,
		ConversationID: conv.ID,
		Provider:       p.Provider.Name(),
		Status:         model.RunPending,
		InputMessages:  len(window),
	})
	if err != nil {
		return 0, false, err
	}
	if err := p.Store.AddAuditEvent(model.AuditEvent{
		WorkspaceID: conv.WorkspaceID,
		EventType:   "ai_context_sent",
		Subject:     conv.ID,
		Detail:      fmt.Sprintf("%d messages from %s sent to %s", len(window), convName(conv), p.Provider.Name()),
	}); err != nil {
		return 0, false, err
	}

	res, perr := p.Provider.ExtractActions(ctx, ProviderInput{
		Profile:      p.Profile,
		Conversation: conv,
		Messages:     window,
	})
	if perr != nil {
		run.Status = model.RunFailed
		run.Error = perr.Error()
		if err := p.Store.FinishExtractionRun(run); err != nil {
			return 0, false, err
		}
		return 0, false, &providerFailedError{fmt.Errorf("provider %s: %w", p.Provider.Name(), perr)}
	}
	res = ValidateResult(res)

	for _, ea := range res.Actions {
		// Anchor to the message the provider named when it resolves to one of
		// the eligible messages; otherwise to the latest eligible inbound one.
		anchor := *latestInbound
		if ea.MessageExternalID != "" {
			for _, m := range msgs {
				if m.MessageExternalID == ea.MessageExternalID {
					anchor = m
					break
				}
			}
		}
		exists, err := p.Store.ActionExistsForMessage(anchor.ID)
		if err != nil {
			return created, true, err
		}
		if exists {
			continue
		}
		act, err := p.Store.CreateAction(model.Action{
			WorkspaceID:    conv.WorkspaceID,
			ConversationID: conv.ID,
			MessageID:      anchor.ID,
			CustomerID:     anchor.CustomerID,
			Type:           ea.Type,
			Title:          ea.Title,
			Summary:        ea.Summary,
			SuggestedReply: ea.SuggestedReply,
			Confidence:     ea.Confidence,
			Urgency:        ea.Urgency,
			Status:         model.StatusOpen,
			Source:         p.Provider.Name(),
		})
		if err != nil {
			return created, true, err
		}
		created++
		if ea.Type == model.ActionNewLead {
			if _, err := p.Store.UpsertLead(model.Lead{
				WorkspaceID:    conv.WorkspaceID,
				ConversationID: conv.ID,
				CustomerID:     anchor.CustomerID,
				ActionID:       act.ID,
				Status:         model.LeadOpen,
				Summary:        ea.Summary,
			}); err != nil {
				return created, true, err
			}
		}
	}

	run.Status = model.RunSuccess
	run.ActionsCreated = created
	if err := p.Store.FinishExtractionRun(run); err != nil {
		return created, true, err
	}
	return created, true, nil
}

func convName(c model.Conversation) string {
	if strings.TrimSpace(c.Title) != "" {
		return c.Title
	}
	return c.ID
}
