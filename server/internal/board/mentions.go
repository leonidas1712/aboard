package board

import (
	"github.com/leonidas1712/aboard/server/internal/mention"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// MaxMentionWakes is how many agents one message's mentions can wake. Past it, a
// mention is recorded and shown but wakes no one, so one message can't wake a whole
// board by naming a big role.
const MaxMentionWakes = 8

// resolveMentions finds the members body mentions among the board's active members, as
// it is when the message is posted: each once, in the order first mentioned, a role
// naming its members in the order they joined. The sender is never mentioned, and a
// name or role nobody has stays text. A mention wakes an agent only when the agent may
// read the message to `to` anyway, and only for the first MaxMentionWakes such agents.
func resolveMentions(tx ReadTx, b Board, sender Member, to []string, body string, recorded ...[]string) ([]Mention, error) {
	refs := mention.Find(body)
	out := []Mention{}
	if len(refs) == 0 {
		return out, nil
	}
	members, err := tx.Members(b.ID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	woken := 0
	add := func(m Member, text string) {
		if m.ID == sender.ID || m.Status != StatusActive || seen[m.ID] {
			return
		}
		seen[m.ID] = true
		mn := Mention{MemberID: m.ID, Kind: m.Kind, Name: m.Name, Text: text}
		if m.Kind == "agent" {
			switch {
			case !rules.CanRead(b.Policy, to, sender.ID, m.Rules(), recorded...):
				mn.Reason = ptr(MentionCannotRead)
			case woken >= MaxMentionWakes:
				mn.Reason = ptr(MentionLimit)
			default:
				mn.Wakes = true
				woken++
			}
		}
		out = append(out, mn)
	}
	for _, r := range refs {
		if _, ok := b.Roles[r.Role]; r.Role != "" && !ok {
			continue
		}
		for _, m := range members {
			if r.Name != "" && m.Name == r.Name || r.Role != "" && m.Role != nil && *m.Role == r.Role {
				add(m, r.Text)
			}
		}
	}
	return out, nil
}
