//go:build live

package live

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// claudeSubagentSetup records what Claude Code sends inside a subagent, for
// TestSubagentCannotActAsItsParent: it returns what to log once the subagent ran, and
// fails the test if no aboard command ran with agent_id in its hook input.
func claudeSubagentSetup(l *lab, project string) func() {
	inputs := l.recordHooks(project)
	return func() {
		t := l.t
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
		t.Logf("SubagentStart fired %d times, SubagentStop %d", subagentStarts, subagentStops)
		if aboardCalls == 0 {
			t.Fatalf("no aboard command ran in a subagent with agent_id in its hook input (inputs in %s)", inputs)
		}
	}
}
