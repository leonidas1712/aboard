//go:build e2e

package e2e

import (
	"testing"
)

// The team mode page's board lifecycle, from the CLI: the creator archives a board,
// aboard boards leaves it out and --archived lists it, restore makes it active again,
// and delete works only on an archived board, with --yes outside a terminal.
func TestBoardLifecycleFromTheCLI(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	maya := tm.person("maya")
	board := tm.newBoard(maya, "open")
	other := tm.newBoard(maya, "open")
	tm.link(maya, other)

	archived := maya.run("board", "archive", board, "--json").json(t)
	matchesCLISpec(t, "BoardLifecycleOutput", archived)
	if archived["lifecycle"] != "archived" || archived["changed"] != true {
		t.Fatalf("archive: %v", archived)
	}
	expectLines(t, maya.run("board", "archive", board),
		board+" is already archived; restore with: aboard board restore "+board+".")

	list := maya.run("boards", "--json").json(t)
	for _, b := range list["boards"].([]any) {
		if b.(map[string]any)["name"] == board {
			t.Fatalf("boards lists the archived board: %v", list)
		}
	}
	if list["archived_count"] != float64(1) {
		t.Fatalf("boards' archived count: %v", list)
	}
	only := maya.run("boards", "--archived", "--json").json(t)
	matchesCLISpec(t, "BoardsOutput", only)
	if rows := only["boards"].([]any); len(rows) != 1 || field(t, only, "boards.0.name") != board {
		t.Fatalf("boards --archived: %v", only)
	}

	expectLines(t, maya.run("board", "restore", board),
		"Restored "+board+". New messages and joins work again.")

	if r := maya.runExit("board", "delete", board, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "confirmation_required" {
		t.Fatalf("delete without --yes:\n%s", r)
	}
	if r := maya.runExit("board", "delete", board, "--yes", "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_not_archived" {
		t.Fatalf("delete of an active board:\n%s", r)
	}
	maya.run("board", "archive", board)
	expectLines(t, maya.run("board", "delete", board, "--yes"),
		"Deleted "+board+". Its record is kept; nobody can open it again.")
	if r := maya.runExit("board", "restore", board, "--json"); r.code != 1 || errorCode(t, r.json(t)) != "board_not_found" {
		t.Fatalf("restore after delete:\n%s", r)
	}
}
