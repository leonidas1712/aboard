package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

type taskNudgeSeat struct {
	Brief  map[string]int       `json:"brief,omitempty"`
	At     map[string]time.Time `json:"at"`
	Starts int                  `json:"starts"`
	Stands map[string]int       `json:"stands"`
}
type taskNudgeHistory struct {
	Seats map[string]taskNudgeSeat `json:"seats"`
}

func taskWork(w *api.AgentWork) deliverytext.TaskWork {
	if w == nil {
		return deliverytext.TaskWork{}
	}
	out := deliverytext.TaskWork{OpenTasks: w.OpenTasks, PostsWithoutTask: w.PostsWithoutTask, Nudges: w.Nudges, AsksWaiting: w.AsksWaiting}
	if w.AsksToIt != nil {
		out.AsksToIt = *w.AsksToIt
	}
	if w.OldestOpen != nil {
		out.OldestOpen = &deliverytext.TaskRef{ID: w.OldestOpen.Id, Ref: w.OldestOpen.Ref, Title: w.OldestOpen.Title}
	}
	if t := w.CurrentTask; t != nil {
		out.CurrentTask = &deliverytext.TaskContext{TaskRef: deliverytext.TaskRef{ID: t.Id, Ref: t.Ref, Title: t.Title}, Owner: t.Owner}
		if t.Stands != nil {
			out.CurrentTask.Stands = &deliverytext.TaskStands{Text: t.Stands.Text, By: t.Stands.By.Name, At: t.Stands.At, Version: t.Stands.Version, MessagesSince: func() int {
				if t.Stands.MessagesSince != nil {
					return *t.Stands.MessagesSince
				}
				return 0
			}()}
		}
	}
	return out
}

func (c *client) taskInbox(ctx context.Context) *api.Inbox {
	r, err := c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{Limit: ptrTo(1)})
	if err != nil || r.JSON200 == nil {
		return nil
	}
	return r.JSON200
}

