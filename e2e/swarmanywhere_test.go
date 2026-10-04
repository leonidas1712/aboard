//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// in is the swarm env with commands running in another folder, made if needed.
func (s *swarmEnv) in(dir string) *env {
	s.t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	e := *s.env
	e.dir = dir
	return &e
}

// writeFileIn writes a board file at path.
func (s *swarmEnv) writeFileIn(path, content string) {
	s.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// swarmsByBoard indexes swarm list's swarms by board.
func swarmsByBoard(t *testing.T, v map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, s := range field(t, v, "swarms").([]any) {
		m := s.(map[string]any)
		out[m["board"].(string)] = m
	}
	return out
}

// swarm list shows every swarm this machine started, wherever its board file is, and
// --swarm runs up, ps and down on one of them from any folder, by the swarm's name or its
// board's. A swarm whose file moved is listed as such; ps and down still work on it, and
// swarm up asks for --file, then takes the file from its new place.
func TestSwarmsAreManagedFromAnyFolder(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	alphaDir, betaDir := filepath.Join(s.home, "alpha"), filepath.Join(s.home, "beta")
	alpha, beta := s.in(alphaDir), s.in(betaDir)
	s.writeFileIn(filepath.Join(alphaDir, "aboard.yaml"), "board: alpha\ntitle: First swarm\nagents:\n  - {name: claude, harness: claude-code}\n")
	s.writeFileIn(filepath.Join(betaDir, "aboard.yaml"), "board: beta\nagents:\n  - {name: omp, harness: omp}\n")
	alphaID := field(t, alpha.run("swarm", "up", "--json").json(t), "swarm").(string)
	betaID := field(t, beta.run("swarm", "up", "--json").json(t), "swarm").(string)
	elsewhere := s.in(filepath.Join(s.home, "elsewhere"))

	list := elsewhere.run("swarm", "list", "--json").json(t)
	matchesCLISpec(t, "SwarmListOutput", list)
	swarms := swarmsByBoard(t, list)
	for board, want := range map[string]struct{ id, dir string }{"alpha": {alphaID, alphaDir}, "beta": {betaID, betaDir}} {
		sw := swarms[board]
		if sw["swarm"] != want.id || sw["folder"] != want.dir || sw["file"] != filepath.Join(want.dir, "aboard.yaml") || sw["file_exists"] != true ||
			sw["agents_running"] != 1.0 || sw["agents_total"] != 1.0 || fmt.Sprint(sw["launchers"]) != "[tmux]" || sw["last_up"] == nil {
			t.Fatalf("swarm list's %s: %v", board, sw)
		}
	}
	if swarms["alpha"]["title"] != "First swarm" || swarms["beta"]["title"] != nil {
		t.Fatalf("swarm list's titles: %v %v", swarms["alpha"]["title"], swarms["beta"]["title"])
	}
	text := elsewhere.run("swarm", "list").stdout
	for _, want := range []string{"2 swarms on this machine:", alphaID + "  alpha  First swarm", "1 of 1 running", "up just now", alphaDir, betaDir} {
		if !strings.Contains(text, want) {
			t.Fatalf("swarm list should say %q:\n%s", want, text)
		}
	}

	// With two swarms and no board file here, ps and down can't tell which one.
	for _, cmd := range []string{"ps", "down"} {
		r := elsewhere.runExit("swarm", cmd, "--json")
		v := r.json(t)
		if r.code != 1 || field(t, v, "error.code") != "swarm_not_selected" || len(field(t, v, "error.details.choices").([]any)) != 2 ||
			!strings.Contains(field(t, v, "error.hint").(string), "--swarm") {
			t.Fatalf("swarm %s with two swarms and no board file:\n%s", cmd, r)
		}
	}

	// ps by the board's name, with the line to watch each agent.
	ps := elsewhere.run("swarm", "ps", "--swarm", "alpha", "--json").json(t)
	matchesCLISpec(t, "SwarmPsOutput", ps)
	if field(t, ps, "swarm") != alphaID || agentsByName(t, ps)["claude"]["state"] != "running" {
		t.Fatalf("swarm ps --swarm alpha: %v", ps)
	}
	attach := "tmux -L " + alphaID + " attach -t " + alphaID + ":claude"
	if text := elsewhere.run("swarm", "ps", "--swarm", alphaID).stdout; !strings.Contains(text, attach) {
		t.Fatalf("swarm ps should show the attach line %q:\n%s", attach, text)
	}

	// down and up by the swarm's name.
	down := elsewhere.run("swarm", "down", "--swarm", betaID, "--json").json(t)
	matchesCLISpec(t, "SwarmDownOutput", down)
	if fmt.Sprint(field(t, down, "stopped")) != "[omp]" {
		t.Fatalf("swarm down --swarm beta: %v", down)
	}
	up := elsewhere.run("swarm", "up", "--swarm", betaID, "--json").json(t)
	matchesCLISpec(t, "SwarmUpOutput", up)
	if field(t, up, "file") != filepath.Join(betaDir, "aboard.yaml") || agentsByName(t, up)["omp"]["seated"] != true {
		t.Fatalf("swarm up --swarm beta should start beta from its own file: %v", up)
	}
	if ag := agentsByName(t, up)["omp"]; ag["dir"] != betaDir {
		t.Fatalf("omp should run in its board file's folder, not where swarm up ran: %v", ag["dir"])
	}

	// The file moves: list still shows the swarm, ps and down still work, up asks for --file.
	moved := filepath.Join(betaDir, "moved.yaml")
	if err := os.Rename(filepath.Join(betaDir, "aboard.yaml"), moved); err != nil {
		t.Fatal(err)
	}
	if sw := swarmsByBoard(t, elsewhere.run("swarm", "list", "--json").json(t))["beta"]; sw["file_exists"] != false {
		t.Fatalf("swarm list should show beta's file gone: %v", sw)
	}
	if text := elsewhere.run("swarm", "list").stdout; !strings.Contains(text, betaDir+" (aboard.yaml is gone)") {
		t.Fatalf("swarm list should say beta's file is gone:\n%s", text)
	}
	if text := elsewhere.run("swarm", "ps", "--swarm", "beta").stdout; !strings.Contains(text, "isn't there any more") {
		t.Fatalf("swarm ps should say beta's file is gone:\n%s", text)
	}
	elsewhere.run("swarm", "down", "--swarm", "beta")
	r := elsewhere.runExit("swarm", "up", "--swarm", "beta", "--json")
	if r.code != 1 || field(t, r.json(t), "error.code") != "board_file_not_found" ||
		!strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard swarm up --swarm "+betaID+" --file") {
		t.Fatalf("swarm up for a swarm whose file moved should ask for --file:\n%s", r)
	}
	if r := elsewhere.runExit("swarm", "up", "--swarm", "beta", "--file", filepath.Join(alphaDir, "aboard.yaml"), "--json"); field(t, r.json(t), "error.code") != "board_file_invalid" {
		t.Fatalf("swarm up --swarm beta with alpha's file:\n%s", r)
	}
	elsewhere.run("swarm", "up", "--swarm", "beta", "--file", moved, "--json")
	if sw := swarmsByBoard(t, elsewhere.run("swarm", "list", "--json").json(t))["beta"]; sw["file"] != moved || sw["file_exists"] != true || sw["agents_running"] != 1.0 {
		t.Fatalf("swarm list after swarm up --file: %v", sw)
	}

	if r := elsewhere.runExit("swarm", "ps", "--swarm", "nowhere", "--json"); field(t, r.json(t), "error.code") != "swarm_not_found" {
		t.Fatalf("swarm ps --swarm nowhere:\n%s", r)
	}
	if r := elsewhere.runExit("swarm", "up", "--json"); !strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard swarm up --swarm") {
		t.Fatalf("swarm up with no board file here should name --swarm:\n%s", r)
	}
}

