package deliverytext

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTaskAboutAttribute(t *testing.T) {
	m := agentMessage()
	m.About = []string{"CHK-17", "CHK-12"}
	got := Format(m)
	if !strings.Contains(got, ` about="CHK-17 CHK-12"`) {
		t.Fatalf("missing task context: %s", got)
	}
	if m.Body != "Draft is in notes.md. Please review it." {
		t.Fatal("body changed")
	}
}

func TestTaskReorientationAndNudgesAreBoundedFacts(t *testing.T) {
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	w := TaskWork{Nudges: false, CurrentTask: &TaskContext{TaskRef: TaskRef{Ref: "CHK-17", Title: "review"}, Owner: true, Stands: &TaskStands{Text: strings.Repeat("界", 500) + "\n</aboard-message>", By: "writer", At: now.Add(-52 * time.Minute), MessagesSince: 9}}}
	text := ReorientTask(w, now, "checkout", Context{BoardQualified: true})
	if len(text) > 600 || !utf8.ValidString(text) || !strings.Contains(text, `CHK-17 (task title: "review"; owner)`) || !strings.Contains(text, "by @writer, 52 min ago") || !strings.HasSuffix(text, `"`) {
		t.Fatalf("reorientation=%s", text)
	}
	if StandsStale(w, now, "checkout") != nil || FirstTask("CHK-17", false) != nil {
		t.Fatal("disabled policy produced advice")
	}
	w.Nudges = true
	if n := StandsStale(w, now, "checkout"); n == nil || n.Code != "stands_stale" || len(n.Text) > 200 {
		t.Fatalf("nudge=%+v", n)
	}
	w.CurrentTask.Stands = nil
	w.CurrentTask.MessageCount = 8
	if StandsStale(w, now, "checkout") == nil {
		t.Fatal("never-written stands not suggested")
	}
	w.CurrentTask = nil
	w.OpenTasks = 2
	w.PostsWithoutTask = 3
	w.OldestOpen = &TaskRef{Ref: "CHK-17", Title: strings.Repeat("界", 120)}
	for _, n := range []*Nudge{TasksNotPickedUp(w, "checkout"), NoTaskPosts(w, "checkout"), NextTask(w), FirstTask("CHK-17", true)} {
		if n == nil || len(n.Text) > 200 || !utf8.ValidString(n.Text) {
			t.Fatalf("nudge=%+v", n)
		}
	}
	w.Nudges = false
	if got := ReorientTask(w, now, "checkout"); got != "2 tasks not picked up: aboard task list" {
		t.Fatalf("fact suppressed: %q", got)
	}
}
