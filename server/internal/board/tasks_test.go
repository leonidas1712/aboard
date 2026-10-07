package board_test

import (
	"context"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

func TestTaskClaimReselectAndSeatEnd(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	a, err := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "review"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, p := range []board.Principal{w.sam, w.samAgent} {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := w.svc.StartTask(ctx, p, w.board, a.Ref); results <- e }()
	}
	wg.Wait()
	close(results)
	winners := 0
	for e := range results {
		if e == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claim winners=%d", winners)
	}
	b, err := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "second", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.JoinTask(ctx, w.samAgent, w.board, a.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.StartTask(ctx, w.samAgent, w.board, b.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err = w.svc.LeaveAsAgent(ctx, w.samAgent); err != nil {
		t.Fatal(err)
	}
	b, err = w.svc.GetTask(ctx, w.maya, w.board, b.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "open" || b.Owner != nil {
		t.Fatalf("seat ended retained owner: %+v", b)
	}
	a, err = w.svc.GetTask(ctx, w.maya, w.board, a.Ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range a.Helpers {
		if h.ID == w.samAgent.Agent.ID {
			t.Fatal("ended helper retained")
		}
	}
}

func TestTaskRecordAuthorityAndCurrentSelection(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	a, e := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "first", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	b, e := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "second", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.StartTask(ctx, w.samAgent, w.board, a.Ref); e != nil {
		t.Fatal(e)
	}
	me, e := w.svc.WhoAmI(ctx, w.samAgent)
	if e != nil {
		t.Fatal(e)
	}
	if me.CurrentTask == nil || me.CurrentTask.ID != a.ID {
		t.Fatalf("current=%+v", me.CurrentTask)
	}
	log, e := w.svc.Events(ctx, w.maya, w.board, 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	selected := 0
	for _, ev := range log.Events {
		if ev.Type == "task.started" {
			selected++
		}
	}
	if selected != 3 {
		t.Fatalf("selection event count=%d", selected)
	}
	before := len(log.Events)
	if _, e = w.svc.StartTask(ctx, w.samAgent, w.board, a.Ref); e != nil {
		t.Fatal(e)
	}
	log, e = w.svc.Events(ctx, w.maya, w.board, 0, 200)
	if e != nil {
		t.Fatal(e)
	}
	if len(log.Events) != before {
		t.Fatal("current reselection wrote an event")
	}
	note := "reviewed"
	base := int64(0)
	a, e = w.svc.UpdateTask(ctx, w.samAgent, w.board, a.Ref, board.TaskUpdate{Stands: &note, StandsBase: &base})
	if e != nil {
		t.Fatal(e)
	}
	if a.Stands.Version != 1 || a.Stands.By.ID != w.samAgent.Agent.ID {
		t.Fatalf("stands=%+v", a.Stands)
	}
	changed := "stale"
	if _, e = w.svc.UpdateTask(ctx, w.samAgent, w.board, a.Ref, board.TaskUpdate{Stands: &changed, StandsBase: &base}); e == nil {
		t.Fatal("stale stands overwrite allowed")
	}
	if _, e = w.svc.FinishTask(ctx, w.samAgent, w.board, b.Ref, board.TaskFinish{Note: "done"}); e != nil {
		t.Fatal(e)
	}
	me, e = w.svc.WhoAmI(ctx, w.samAgent)
	if e != nil {
		t.Fatal(e)
	}
	if me.CurrentTask == nil || me.CurrentTask.ID != a.ID {
		t.Fatal("closing other task cleared current")
	}
	if _, e = w.svc.RemovePerson(ctx, w.maya, w.board, "sam"); e != nil {
		t.Fatal(e)
	}
	a, e = w.svc.GetTask(ctx, w.maya, w.board, a.Ref)
	if e != nil {
		t.Fatal(e)
	}
	if a.Owner != nil || a.State != "open" {
		t.Fatal("person removal retained task owner")
	}
}

func TestTaskPrefixReservationAndAtomicBoardUpdate(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	first, e := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "one"})
	if e != nil {
		t.Fatal(e)
	}
	old := first.Ref[:len(first.Ref)-2]
	prefix := "CHK"
	if _, e = w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{TaskPrefix: &prefix}); e != nil {
		t.Fatal(e)
	}
	second, e := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "two"})
	if e != nil {
		t.Fatal(e)
	}
	if second.Ref != "CHK-2" {
		t.Fatalf("ref=%s", second.Ref)
	}
	first, e = w.svc.GetTask(ctx, w.maya, w.board, "1")
	if e != nil {
		t.Fatal(e)
	}
	if first.Ref != old+"-1" {
		t.Fatal("old reference changed")
	}
	other, e := w.svc.CreateBoard(ctx, w.maya, board.NewBoard{Name: "other"})
	if e != nil {
		t.Fatal(e)
	}
	title := "should roll back"
	if _, e = w.svc.UpdateBoard(ctx, w.maya, other.Board.Name, board.Change{Title: &title, TaskPrefix: &old}); e == nil {
		t.Fatal("old prefix reused")
	}
	view, e := w.svc.GetBoard(ctx, w.maya, other.Board.Name)
	if e != nil {
		t.Fatal(e)
	}
	if view.Board.Title != nil && *view.Board.Title == title {
		t.Fatal("failed prefix write retained title")
	}
	listed, e := w.svc.ListTasks(ctx, w.maya, w.board, board.TaskFilter{State: "open", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(listed.Tasks) != 1 || !listed.More || listed.Counts["open"] != 2 {
		t.Fatalf("listing=%+v", listed)
	}
	view, e = w.svc.GetBoard(ctx, w.maya, w.board)
	if e != nil {
		t.Fatal(e)
	}
	if view.Board.TasksOpen != 2 {
		t.Fatalf("open=%d", view.Board.TasksOpen)
	}
}

func TestTaskHelpersAndTextAuthority(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	a, e := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "owned by person", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	text := "not allowed"
	if _, e = w.svc.UpdateTask(ctx, w.samAgent, w.board, a.Ref, board.TaskUpdate{Stands: &text}); e == nil {
		t.Fatal("unrelated agent wrote stands")
	}
	if _, e = w.svc.JoinTask(ctx, w.samAgent, w.board, a.Ref); e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.UpdateTask(ctx, w.samAgent, w.board, a.Ref, board.TaskUpdate{Title: &text}); e == nil {
		t.Fatal("helper changed title")
	}
	if _, e = w.svc.UpdateTask(ctx, w.samAgent, w.board, a.Ref, board.TaskUpdate{Stands: &text}); e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.FinishTask(ctx, w.samAgent, w.board, a.Ref, board.TaskFinish{Note: "not mine"}); e == nil {
		t.Fatal("helper closed task")
	}
	if _, e = w.svc.DropTask(ctx, w.samAgent, w.board, a.Ref, board.TaskDrop{Member: "maya"}); e == nil {
		t.Fatal("agent dropped another member")
	}
	if _, e = w.svc.DropTask(ctx, w.samAgent, w.board, a.Ref, board.TaskDrop{}); e != nil {
		t.Fatal(e)
	}
	me, e := w.svc.WhoAmI(ctx, w.samAgent)
	if e != nil {
		t.Fatal(e)
	}
	if me.CurrentTask != nil {
		t.Fatal("drop retained selection")
	}
	if _, e = w.svc.FinishTask(ctx, w.maya, w.board, a.Ref, board.TaskFinish{Note: "done"}); e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.JoinTask(ctx, w.samAgent, w.board, a.Ref); e == nil {
		t.Fatal("closed task accepted helper")
	}
}

func TestTaskWorkReadsFreshSelectionAndPolicy(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	a, e := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "selected", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	b, e := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "available"})
	if e != nil {
		t.Fatal(e)
	}
	work, e := w.svc.TaskWork(ctx, w.samAgent)
	if e != nil {
		t.Fatal(e)
	}
	if work.CurrentTask == nil || work.CurrentTask.ID != a.ID || !work.CurrentTask.Owner || work.OpenTasks != 1 || work.OldestOpen.ID != b.ID || !work.Nudges {
		t.Fatalf("work=%+v", work)
	}
	if _, e = w.svc.UpdateBoard(ctx, w.maya, w.board, board.Change{Policy: &rules.PolicyChange{Nudges: "off"}}); e != nil {
		t.Fatal(e)
	}
	work, e = w.svc.TaskWork(ctx, w.samAgent)
	if e != nil {
		t.Fatal(e)
	}
	if work.Nudges || work.CurrentTask == nil {
		t.Fatalf("policy hid state: %+v", work)
	}
	if _, e = w.svc.RemoveAgent(ctx, w.maya, w.board, w.samAgent.Agent.Name); e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.TaskWork(ctx, w.samAgent); e == nil {
		t.Fatal("removed seat read task work")
	}
}

