package board

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// NewAsk asks one member to decide, optionally stating a non-blocking default.
type NewAsk struct {
	Options   []string   `json:"options"`
	GoingWith *string    `json:"going_with"`
	GoingAt   *time.Time `json:"going_at"`
	Approval  bool       `json:"approval"`
}

// NewAnswer marks a reply as a choice or withdrawal.
type NewAnswer struct {
	Option    *int `json:"option"`
	Withdrawn bool `json:"withdrawn"`
}

// Ask holds immutable posting facts and a projection of subsequent answers.
type Ask struct {
	To           string   `json:"to"`
	Options      []string `json:"options"`
	Blocking     bool     `json:"blocking"`
	GoingWith    *string  `json:"going_with"`
	GoingAt      *string  `json:"going_at"`
	TaskID       *string  `json:"task_id"`
	Target       Member   `json:"-"`
	Task         *TaskRef `json:"-"`
	State        string   `json:"-"`
	AnswerSeq    *int64   `json:"-"`
	AnswerOption *int     `json:"-"`
	CanAnswer    bool     `json:"-"`
	CanWithdraw  bool     `json:"-"`
}

// Answer records which immutable ask a reply answered.
type Answer struct {
	AskID      string  `json:"ask_id"`
	Option     *int    `json:"option"`
	Withdrawn  bool    `json:"withdrawn"`
	AskSeq     int64   `json:"-"`
	OptionText *string `json:"-"`
}

// BlockedOn is one blocking ask the task reader may see.
type BlockedOn struct {
	AskID  string
	AskSeq int64
	To     Member
	Since  string
}

func askError(status int, code, text string) error {
	return apierr.New(status, code, text, "Read the ask, then reply to its message or withdraw your own ask.")
}

func askRecipient(tx ReadTx, b Board, me Member, in NewMessage) ([]string, error) {
	if in.Ask == nil {
		return in.To, nil
	}
	if in.Answer != nil || in.ReplyTo != nil {
		return nil, askError(422, "ask_invalid", "An ask can't also answer or reply to another message.")
	}
	if in.Ask.Approval {
		return nil, askError(422, "ask_invalid", "File approval asks are not supported by this server.")
	}
	to := in.To
	if len(to) == 0 && me.Kind == "agent" {
		owner, e := tx.HumanMember(b.ID, me.HumanID)
		if e != nil {
			return nil, e
		}
		to = []string{"@" + owner.Name}
	}
	if len(to) != 1 || !strings.HasPrefix(to[0], "@") {
		return nil, askError(422, "ask_invalid", "An ask must address one member by name.")
	}
	if len(in.Ask.Options) > 4 || (in.Ask.GoingAt != nil && in.Ask.GoingWith == nil) {
		return nil, askError(422, "ask_invalid", "Use at most four options and a default when setting its time.")
	}
	for _, v := range in.Ask.Options {
		if utf8.RuneCountInString(v) > 80 || strings.TrimSpace(v) == "" {
			return nil, askError(422, "ask_invalid", "Each option must contain 1 to 80 characters.")
		}
	}
	if in.Ask.GoingWith != nil && (strings.TrimSpace(*in.Ask.GoingWith) == "" || utf8.RuneCountInString(*in.Ask.GoingWith) > 120) {
		return nil, askError(422, "ask_invalid", "The default must contain 1 to 120 characters.")
	}
	return to, nil
}

func makeAsk(tx ReadTx, b Board, to []string, in *NewAsk, tags []TaskTag, now time.Time) (*Ask, error) {
	if in == nil {
		return nil, nil
	}
	target, e := tx.MemberByName(b.ID, strings.TrimPrefix(to[0], "@"))
	if e != nil {
		return nil, e
	}
	if target.Status != StatusActive {
		return nil, askError(422, "ask_invalid", "The member asked must still be on the board.")
	}
	a := &Ask{To: target.ID, Options: in.Options, Blocking: in.GoingWith == nil, GoingWith: in.GoingWith}
	if a.Options == nil {
		a.Options = []string{}
	}
	if in.GoingWith != nil {
		at := now
		if in.GoingAt != nil {
			at = *in.GoingAt
		}
		v := stamp(at)
		a.GoingAt = &v
	}
	for _, tag := range tags {
		if tag.How != "named" {
			a.TaskID = ptr(tag.ID)
			break
		}
	}
	return a, nil
}

