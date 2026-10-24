package clisession

import (
	"fmt"
	"strings"

	"github.com/noopolis/moltnet/pkg/protocol"
)

const maxNudgeSenders = 5

// Nudge is the coalesced notice for one Moltnet conversation that an attached
// session has unread messages. It never carries message bodies: the session
// pulls them itself with `moltnet read --since-last`.
type Nudge struct {
	Target  protocol.Target
	Count   int
	Senders []string
}

// Add folds one more message into the nudge.
func (n *Nudge) Add(message *protocol.Message) {
	if message == nil {
		return
	}
	if n.Count == 0 {
		n.Target = message.Target
	}
	n.Count++
	n.addSender(message.From.ID)
}

// Merge folds a later nudge for the same conversation into this one.
func (n *Nudge) Merge(later *Nudge) {
	if later == nil || later.Count == 0 {
		return
	}
	if n.Count == 0 {
		n.Target = later.Target
	}
	n.Count += later.Count
	for _, sender := range later.Senders {
		n.addSender(sender)
	}
}

func (n *Nudge) addSender(sender string) {
	sender = strings.TrimSpace(sender)
	if sender == "" {
		return
	}
	for _, existing := range n.Senders {
		if existing == sender {
			return
		}
	}
	n.Senders = append(n.Senders, sender)
}

// Text renders the one-line nudge delivered into the attached session.
func (n Nudge) Text() string {
	ref, label := nudgeTargetRef(n.Target)
	noun := "messages"
	if n.Count == 1 {
		noun = "message"
	}
	text := fmt.Sprintf("[moltnet] %d new %s in %s", n.Count, noun, label)
	if senders := n.senderList(); senders != "" {
		text += " from " + senders
	}
	return text + ". Read: moltnet read --since-last " + ref
}

func (n Nudge) senderList() string {
	if len(n.Senders) <= maxNudgeSenders {
		return strings.Join(n.Senders, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(n.Senders[:maxNudgeSenders], ", "), len(n.Senders)-maxNudgeSenders)
}

func nudgeTargetRef(target protocol.Target) (string, string) {
	switch target.Kind {
	case protocol.TargetKindDM:
		ref := "dm:" + target.DMID
		return ref, ref
	case protocol.TargetKindThread:
		ref := "thread:" + target.ThreadID
		return ref, ref
	default:
		return "room:" + target.RoomID, "#" + target.RoomID
	}
}