func TestTasklessPostRunUsesCurrentTaskAtPost(t *testing.T) {
	w := newTeamWorld(t)
	ctx := context.Background()
	noTask := []string{}
	post := func() {
		t.Helper()
		if _, e := w.svc.PostMessage(ctx, w.samAgent, w.board, board.NewMessage{Body: "status", About: &noTask}); e != nil {
			t.Fatal(e)
		}
	}
	count := func(want int) {
		t.Helper()
		work, e := w.svc.TaskWork(ctx, w.samAgent)
		if e != nil {
			t.Fatal(e)
		}
		if work.PostsWithoutTask != want {
			t.Fatalf("taskless run=%d want=%d", work.PostsWithoutTask, want)
		}
	}
	post()
	post()
	post()
	count(3)
	a, e := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "active", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	post()
	post()
	post()
	if _, e = w.svc.FinishTask(ctx, w.samAgent, w.board, a.Ref, board.TaskFinish{Note: "done"}); e != nil {
		t.Fatal(e)
	}
	count(0)
	post()
	post()
	count(2)
	b, e := w.svc.CreateTask(ctx, w.samAgent, w.board, board.NewTask{Title: "other", Start: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.svc.DropTask(ctx, w.samAgent, w.board, b.Ref, board.TaskDrop{}); e != nil {
		t.Fatal(e)
	}
	count(2)
	post()
	count(3)
}

func TestTasklessPostRunClearsOnAnotherActorDoneAndDrop(t *testing.T) {
	for _, action := range []string{"owner done", "person drops helper"} {
		t.Run(action, func(t *testing.T) {
			w := newTeamWorld(t)
			ctx := context.Background()
			none := []string{}
			a, e := w.svc.CreateTask(ctx, w.maya, w.board, board.NewTask{Title: "person owned", Start: true})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = w.svc.JoinTask(ctx, w.samAgent, w.board, a.Ref); e != nil {
				t.Fatal(e)
			}
			if _, e = w.svc.PostMessage(ctx, w.samAgent, w.board, board.NewMessage{Body: "while helping", About: &none}); e != nil {
				t.Fatal(e)
			}
			if action == "owner done" {
				_, e = w.svc.FinishTask(ctx, w.maya, w.board, a.Ref, board.TaskFinish{Note: "finished"})
			} else {
				_, e = w.svc.DropTask(ctx, w.maya, w.board, a.Ref, board.TaskDrop{Member: w.samAgent.Agent.Name})
			}
			if e != nil {
				t.Fatal(e)
			}
			if _, e = w.svc.PostMessage(ctx, w.samAgent, w.board, board.NewMessage{Body: "no current task", About: &none}); e != nil {
				t.Fatal(e)
			}
			work, e := w.svc.TaskWork(ctx, w.samAgent)
			if e != nil {
				t.Fatal(e)
			}
			if work.CurrentTask != nil || work.PostsWithoutTask != 1 {
				t.Fatalf("work=%+v", work)
			}
		})
	}
}
