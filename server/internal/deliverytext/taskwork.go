package deliverytext

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// TaskRef is a task reference the server resolved for this seat.
type TaskRef struct{ ID, Ref, Title string }

// TaskStands is the latest recorded task note, with its age and visible message count.
type TaskStands struct {
	Text                   string
	At                     time.Time
	Version, MessagesSince int
}

// TaskContext names an agent's current task and part in it.
type TaskContext struct {
	TaskRef
	Owner        bool
	MessageCount int
	Stands       *TaskStands
}

// TaskWork is the task part of an agent's inbox work, including its board's nudge policy.
type TaskWork struct {
	CurrentTask                 *TaskContext
	OpenTasks, PostsWithoutTask int
	OldestOpen                  *TaskRef
	Nudges                      bool
}

// Nudge is advice for output already reaching an agent. Callers enforce its rate limit.
type Nudge struct {
	Code string `json:"code"`
	Text string `json:"text"`
}

func boundedText(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	if n < 3 {
		return ""
	}
	s = s[:n-3]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}

func plainLine(s string) string {
	return strings.Join(strings.FieldsFunc(EscapeBody(s), func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }), " ")
}
func taskNudge(code, text string) *Nudge { return &Nudge{Code: code, Text: boundedText(text, 200)} }

// TasksNotPickedUp reports open work without selecting it.
func TasksNotPickedUp(w TaskWork, board string, contexts ...Context) *Nudge {
	if !w.Nudges || w.CurrentTask != nil || w.OpenTasks == 0 {
		return nil
	}
	return taskNudge("tasks_not_picked_up", fmt.Sprintf("%d tasks not picked up: %s", w.OpenTasks, boardCommand("aboard task list", board, contexts)))
}

// NoTaskPosts suggests recording work after consecutive taskless posts.
func NoTaskPosts(w TaskWork, board string, contexts ...Context) *Nudge {
	if !w.Nudges || w.CurrentTask != nil || w.PostsWithoutTask < 3 {
		return nil
	}
	command := boardCommand(`aboard task new "…"`, board, contexts)
	if w.OldestOpen != nil {
		command = "aboard task start " + w.OldestOpen.Ref + ", or open one: " + command
	}
	text := fmt.Sprintf("Tip: your last %d messages are about no task. Start one: %s", w.PostsWithoutTask, command)
	if len(text) > 200 {
		text = fmt.Sprintf("%d messages have no task: %s", w.PostsWithoutTask, command)
	}
	return taskNudge("no_task_posts", text)
}

// NextTask names available work after the agent closes a task.
func NextTask(w TaskWork) *Nudge {
	if !w.Nudges || w.OldestOpen == nil {
		return nil
	}
	t := w.OldestOpen
	command := " (aboard task start " + t.Ref + ")"
	prefix := "Next not picked up: " + t.Ref + " "
	return taskNudge("next_task", prefix+boundedText(plainLine(t.Title), 200-len(prefix)-len(command))+command)
}

// FirstTask explains message tagging when a seat first starts tasks.
func FirstTask(ref string, nudges bool) *Nudge {
	if !nudges {
		return nil
	}
	return taskNudge("first_task", "Your messages are about "+ref+" until it's done; add --task to say otherwise.")
}

// StandsStale suggests a note after eight visible messages and fifteen minutes.
func StandsStale(w TaskWork, now time.Time, board string, contexts ...Context) *Nudge {
	if !w.Nudges || w.CurrentTask == nil {
		return nil
	}
	t := w.CurrentTask
	count := t.MessageCount
	if t.Stands != nil {
		count = t.Stands.MessagesSince
		if now.Sub(t.Stands.At) < 15*time.Minute {
			return nil
		}
	}
	if count < 8 {
		return nil
	}
	return taskNudge("stands_stale", fmt.Sprintf("%s · Where it stands is %d messages old: %s", t.Ref, count, boardCommand(`aboard task note "…"`, board, contexts)))
}

// ReorientTask reports state even when advice is disabled, without reading message bodies.
func ReorientTask(w TaskWork, now time.Time, board string, contexts ...Context) string {
	if w.CurrentTask == nil {
		if w.OpenTasks == 0 {
			return ""
		}
		return boundedText(fmt.Sprintf("%d tasks not picked up: %s", w.OpenTasks, boardCommand("aboard task list", board, contexts)), 600)
	}
	t := w.CurrentTask
	part := "helper"
	if t.Owner {
		part = "owner"
	}
	text := fmt.Sprintf("You're on %s %s (%s).", t.Ref, boundedText(plainLine(t.Title), 120), part)
	if t.Stands != nil {
		minutes := int(max(time.Duration(0), now.Sub(t.Stands.At)) / time.Minute)
		text += fmt.Sprintf(" Where it stands, %d min ago: %q", minutes, plainLine(t.Stands.Text))
	}
	if contextOf(contexts).BoardQualified {
		text = "Board " + board + ": " + text
	}
	return boundedText(text, 600)
}