// taskNudges uses server-visible facts; history only limits advice already reaching
// the seat. It cannot change a cursor, post an event or wake a session.
func (a *app) taskNudges(ctx context.Context, c *client, ref delivery.AgentRef, in *api.Inbox, trigger string, qualified bool, posted ...*api.Message) []deliverytext.Nudge {
	out := []deliverytext.Nudge{}
	if in == nil || in.Work == nil {
		return out
	}
	work := taskWork(in.Work)
	aboutCurrent := false
	if trigger == "say" && work.CurrentTask != nil && len(posted) > 0 && posted[0].About != nil {
		for _, tag := range *posted[0].About {
			if tag.Id == work.CurrentTask.ID {
				aboutCurrent = true
				break
			}
		}
	}
	if trigger == "say" && aboutCurrent && work.CurrentTask.Stands == nil {
		r, err := c.api.GetTaskWithResponse(ctx, ref.Board, work.CurrentTask.ID)
		if err == nil && r.JSON200 != nil {
			work.CurrentTask.MessageCount = r.JSON200.MessageCount
		}
	}
	dc := deliverytext.Context{BoardQualified: qualified}
	var candidates []*deliverytext.Nudge
	switch trigger {
	case "inbox", "status":
		candidates = append(candidates, deliverytext.TasksNotPickedUp(work, ref.Board, dc))
	case "ask":
		if len(posted) > 0 {
			candidates = append(candidates, deliverytext.AskOptions(work, textMessage(*posted[0])))
		}
	case "say":
		if len(posted) > 0 {
			askWork := work
			if work.CurrentTask != nil && work.AsksWaiting > 0 {
				asks, e := c.api.ListAsksWithResponse(ctx, &api.ListAsksParams{Board: &ref.Board, FromMe: ptrTo(true), State: ptrTo(api.ListAsksParamsStateOpen), Task: &work.CurrentTask.ID})
				if e == nil && asks.JSON200 != nil {
					askWork.AsksWaiting = len(asks.JSON200.Asks)
				}
			}
			candidates = append(candidates, deliverytext.AskInstead(askWork, textMessage(*posted[0]), ref.Board, dc))
		}
		candidates = append(candidates, deliverytext.NoTaskPosts(work, ref.Board, dc))
		if aboutCurrent {
			candidates = append(candidates, deliverytext.StandsStale(work, time.Now(), ref.Board, dc))
		}
	case "start", "new":
		if work.CurrentTask != nil {
			candidates = append(candidates, deliverytext.FirstTask(work.CurrentTask.Ref, work.Nudges))
		}
	case "done":
		candidates = append(candidates, deliverytext.NextTask(work))
	}
	if trigger == "status" || trigger == "done" {
		for _, n := range candidates {
			if n != nil {
				out = append(out, *n)
			}
		}
		return out
	}
	anyAdvice := false
	for _, n := range candidates {
		if n != nil {
			anyAdvice = true
		}
	}
	if !anyAdvice && trigger != "start" && trigger != "new" {
		return out
	}
	id := ref.MemberID
	if in.MemberId != nil {
		id = *in.MemberId
	}
	if id == "" {
		return out
	}
	p, err := a.paths()
	if err != nil {
		return out
	}
	var history taskNudgeHistory
	err = updateJSONFile(filepath.Join(p.state, "nudges.json"), &history, func() error {
		if history.Seats == nil {
			history.Seats = map[string]taskNudgeSeat{}
		}
		key := ref.Server + "\n" + id
		seat := history.Seats[key]
		if seat.At == nil {
			seat.At = map[string]time.Time{}
		}
		if seat.Stands == nil {
			seat.Stands = map[string]int{}
		}
		now := time.Now()
		first := trigger == "start" || trigger == "new"
		if first && work.CurrentTask != nil {
			seat.Starts++
		}
		for _, n := range candidates {
			if n == nil {
				continue
			}
			switch n.Code {
			case "first_task":
				if seat.Starts > 3 {
					continue
				}
			case "stands_stale":
				t := work.CurrentTask
				version, count := 0, t.MessageCount
				if t.Stands != nil {
					version, count = t.Stands.Version, t.Stands.MessagesSince
				}
				k := t.ID + "\n" + strconv.Itoa(version)
				if count-seat.Stands[k] < 8 {
					continue
				}
				seat.Stands[k] = count
			default:
				if at := seat.At[n.Code]; !at.IsZero() && now.Sub(at) < nudgeInterval(n.Code) {
					continue
				}
				seat.At[n.Code] = now
			}
			out = append(out, *n)
		}
		history.Seats[key] = seat
		return nil
	})
	if err != nil {
		return []deliverytext.Nudge{}
	}
	return out
}

func nudgesText(ns []deliverytext.Nudge) string {
	var text strings.Builder
	for _, n := range ns {
		text.WriteString(n.Text)
		text.WriteByte('\n')
	}
	return text.String()
}

func nudgeInterval(code string) time.Duration {
	switch code {
	case "ask_options":
		return 24 * time.Hour
	case "ask_instead":
		return time.Hour
	default:
		return 30 * time.Minute
	}
}

func (a *app) allowBriefNudge(ref delivery.AgentRef, b deliverytext.BriefContext) bool {
	if ref.MemberID == "" || b.FileID == "" {
		return false
	}
	p, err := a.paths()
	if err != nil {
		return false
	}
	allowed := false
	var history taskNudgeHistory
	err = updateJSONFile(filepath.Join(p.state, "nudges.json"), &history, func() error {
		if history.Seats == nil {
			history.Seats = map[string]taskNudgeSeat{}
		}
		key := ref.Server + "\n" + ref.MemberID
		seat := history.Seats[key]
		if seat.Brief == nil {
			seat.Brief = map[string]int{}
		}
		version := b.FileID + "\n" + strconv.Itoa(b.Version)
		progress := max(b.MessagesSince/30, b.TasksDoneSince/3)
		if progress > seat.Brief[version] {
			seat.Brief[version] = progress
			allowed = true
		}
		history.Seats[key] = seat
		return nil
	})
	return err == nil && allowed
}