func mayAnswer(me Member, m Message) bool {
	return m.Ask != nil && (me.ID == m.Ask.To || (me.Kind == "human" && m.SenderKind == "agent" && me.HumanID == m.SenderHuman))
}

func resolveAnswer(tx ReadTx, b Board, me Member, in NewMessage) (*Answer, error) {
	if in.ReplyTo == nil {
		if in.Answer != nil {
			return nil, askError(422, "ask_invalid", "An answer must reply to an ask.")
		}
		return nil, nil
	}
	orig, e := tx.MessageByID(*in.ReplyTo)
	if e != nil {
		return nil, e
	}
	if orig.Ask == nil {
		if in.Answer != nil {
			return nil, askError(422, "ask_invalid", "This message isn't an ask.")
		}
		return nil, nil
	}
	if !rules.CanRead(b.Policy, orig.To, orig.SenderID, me.Rules()) {
		if in.Answer != nil {
			return nil, apierr.New(404, "message_not_found", "The ask isn't available.", "Read a message you can access.")
		}
		return nil, nil
	}
	withdraw := in.Answer != nil && in.Answer.Withdrawn
	if withdraw && me.ID != orig.SenderID {
		return nil, askError(403, "not_asked", "Only the asker may withdraw an ask.")
	}
	if !withdraw && !mayAnswer(me, orig) {
		if in.Answer != nil {
			return nil, askError(403, "not_asked", "You aren't allowed to answer this ask.")
		}
		return nil, nil
	}
	latest, e := tx.LatestAnswer(orig.ID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return nil, e
	}
	if latest.Answer != nil && latest.Answer.Withdrawn {
		return nil, askError(409, "ask_closed", "This ask was withdrawn.")
	}
	a := &Answer{AskID: orig.ID, AskSeq: orig.Seq, Withdrawn: withdraw}
	if in.Answer != nil {
		a.Option = in.Answer.Option
	}
	if a.Option != nil {
		if withdraw || *a.Option < 1 || *a.Option > len(orig.Ask.Options) {
			return nil, askError(422, "ask_invalid", "The option isn't among this ask's options.")
		}
		a.OptionText = ptr(orig.Ask.Options[*a.Option-1])
	}
	return a, nil
}

func projectAsk(tx ReadTx, me Member, m *Message, now time.Time) error {
	if m.Ask == nil && m.Answer == nil {
		return nil
	}
	b, e := tx.BoardByID(m.BoardID)
	if e != nil {
		return e
	}
	if m.Answer != nil {
		orig, e := tx.MessageByID(m.Answer.AskID)
		if e != nil {
			return e
		}
		m.Answer.AskSeq = orig.Seq
		if m.Answer.Option != nil && rules.CanRead(b.Policy, orig.To, orig.SenderID, me.Rules()) {
			m.Answer.OptionText = ptr(orig.Ask.Options[*m.Answer.Option-1])
		}
	}
	if m.Ask == nil {
		return nil
	}
	a := m.Ask
	target, e := tx.MemberByID(a.To)
	if e != nil {
		return e
	}
	a.Target = target
	var role rules.Role
	if me.Role != nil {
		role = b.Roles[*me.Role]
	}
	writable := lifecycleOf(b) == LifecycleActive && rules.CheckPost(b.Policy, role, me.Rules(), []string{"@" + m.SenderName}, false) == ""
	a.CanAnswer = writable && mayAnswer(me, *m)
	a.CanWithdraw = writable && me.ID == m.SenderID
	if a.TaskID != nil {
		t, e := tx.TaskBySelector(m.BoardID, *a.TaskID)
		if e != nil {
			return e
		}
		ref := taskRef(t)
		a.Task = &ref
	}
	a.State = "open"
	latest, e := tx.LatestAnswer(m.ID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return e
	}
	if latest.Answer != nil {
		a.State = "answered"
		if latest.Answer.Withdrawn {
			a.State = "withdrawn"
		}
		a.AnswerSeq = ptr(latest.Seq)
		a.AnswerOption = latest.Answer.Option
	} else if a.GoingAt != nil {
		at, e := time.Parse(time.RFC3339Nano, *a.GoingAt)
		if e != nil {
			return e
		}
		if !now.Before(at) {
			a.State = "went_with"
		}
	}
	if a.State == "withdrawn" {
		a.CanAnswer = false
		a.CanWithdraw = false
	}
	return nil
}

