package support

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Status is how far a harness has a capability.
type Status string

// Statuses of a capability.
const (
	Supported     Status = "supported"
	Partial       Status = "partial"
	Unsupported   Status = "unsupported"
	NotApplicable Status = "n/a"
)

// Capability is one thing a harness is measured on.
type Capability struct {
	// ID names the capability in results and notes.
	ID string
	// Label heads its column in the README's table.
	Label string
	// Scenarios are the live scenarios that prove it. A capability with none is proven
	// by the fast kit alone.
	Scenarios []string
	// liveIf says whether the live scenarios apply to a harness; nil means they do.
	liveIf func(p Profile) bool
	// declare says what the profile declares, with a note when it isn't plainly yes.
	declare func(p Profile) (Status, string)
}

// Baseline is the capability every supported harness has: it joins a board with a join
// line, its session has an identity so commands act as its agent, it posts and reads,
// the skill is installed, aboard init and uninstall leave its files clean, and it has a
// docs page. A harness without it isn't supported.
const Baseline = "baseline"

// Capabilities are what the conformance kits measure, in the order the README's table
// shows them.
var Capabilities = []Capability{
	{ID: Baseline, Label: "Baseline", Scenarios: []string{"JoinsAndTalks"}, declare: func(p Profile) (Status, string) {
		switch p.Identity.Kind {
		case "env", "hook":
			return Supported, ""
		case "extension":
			return Supported, "identity comes from Aboard's extension"
		}
		return Unsupported, "the profile doesn't say where a session's id comes from"
	}},
	{
		ID: "idle_delivery", Label: "Wakes when idle", Scenarios: []string{"WakesAndReplies", "PingPong", "PingPongAcrossHarnesses", "RepliesReachPromptly", "KilledSessionRedelivers", "RestartsLoseNothing"},
		declare: func(p Profile) (Status, string) {
			switch {
			case p.Has("idle-hook"):
				return Supported, ""
			case p.Has("queue"), p.Has("extension"):
				return Supported, ""
			}
			return Unsupported, "the agent runs aboard inbox --wait"
		},
	},
	{ID: "turn_end", Label: "Peers at turn end", Scenarios: []string{"OwnerReachesBusy"}, declare: func(p Profile) (Status, string) {
		if p.Delivers() {
			return Supported, ""
		}
		return Unsupported, ""
	}},
	{ID: "owner_mid_turn", Label: "Owner mid-turn", Scenarios: []string{"OwnerReachesBusy"}, declare: func(p Profile) (Status, string) {
		if p.Has("tool-boundary") || p.Has("turn-start") || p.Has("extension") {
			return Supported, ""
		}
		return Unsupported, "the owner's messages wait for the turn's end"
	}},
	{ID: "waiting_notice", Label: "Waiting notice", Scenarios: []string{"PeerWaitsButNoticeArrives"}, declare: func(p Profile) (Status, string) {
		switch {
		case p.Delivery.WaitingNotice && !p.WaitsForIdle() && p.Has("queue"):
			return NotApplicable, "the harness's own queue takes peers' messages as they come, so none wait to be named"
		case p.Delivery.WaitingNotice:
			return Supported, ""
		}
		return Unsupported, ""
	}},
	{ID: "presence", Label: "Presence", Scenarios: []string{"WakesAndReplies"}, declare: func(p Profile) (Status, string) {
		_, prompt := p.Hook("prompt")
		_, end := p.Hook("end")
		_, wait := p.Hook("wait")
		_, turnEnd := p.Hook("turn-end")
		switch {
		case p.Has("extension"), prompt && end && (wait || turnEnd):
			return Supported, ""
		case end:
			return Partial, "shows connected and disconnected, not working and idle"
		}
		return Unsupported, ""
	}},
	{ID: "reconnect", Label: "Reconnects on resume", Scenarios: []string{"ResumeReconnects"}, declare: func(p Profile) (Status, string) {
		if !p.Delivers() {
			return NotApplicable, ""
		}
		if p.Lifecycle.ResumeKeepsID {
			return Supported, ""
		}
		return Unsupported, "a resumed session has a new id: run aboard resume in it"
	}},
	{ID: "subagents", Label: "Subagents", Scenarios: []string{"SubagentCannotActAsItsParent"}, declare: func(p Profile) (Status, string) {
		switch p.SubagentIdentity {
		case "marked":
			return Supported, "marked: a subagent's commands may only read"
		case "seats":
			return Supported, "a subagent can have a seat of its own"
		}
		return Unsupported, "a subagent's commands can't be told from its parent's"
	}},
	{ID: "project_scope", Label: "Project setup", Scenarios: []string{"ProjectScopeSetup"}, declare: func(p Profile) (Status, string) {
		for _, it := range p.Install {
			if (it.Kind == "skill" || it.Kind == "hooks" || it.Kind == "file") && it.Project == "" {
				return Partial, "its " + it.Kind + " goes only in the global config"
			}
		}
		return Supported, ""
	}},
	{ID: "launcher", Label: "Started by a launcher", declare: func(Profile) (Status, string) {
		return Unsupported, "no launcher starts sessions in this aboard"
	}},
	{
		ID: "sandbox", Label: "Sandbox check", Scenarios: []string{"SandboxNeedsTheAllowRule"},
		// Only a sandbox that blocks the network has a live scenario; detecting one that
		// doesn't is proven by the fast kit.
		liveIf: func(p Profile) bool { return len(p.SandboxNetworkEnv) > 0 },
		declare: func(p Profile) (Status, string) {
			switch {
			case len(p.SandboxNetworkEnv) > 0:
				return Supported, ""
			case len(p.SandboxEnv) > 0:
				return Supported, ""
			}
			return NotApplicable, ""
		},
	},
}

