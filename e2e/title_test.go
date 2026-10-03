//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// A board made with a title shows it beside its name; its admin changes or removes the
// title from a terminal, and the board's address stays its name throughout.
func TestBoardTitleFromPairAndBoardTitle(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pair := e.run("pair", "--title", "Payments retry design")
	if !strings.Contains(pair.stdout, "Created board general (Payments retry design)") {
		t.Fatalf("pair doesn't show the title:\n%s", pair)
	}
	if got := field(t, e.getAsOwner("/v1/boards/general"), "title"); got != "Payments retry design" {
		t.Fatalf("title after pair = %v", got)
	}

	r := e.run("board", "title", "Retry", "design", "v2", "--json").json(t)
	if field(t, r, "board") != "general" || field(t, r, "before") != "Payments retry design" || field(t, r, "after") != "Retry design v2" {
		t.Fatalf("board title --json = %v", r)
	}
	if text := e.run("board", "title", ""); !strings.Contains(text.stdout, "Board general has no title now.") {
		t.Fatalf("removing the title:\n%s", text)
	}
	if got := field(t, e.getAsOwner("/v1/boards/general"), "title"); got != nil {
		t.Fatalf("title after removing = %v", got)
	}

	if r := e.runExit("board", "title", "--json"); r.code != 2 || field(t, r.json(t), "error.code") != "invalid_request" {
		t.Fatalf("board title without text should be a usage error\n%s", r)
	}
	long := strings.Repeat("x", 81)
	if r := e.runExit("board", "title", long, "--json"); r.code != 1 || field(t, r.json(t), "error.code") != "invalid_request" {
		t.Fatalf("an 81-character title should be refused\n%s", r)
	}
}
