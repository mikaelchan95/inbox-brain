package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mikaelchan95/inbox-brain/internal/extract"
	"github.com/mikaelchan95/inbox-brain/internal/model"
	"github.com/mikaelchan95/inbox-brain/internal/store"
)

const classifyUsage = "usage: ib classify conversations|review|approve|ignore|mixed [<conversation-id>]"

// cmdClassify dispatches the classify subcommands.
func cmdClassify(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", classifyUsage)
	}
	sub, rest := args[0], args[1:]

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	switch sub {
	case "conversations":
		pl, err := e.newPipeline(extract.NewRulesProvider(), stdout)
		if err != nil {
			return err
		}
		n, err := pl.ClassifyAll(context.Background())
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Classified %d conversation(s).\n", n)
		return nil
	case "review":
		return classifyReview(e, stdout)
	case "approve", "ignore", "mixed":
		if len(rest) != 1 {
			return fmt.Errorf("usage: ib classify %s <conversation-id>", sub)
		}
		return classifyVerdict(e, sub, rest[0], stdout)
	default:
		return fmt.Errorf("unknown classify subcommand %q\n%s", sub, classifyUsage)
	}
}

// reviewLine is one conversation in the review listing.
type reviewLine struct {
	conv   model.Conversation
	cls    *model.ConversationClassification
	reason string
	conf   float64
}

// classifyReview lists conversations bucketed for review (spec §11): each
// line shows id, title, channel, confidence and reason — no message snippets.
func classifyReview(e *env, stdout io.Writer) error {
	convs, err := e.st.ListConversations(store.ConversationFilter{})
	if err != nil {
		return err
	}

	var suggested, needsReview, mixed, ignored []reviewLine
	approved := 0
	for _, conv := range convs {
		cls, err := e.st.GetConversationClassification(conv.ID)
		if err != nil {
			return err
		}
		line := reviewLine{conv: conv, cls: cls}
		if cls == nil {
			line.reason = "not classified yet — run: ib classify conversations"
			needsReview = append(needsReview, line)
			continue
		}
		line.reason = cls.Reason
		line.conf = cls.BusinessConfidence
		switch eff := effectiveLabel(cls); {
		case eff == model.ConvPersonal:
			ignored = append(ignored, line)
		case eff == model.ConvMixed:
			mixed = append(mixed, line)
		case eff == model.ConvBusiness && cls.ReviewedByUser:
			approved++
		case eff == model.ConvBusiness && cls.BusinessConfidence >= model.ThresholdSuggest:
			suggested = append(suggested, line)
		default: // unknown, or business below the suggest threshold
			needsReview = append(needsReview, line)
		}
	}

	printBucket := func(header string, lines []reviewLine) {
		fmt.Fprintf(stdout, "%s (%d):\n", header, len(lines))
		for _, l := range lines {
			fmt.Fprintf(stdout, "  %s  %-24s %-9s %3.0f%%  %s\n",
				l.conv.ID, convDisplayName(l.conv), l.conv.Channel, l.conf, l.reason)
		}
		fmt.Fprintln(stdout)
	}
	printBucket("Suggested business chats", suggested)
	printBucket("Needs review", needsReview)
	printBucket("Mixed chats", mixed)
	printBucket("Ignored as personal", ignored)
	fmt.Fprintf(stdout, "Approved: %d conversation(s).\n", approved)
	fmt.Fprintf(stdout, "Next: ib classify approve|ignore|mixed <conversation-id>\n")
	return nil
}

// classifyVerdict applies the user's review decision for one conversation:
// approve = mark reviewed (+ override business when the label is not already
// business); ignore = override personal; mixed = override mixed. Each records
// an audit event.
func classifyVerdict(e *env, verdict, conversationID string, stdout io.Writer) error {
	conv, err := e.st.GetConversation(conversationID)
	if err != nil {
		return err
	}
	if conv == nil {
		return fmt.Errorf("conversation %s not found — list ids with: ib classify review", conversationID)
	}
	name := convDisplayName(*conv)

	var detail, confirmation string
	switch verdict {
	case "approve":
		cls, err := e.st.GetConversationClassification(conversationID)
		if err != nil {
			return err
		}
		if err := e.st.MarkReviewed(conversationID); err != nil {
			return err
		}
		if cls == nil || cls.Classification != model.ConvBusiness {
			if err := e.st.SetUserOverride(conversationID, model.ConvBusiness); err != nil {
				return err
			}
		}
		detail = fmt.Sprintf("user approved %q as business via CLI", name)
		confirmation = fmt.Sprintf("Approved %q (%s) as business — extract it with: ib extract --approved-only", name, conversationID)
	case "ignore":
		if err := e.st.SetUserOverride(conversationID, model.ConvPersonal); err != nil {
			return err
		}
		// Spec §24.5: marking a chat personal purges its derived business
		// artifacts so nothing from it lingers on the dashboard.
		nActions, err := e.st.DeleteActionsForConversation(conversationID)
		if err != nil {
			return err
		}
		nLeads, err := e.st.DeleteLeadsForConversation(conversationID)
		if err != nil {
			return err
		}
		detail = fmt.Sprintf("user ignored %q as personal via CLI", name)
		confirmation = fmt.Sprintf("Ignored %q (%s) as personal — it will not be extracted or searched.", name, conversationID)
		if nActions+nLeads > 0 {
			confirmation += fmt.Sprintf(" Removed %d derived action(s) and %d lead(s).", nActions, nLeads)
		}
	case "mixed":
		if err := e.st.SetUserOverride(conversationID, model.ConvMixed); err != nil {
			return err
		}
		detail = fmt.Sprintf("user marked %q as mixed via CLI", name)
		confirmation = fmt.Sprintf("Marked %q (%s) as mixed — only its business messages will be extracted.", name, conversationID)
	}

	if err := e.st.AddAuditEvent(model.AuditEvent{
		WorkspaceID: e.ws.ID,
		EventType:   "user_override",
		Subject:     conversationID,
		Detail:      detail,
	}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, confirmation)
	return nil
}
