//go:build live

package live

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// recordHooks adds hooks to a project's Claude Code settings, after Aboard's, that append
// each hook input of PreToolUse (Bash), SubagentStart and SubagentStop to a file in the
// lab, so the test sees what Claude Code sends inside a subagent. It returns the file.
func (l *lab) recordHooks(project string) string {
	l.t.Helper()
	log := filepath.Join(l.dir, "hook-inputs.jsonl")
	path := filepath.Join(project, ".claude", "settings.local.json")
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		l.t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		l.t.Fatal(err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	record := map[string]any{"type": "command", "command": "cat >> " + shellQuote(log) + " && echo >> " + shellQuote(log), "timeout": 10}
	for event, matcher := range map[string]string{"PreToolUse": "Bash", "SubagentStart": "", "SubagentStop": ""} {
		group := map[string]any{"hooks": []any{record}}
		if matcher != "" {
			group["matcher"] = matcher
		}
		groups, _ := hooks[event].([]any)
		hooks[event] = append(groups, group)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		l.t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		l.t.Fatal(err)
	}
	return log
}

// hookInput is the part of a recorded hook input the test reads.
type hookInput struct {
	Event     string `json:"hook_event_name"`
	AgentID   string `json:"agent_id"`
	AgentType string `json:"agent_type"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

func readHookInputs(t *testing.T, path string) []hookInput {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []hookInput
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var in hookInput
		if json.Unmarshal(sc.Bytes(), &in) == nil && in.Event != "" {
			out = append(out, in)
		}
	}
	return out
}

// A Claude Code session's subagent runs aboard in its parent's session, so without a
// mark it would act as the parent. Its Bash hook input carries agent_id, Aboard's
// pre-tool hook marks the command, and aboard refuses to post: nothing reaches the
// board, and the refusal is in the subagent's transcript. The test also records what
// Claude Code sent (agent_id, SubagentStart and SubagentStop) and whether it asked
// before the marked command ran.
func TestClaudeSubagentCannotActAsItsParent(t *testing.T) {
	requireClaude(t)
	t.Parallel()
	l := newLab(t)
	l.pairCLI()
	proj := l.project("project", "claude-code")
	inputs := l.recordHooks(proj)
	writer := l.startClaude("writer", proj)
	writer.bind("writer")

	start := time.Now()
	writer.submit("Use your Agent tool to start one general-purpose subagent with this task: \"Run the shell command " +
		"`aboard status --json`, then run `aboard say --to @reviewer 'subagent was here'`, and report the exact output " +
		"of both.\" Don't run either command yourself. When the subagent has reported, reply only DONE.")
	asked := 0
	var since time.Time
	writer.l.waitFor(6*time.Minute, "the writer's turn with its subagent to end", func() bool {
		s := writer.screen()
		if strings.Contains(s, "Do you want to proceed?") {
			asked++
			writer.keys("Enter") // the default answer, Yes
			since = time.Time{}
			return false
		}
		if !writer.idle() {
			since = time.Time{}
			return false
		}
		if since.IsZero() {
			since = time.Now()
		}
		// Claude Code may run the subagent in the background and end the writer's turn
		// before it finishes, and SubagentStop can fire while it still runs, so the end
		// is the writer's DONE, which it sends only once the subagent has reported.
		return strings.Contains(s, "⏺ DONE") && time.Since(since) >= time.Second
	})

	var aboardCalls, subagentStarts, subagentStops int
	for _, in := range readHookInputs(t, inputs) {
		switch in.Event {
		case "PreToolUse":
			if strings.Contains(in.ToolInput.Command, "aboard") {
				t.Logf("PreToolUse for %q: agent_id %q, agent_type %q", in.ToolInput.Command, in.AgentID, in.AgentType)
				if in.AgentID != "" {
					aboardCalls++
				}
			}
		case "SubagentStart":
			subagentStarts++
		case "SubagentStop":
			subagentStops++
		}
	}
	t.Logf("SubagentStart fired %d times, SubagentStop %d; Claude Code asked before a command %d times", subagentStarts, subagentStops, asked)
	if aboardCalls == 0 {
		t.Fatalf("no aboard command ran in a subagent with agent_id in its hook input (inputs in %s)", inputs)
	}

	for _, m := range l.messages("reviewer") {
		if strings.Contains(m.Body, "subagent was here") {
			t.Fatalf("the subagent posted as %s: #%d %q", m.From.Name, m.Seq, m.Body)
		}
	}
	marked, refused, statusAsWriter := false, false, false
	for _, path := range l.claudeTranscripts() {
		raw, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			continue
		}
		text := string(raw)
		marked = marked || strings.Contains(text, "export ABOARD_SUBAGENT=")
		refused = refused || strings.Contains(text, "subagent_without_seat") || strings.Contains(text, "runs in a subagent")
		statusAsWriter = statusAsWriter || strings.Contains(text, `\"agent\": \"writer\"`)
	}
	t.Logf("in the transcripts: command marked %v, refusal %v, the subagent's status named writer %v (took %s)",
		marked, refused, statusAsWriter, time.Since(start).Round(time.Second))
	if !marked || !refused {
		t.Fatalf("the subagent's aboard command wasn't marked and refused (marked %v, refused %v)", marked, refused)
	}
}