// Result is one live scenario's latest outcome for a harness.
type Result struct {
	// Result is pass, fail or n/a (the scenario doesn't apply to the harness).
	Result string `json:"result"`
	// Date is the day it ran, YYYY-MM-DD.
	Date string `json:"date,omitempty"`
	// Test is the test that produced it.
	Test string `json:"test"`
}

// HarnessResults are a harness's recorded live results.
type HarnessResults struct {
	// Version is the harness version the latest run used.
	Version string `json:"version,omitempty"`
	// Notes say more about a capability than its status, by capability ID; written by
	// hand, for what the profile can't say.
	Notes     map[string]string `json:"notes,omitempty"`
	Scenarios map[string]Result `json:"scenarios"`
}

// Results is the live suite's record, e2e/live/support.json.
type Results struct {
	Harnesses map[string]*HarnessResults `json:"harnesses"`
}

// ResultsFile is the record's path from the repository's root.
const ResultsFile = "e2e/live/support.json"

// LoadResults reads the record, or returns an empty one if there is none.
func LoadResults(path string) (*Results, error) {
	r := &Results{Harnesses: map[string]*HarnessResults{}}
	raw, err := os.ReadFile(filepath.Clean(path))
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, r); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if r.Harnesses == nil {
		r.Harnesses = map[string]*HarnessResults{}
	}
	return r, nil
}

// Save writes the record, indented, with a final newline.
func (r *Results) Save(path string) error {
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644) //nolint:gosec // a file in the repository
}

// Record sets a scenario's result for a harness.
func (r *Results) Record(harness, scenario string, res Result) {
	h := r.Harnesses[harness]
	if h == nil {
		h = &HarnessResults{}
		r.Harnesses[harness] = h
	}
	if h.Scenarios == nil {
		h.Scenarios = map[string]Result{}
	}
	h.Scenarios[scenario] = res
}

// Cell is one capability of one harness in the matrix.
type Cell struct {
	Capability Capability
	Status     Status
	Note       string
}

// Matrix is a harness's capabilities: what its profile declares, checked against its
// live results. A declared capability a live scenario measures counts only once that
// scenario passed; one that failed in the latest run is partial until it passes again.
func Matrix(p Profile, r *HarnessResults) []Cell {
	if r == nil {
		r = &HarnessResults{}
	}
	cells := make([]Cell, 0, len(Capabilities))
	for _, c := range Capabilities {
		status, note := c.declare(p)
		live := c.liveIf == nil || c.liveIf(p)
		if live && (status == Supported || status == Partial) {
			var passed, failed []string
			for _, s := range c.Scenarios {
				switch res := r.Scenarios[s]; res.Result {
				case "pass":
					passed = append(passed, s)
				case "fail":
					failed = append(failed, s+" "+res.Date)
				}
			}
			switch {
			case len(failed) > 0:
				status, note = Partial, join(note, "failed live: "+strings.Join(failed, ", "))
			case len(c.Scenarios) > 0 && len(passed) == 0:
				status, note = Partial, join(note, "not proven by the live kit")
			}
		}
		if n := r.Notes[c.ID]; n != "" {
			note = join(note, n)
		}
		cells = append(cells, Cell{Capability: c, Status: status, Note: note})
	}
	return cells
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// SupportedHarness reports whether a harness counts as supported: it has the baseline.
func SupportedHarness(cells []Cell) bool {
	for _, c := range cells {
		if c.Capability.ID == Baseline {
			return c.Status == Supported || c.Status == Partial
		}
	}
	return false
}

// symbol is how a status shows in the table.
func symbol(s Status) string {
	switch s {
	case Supported:
		return "✓"
	case Partial:
		return "partial"
	case NotApplicable:
		return "n/a"
	case Unsupported:
	}
	return "–"
}

// Table renders the README's harness table: one row per harness, one column per
// capability, then each note, under the table, naming its harness and capability.
func Table(profiles []Profile, results *Results) string {
	var b strings.Builder
	b.WriteString("| Harness |")
	for _, c := range Capabilities {
		b.WriteString(" " + c.Label + " |")
	}
	b.WriteString("\n| --- |")
	for range Capabilities {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	var notes []string
	for _, p := range profiles {
		h := results.Harnesses[p.Harness]
		fmt.Fprintf(&b, "| [%s](docs/harnesses/%s.mdx) |", p.Name, p.Harness)
		for _, cell := range Matrix(p, h) {
			b.WriteString(" " + symbol(cell.Status) + " |")
			if cell.Note != "" {
				notes = append(notes, fmt.Sprintf("- %s, %s: %s.", p.Name, strings.ToLower(cell.Capability.Label), cell.Note))
			}
		}
		b.WriteString("\n")
	}
	if len(notes) > 0 {
		b.WriteString("\n")
		b.WriteString(strings.Join(notes, "\n"))
		b.WriteString("\n")
	}
	var versions []string
	for _, p := range profiles {
		if h := results.Harnesses[p.Harness]; h != nil && h.Version != "" {
			v := h.Version
			if n := versionNumber.FindString(v); n != "" {
				v = n
			}
			versions = append(versions, p.Name+" "+v)
		}
	}
	sort.Strings(versions)
	if len(versions) > 0 {
		b.WriteString("\nLive results from " + strings.Join(versions, " and ") + ".\n")
	}
	return b.String()
}

var versionNumber = regexp.MustCompile(`\d+\.\d+(\.\d+)?`)

// ScenarioCapabilities lists the capabilities a live scenario proves.
func ScenarioCapabilities(scenario string) []string {
	var out []string
	for _, c := range Capabilities {
		if slices.Contains(c.Scenarios, scenario) {
			out = append(out, c.ID)
		}
	}
	return out
}
