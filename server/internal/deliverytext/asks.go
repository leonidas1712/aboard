package deliverytext

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Ask holds only the recorded ask fields the recipient may read.
type Ask struct {
	ToName    string
	Blocking  bool
	Options   []string
	GoingWith string
	GoingAt   *time.Time
}

// Answer names the recorded decision, not a decision inferred from its body.
type Answer struct {
	Seq, Option int
	OptionText  string
	Withdrawn   bool
}

func askFooter(m Message, contexts []Context) string {
	if m.Ask != nil {
		a := m.Ask
		var b strings.Builder
		for i, option := range a.Options {
			fmt.Fprintf(&b, "\n%d. %s", i+1, plainLine(option))
		}
		command := boardCommand("aboard say", m.Board, contexts) + fmt.Sprintf(" --reply %d", m.Seq)
		if len(a.Options) > 0 {
			fmt.Fprintf(&b, "\nAnswer with: %s --option 1 (or in your own words: %s \"…\").", command, command)
		} else {
			fmt.Fprintf(&b, "\nAnswer with: %s \"…\".", command)
		}
		if a.GoingWith != "" {
			fmt.Fprintf(&b, "\nGoing with %q", EscapeBody(a.GoingWith))
			if a.GoingAt != nil {
				b.WriteString(" at " + a.GoingAt.Format("15:04"))
			}
			b.WriteString(" unless you say otherwise.")
		}
		return b.String()
	}
	if a := m.Answer; a != nil {
		if a.Withdrawn {
			return fmt.Sprintf("\nAboard: @%s withdrew ask #%d.", plainLine(m.FromName), a.Seq)
		}
		text := fmt.Sprintf("\nAboard: @%s answered your ask #%d", plainLine(m.FromName), a.Seq)
		if a.Option > 0 {
			text += " with option " + strconv.Itoa(a.Option)
			if a.OptionText != "" {
				text += fmt.Sprintf(", %q", plainLine(a.OptionText))
			}
		}
		return text + "."
	}
	return ""
}

var askDecisionPhrase = regexp.MustCompile(`(?i)\b(?:I'm blocked|blocked on|waiting (?:for|on) you|need your (?:decision|approval|input)|can you (?:decide|confirm))\b`)

// AskInstead recognizes only the approved fixed phrases, never a model judgment.
func AskInstead(w TaskWork, m Message, board string, contexts ...Context) *Nudge {
	if !w.Nudges || m.Ask != nil || m.Answer != nil || m.ExpectsReply || w.AsksWaiting > 0 {
		return nil
	}
	if !askDecisionPhrase.MatchString(m.Body) {
		return nil
	}
	text := "Tip: to get a decision, ask: " + boardCommand(`aboard ask "…" "option" "option"`, board, contexts) + "."
	if w.CurrentTask != nil {
		text += " It marks " + w.CurrentTask.Ref + " Blocked and the answer wakes you."
	}
	return taskNudge("ask_instead", text)
}

// AskOptions suggests useful choices after an ask without options.
func AskOptions(w TaskWork, m Message) *Nudge {
	if !w.Nudges || m.Ask == nil || len(m.Ask.Options) > 0 {
		return nil
	}
	who := "the recipient"
	if m.Ask.ToName != "" {
		who = "@" + plainLine(m.Ask.ToName)
	}
	return taskNudge("ask_options", "Tip: next time add 2 to 4 options after the question; "+who+" answers with one key, or in their own words.")
}
