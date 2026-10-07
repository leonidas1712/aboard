//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// The harness gets a work request rather than CLI commands. Its installed skill must
// teach it to record the task, let the server tag its message, and close the task.
func TestTaskWorkflowFromSkill(t *testing.T) {
	eachHarness(t, "TaskWorkflowFromSkill", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		first := d.start(l, "first", l.project("first-project", d.p.Harness))
		second := d.start(l, "second", l.project("second-project", d.p.Harness))
		first.submit("Pair with another agent on Aboard.")
		var line string
		l.waitFor(3*time.Minute, "the first session to give a join line", func() bool { line = joinLine.FindString(first.screen()); return line != "" })
		first.waitIdle(2 * time.Minute)
		board := boardOf.FindStringSubmatch(line)[1]
		second.submit(line)
		var names []string
		l.waitFor(3*time.Minute, "the second session to join the board", func() bool { names = l.agents(board); return len(names) >= 2 })
		l.waitMessage(names[1], time.Time{}, "", 3*time.Minute)
		l.waitQuiet(4*time.Minute, names[1], first, second)

		const title = "Check the Aboard task workflow"
		const progress = "TASK-PROGRESS-7319"
		const finalNote = "The Aboard task workflow check is complete."
		first.submit(fmt.Sprintf("Use the Aboard skill to record a new task titled %q and take responsibility for it. While working on that task, send @%s an Aboard message whose entire text is %q; no reply is needed. Then mark that task done with final note %q. Do only this small task, and end your turn when it is recorded as done.", title, names[1], progress, finalNote))

		var task nativeTaskRecord
		var posted nativeTaskMessage
		l.waitFor(3*time.Minute, "a closed task and its automatically tagged progress message on the public API", func() bool {
			var tasks struct {
				Tasks []nativeTaskRecord `json:"tasks"`
			}
			l.ownerModeRequest(http.MethodGet, "/v1/boards/"+url.PathEscape(board)+"/tasks?state=all&limit=200", "", &tasks)
			for _, candidate := range tasks.Tasks {
				if candidate.Title == title && candidate.OpenedBy.Name == names[0] {
					task = candidate
					break
				}
			}
			if task.ID == "" || task.State != "done" {
				return false
			}
			var page struct {
				Messages []nativeTaskMessage `json:"messages"`
			}
			l.ownerModeRequest(http.MethodGet, "/v1/boards/"+url.PathEscape(board)+"/messages?limit=200", "", &page)
			for _, candidate := range page.Messages {
				if candidate.From.Name == names[0] && candidate.From.Kind == "agent" && candidate.Body == progress {
					posted = candidate
					break
				}
			}
			return posted.ID != ""
		})
		if task.Ref == "" || task.Owner == nil || task.Owner.Name != names[0] || task.Owner.Kind != "agent" || task.OpenedBy.Kind != "agent" || task.ClosedNote == nil || *task.ClosedNote != finalNote {
			t.Fatalf("task's recorded owner or final note is wrong: id=%s owner=%+v opener=%+v state=%s closed_note=%v", task.ID, task.Owner, task.OpenedBy, task.State, task.ClosedNote)
		}
		if posted.From.Name != names[0] || posted.From.Kind != "agent" || len(posted.About) != 1 || posted.About[0].ID != task.ID || posted.About[0].Ref != task.Ref || posted.About[0].How != "current" {
			t.Fatalf("message #%d has about=%+v; want only %s (%s), inferred from the sender's current task", posted.Seq, posted.About, task.ID, task.Ref)
		}
		// MemberRef intentionally has no permanent ID. The public record binds the
		// task opener, its owner selection and the progress post to the same seat.
		var createdID, startedID, postedID string
		for after := 0; ; {
			var page struct {
				Events []struct {
					Type  string `json:"type"`
					Actor struct {
						Kind     string `json:"kind"`
						MemberID string `json:"member_id"`
					} `json:"actor"`
					Data struct {
						TaskID    string `json:"task_id"`
						MemberID  string `json:"member_id"`
						MessageID string `json:"message_id"`
					} `json:"data"`
				} `json:"events"`
				NextAfter int `json:"next_after"`
			}
			l.ownerModeRequest(http.MethodGet, fmt.Sprintf("/v1/boards/%s/events?limit=200&after=%d", url.PathEscape(board), after), "", &page)
			for _, event := range page.Events {
				switch {
				case event.Type == "task.created" && event.Data.TaskID == task.ID && event.Actor.Kind == "agent":
					createdID = event.Actor.MemberID
				case event.Type == "task.started" && event.Data.TaskID == task.ID:
					startedID = event.Data.MemberID
				case event.Type == "message.posted" && event.Data.MessageID == posted.ID && event.Actor.Kind == "agent":
					postedID = event.Actor.MemberID
				}
			}
			if len(page.Events) == 0 || page.NextAfter <= after {
				break
			}
			after = page.NextAfter
		}
		if createdID == "" || startedID != createdID || postedID != createdID {
			t.Fatalf("task creation, owner selection and progress must bind to one permanent seat: created=%q started=%q posted=%q", createdID, startedID, postedID)
		}
		var members struct {
			Members []struct {
				ID          string          `json:"id"`
				CurrentTask json.RawMessage `json:"current_task"`
			} `json:"members"`
		}
		l.ownerModeRequest(http.MethodGet, "/v1/boards/"+url.PathEscape(board)+"/members", "", &members)
		found := false
		for _, member := range members.Members {
			if member.ID == createdID {
				found = true
				if string(member.CurrentTask) != "null" {
					t.Fatalf("finished task remained current, or current_task was omitted: %s", member.CurrentTask)
				}
			}
		}
		if !found {
			t.Fatalf("task opener %s is missing from the board", createdID)
		}
		first.waitIdle(2 * time.Minute)
	})
}

type (
	nativeTaskMember struct {
		Name string `json:"name"`
		Kind string `json:"kind"`
	}
	nativeTaskRecord struct {
		ID         string            `json:"id"`
		Ref        string            `json:"ref"`
		Title      string            `json:"title"`
		State      string            `json:"state"`
		Owner      *nativeTaskMember `json:"owner"`
		OpenedBy   nativeTaskMember  `json:"opened_by"`
		ClosedNote *string           `json:"closed_note"`
	}
)

type nativeTaskMessage struct {
	ID    string           `json:"id"`
	Seq   int              `json:"seq"`
	Body  string           `json:"body"`
	From  nativeTaskMember `json:"from"`
	About []struct {
		ID  string `json:"id"`
		Ref string `json:"ref"`
		How string `json:"how"`
	} `json:"about"`
}
