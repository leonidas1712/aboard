//go:build e2e

package e2e

import "testing"

func TestTaskListDefaultsToActiveWorkForPeopleAndAgents(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	board := tm.newBoard(tm.admin, "open")
	tm.link(tm.admin, board)
	agent := tm.admin.claudeSession("s-task-list-active")
	agent.run("join", "--board", board, "--server", tm.url(), "--json")
	refs := make([]string, 4)
	for i, title := range []string{"Still open", "Under way", "Finished", "No longer needed"} {
		created := tm.admin.run("task", "new", title, "--json").json(t)
		refs[i] = field(t, created, "task.ref").(string)
	}
	agent.run("task", "start", refs[1], "--json")
	tm.admin.run("task", "done", "Completed", "--task", refs[2], "--json")
	tm.admin.run("task", "done", "Cancelled", "--task", refs[3], "--cancelled", "--json")
	check := func(out map[string]any, all bool) {
		t.Helper()
		matchesCLISpec(t, "TaskListOutput", out)
		tasks := field(t, out, "tasks").([]any)
		want := 2
		if all {
			want = 4
		}
		if len(tasks) != want {
			t.Fatalf("task list returned %d tasks, want %d: %v", len(tasks), want, out)
		}
		for i, state := range []string{"open", "in_progress", "done", "cancelled"}[:want] {
			task := tasks[i].(map[string]any)
			if task["ref"] != refs[i] || task["state"] != state {
				t.Fatalf("task %d = %v, want %s in %s", i, task, refs[i], state)
			}
		}
		for _, state := range []string{"open", "in_progress", "done", "cancelled"} {
			if field(t, out, "counts."+state) != float64(1) {
				t.Fatalf("counts lost filtered %s tasks: %v", state, out)
			}
		}
	}
	check(tm.admin.run("task", "list", "--json").json(t), false)
	check(agent.run("task", "list", "--json").json(t), false)
	check(tm.admin.run("task", "list", "--all", "--json").json(t), true)
	check(agent.run("task", "list", "--all", "--json").json(t), true)
}