// swarm show gives one swarm in full, with each launcher's own line to watch each agent:
// a tmux window, the herdr session, or a headless runner's log; and the commands to stop
// and start it.
func TestSwarmShowGivesEachLaunchersCommands(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: mixed\ntitle: Three hosts\nagents:\n" +
		"  - {name: win, harness: claude-code, launcher: tmux}\n" +
		"  - {name: pane, harness: omp, launcher: herdr}\n" +
		"  - {name: bg, harness: claude-code, launcher: headless}\n")
	id := field(t, s.run("swarm", "up", "--json").json(t), "swarm").(string)
	elsewhere := s.in(filepath.Join(s.home, "elsewhere"))

	v := elsewhere.run("swarm", "show", "mixed", "--json").json(t)
	matchesCLISpec(t, "SwarmShowOutput", v)
	if field(t, v, "swarm") != id || field(t, v, "title") != "Three hosts" || field(t, v, "agents_running") != 3.0 ||
		fmt.Sprint(field(t, v, "launchers")) != "[headless herdr tmux]" || field(t, v, "commands.open") != "aboard open --board mixed" ||
		field(t, v, "commands.down") != "aboard swarm down --swarm "+id {
		t.Fatalf("swarm show mixed: %v", v)
	}
	agents := agentsByName(t, v)
	for name, watch := range map[string]string{
		"win":  "tmux -L " + id + " attach -t " + id + ":win",
		"pane": "herdr session attach " + id,
		"bg":   "tail -f " + filepath.Join(s.stateDir(), "swarms", "headless", id, "bg.log"),
	} {
		cmds := agents[name]["commands"].(map[string]any)
		if cmds["watch"] != watch || cmds["stop"] != "aboard swarm down "+name+" --swarm "+id || cmds["start"] != "aboard swarm up --swarm "+id {
			t.Fatalf("%s's commands: %v, want watch %q", name, cmds, watch)
		}
	}

	text := elsewhere.run("swarm", "show", id).stdout
	for _, want := range []string{
		"mixed · Three hosts · swarm " + id, "file      " + filepath.Join(s.dir, "aboard.yaml"),
		"running   3 of 3 agents", "win · claude-code · tmux · running · seated",
		"watch    herdr session attach " + id, "stop     aboard swarm down pane --swarm " + id,
		"Watch and message them: aboard open --board mixed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("swarm show should say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("swarm show into a pipe has control codes:\n%q", text)
	}

	elsewhere.run("swarm", "down", "pane", "--swarm", id)
	if text := elsewhere.run("swarm", "show", id).stdout; !strings.Contains(text, "start    aboard swarm up --swarm "+id) {
		t.Fatalf("swarm show should say how to start the stopped agent:\n%s", text)
	}
}
