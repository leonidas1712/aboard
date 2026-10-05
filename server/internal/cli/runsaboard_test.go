package cli

import "testing"

// A subagent's command is rewritten only when it runs aboard, so commands that merely
// pass through a folder called aboard keep matching the person's allow rules.
func TestRunsAboard(t *testing.T) {
	for _, c := range []struct {
		command string
		runs    bool
	}{
		{"aboard status --json", true},
		{"  aboard read", true},
		{"cd /tmp && aboard say hi", true},
		{"git log | aboard say -", true},
		{"ABOARD_AGENT=writer aboard inbox", true},
		{"env FOO=1 aboard inbox", true},
		{"/Users/alex/go/bin/aboard status", true},
		{"echo $(aboard status --json)", true},
		{"(aboard read)", true},
		{"true;aboard read", true},
		{"cd /Users/alex/aboard && git status", false},
		{"cat /Users/alex/aboard/README.md", false},
		{"go test ./... # in aboard", false},
		{"grep -r aboard .", false},
		{"ls aboard-notes", false},
		{"aboardx status", false},
		{"git status", false},
	} {
		if got := runsAboard.MatchString(c.command); got != c.runs {
			t.Errorf("runsAboard(%q) = %v; want %v", c.command, got, c.runs)
		}
	}
}