// AskFilter selects asks across the caller's current boards.
type AskFilter struct {
	Board, Task, State string
	ToMe, FromMe       bool
	Limit              int
}

// AskItem retains the board and reader needed to render the message safely.
type AskItem struct {
	Board   Board
	Reader  Member
	Message Message
}

// AskListing contains the visible asks, newest first, without acknowledging them.
type AskListing struct {
	Asks []AskItem
	More bool
}

// ListAsks returns current visible asks without moving read positions.
func (s *Service) ListAsks(ctx context.Context, p Principal, f AskFilter) (AskListing, error) {
	out := AskListing{Asks: []AskItem{}}
	if p.Delegation != nil {
		return out, apierr.New(403, "forbidden", "A delegation cannot read asks.", "Use your member credential.")
	}
	e := s.st.Read(ctx, func(tx ReadTx) error {
		if e := stillValid(tx, p, stamp(s.clk.Now())); e != nil {
			return e
		}
		var boards []Board
		switch {
		case f.Board != "":
			b, _, e := s.access(tx, p, f.Board)
			if e != nil {
				return e
			}
			boards = []Board{b}
		case p.Agent != nil:
			b, _, e := seatOf(tx, *p.Agent)
			if e != nil {
				return e
			}
			boards = []Board{b}
		default:
			var e error
			boards, e = tx.BoardsOfHuman(p.Human.ID)
			if e != nil {
				return e
			}
		}
		for _, b := range boards {
			if lifecycleOf(b) == LifecycleDeleted {
				continue
			}
			_, me, e := s.access(tx, p, b.Name)
			if e != nil {
				return e
			}
			var taskID string
			if f.Task != "" {
				t, e := findTask(tx, b, f.Task)
				if e != nil {
					if f.Board == "" {
						continue
					}
					return e
				}
				taskID = t.ID
			}
			msgs, e := tx.Asks(b.ID)
			if e != nil {
				return e
			}
			if e := s.annotate(tx, b, me, msgs); e != nil {
				return e
			}
			for _, m := range msgs {
				if !rules.CanRead(b.Policy, m.To, m.SenderID, me.Rules()) {
					continue
				}
				a := m.Ask
				if f.ToMe && a.To != me.ID || f.FromMe && m.SenderID != me.ID || taskID != "" && (a.TaskID == nil || *a.TaskID != taskID) {
					continue
				}
				state := f.State
				if state == "" {
					state = "open"
				}
				if state != "all" && a.State != state {
					continue
				}
				out.Asks = append(out.Asks, AskItem{b, me, m})
			}
		}
		sort.SliceStable(out.Asks, func(i, j int) bool {
			a, b := out.Asks[i].Message, out.Asks[j].Message
			if f.ToMe && (f.State == "open" || f.State == "") && a.Ask.Blocking != b.Ask.Blocking {
				return a.Ask.Blocking
			}
			if a.At == b.At {
				return a.Seq > b.Seq
			}
			return a.At > b.At
		})
		limit := f.Limit
		if limit <= 0 {
			limit = 100
		}
		if len(out.Asks) > limit {
			out.More = true
			out.Asks = out.Asks[:limit]
		}
		return nil
	})
	return out, e
}

// AskCounts reports only the current member's open asks.
type AskCounts struct {
	Blocking  int64 `json:"blocking"`
	GoingWith int64 `json:"going_with"`
}

func (s *Service) askCounts(tx ReadTx, b Board, me Member) (AskCounts, error) {
	out := AskCounts{}
	ms, e := tx.Asks(b.ID)
	if e != nil {
		return out, e
	}
	for _, m := range ms {
		if m.Ask.To != me.ID {
			continue
		}
		if e := projectAsk(tx, me, &m, s.clk.Now()); e != nil {
			return out, e
		}
		if m.Ask.State != "open" {
			continue
		}
		if m.Ask.Blocking {
			out.Blocking++
		} else {
			out.GoingWith++
		}
	}
	return out, nil
}

func (s *Service) attentionOf(tx ReadTx, b Board, me Member, v *View) error {
	n, e := tx.CountNeedsReply(me)
	if e != nil {
		return e
	}
	counts, e := s.askCounts(tx, b, me)
	if e != nil {
		return e
	}
	n += counts.Blocking + counts.GoingWith
	v.NeedsReply = &n
	v.AsksToMe = &counts
	return nil
}
