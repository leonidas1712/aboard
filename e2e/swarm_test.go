//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// swarmEnv is an isolated machine where aboard swarm up can start sessions: claude,
// codex and omp on its PATH are e2e/fakeagent, which does what each harness does with
// Aboard without a model; herdr is e2e/fakeherdr, with herdr's launcher beside it; and
// tmux and herdr keep their sockets in folders of the test's own, never beside the
// person's.
type swarmEnv struct {
	*env
	log string
}

func newSwarmEnv(t *testing.T) *swarmEnv {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatal("tmux isn't installed, and the tmux launcher's tests need it. Install it: brew install tmux, or apt install tmux.")
	}
	e := newEnv(t)
	sockets, err := os.MkdirTemp("/tmp", "aboard-swarm-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	built := filepath.Dir(binary)
	s := &swarmEnv{env: e, log: filepath.Join(e.home, "fake-agents.jsonl")}
	for i, kv := range e.vars {
		if path, ok := strings.CutPrefix(kv, "PATH="); ok {
			e.vars[i] = "PATH=" + strings.Join([]string{filepath.Dir(e.bin), filepath.Join(built, "fakeagents"), filepath.Join(built, "herdrbin"), path}, string(os.PathListSeparator))
		}
	}
	e.vars = append(e.vars,
		"FAKE_AGENT_LOG="+s.log, "FAKE_CODEX="+filepath.Join(fakeBin, "codex"),
		"TMUX_TMPDIR="+sockets, "XDG_CONFIG_HOME="+sockets,
	)
	t.Cleanup(func() {
		_ = e.exec(nil, "", "swarm", "down", "--json")
		if entries, _ := os.ReadDir(filepath.Join(sockets, "tmux-"+strconv.Itoa(os.Getuid()))); len(entries) != 0 {
			for _, sock := range entries {
				kill := exec.Command("tmux", "-L", sock.Name(), "kill-server")
				kill.Env = e.vars
				_ = kill.Run()
			}
		}
	})
	return s
}

// writeBoardFile writes aboard.yaml in the env's project folder.
func (s *swarmEnv) writeBoardFile(content string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.dir, "aboard.yaml"), []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// fakeStart is one start of a fake harness, as it logged it.
type fakeStart struct {
	Harness  string   `json:"harness"`
	Argv     []string `json:"argv"`
	Session  string   `json:"session"`
	Source   string   `json:"source"`
	Prompt   string   `json:"prompt"`
	Print    bool     `json:"print"`
	Agent    string   `json:"agent"`
	Launch   string   `json:"launch"`
	Headless string   `json:"headless"`
	Cwd      string   `json:"cwd"`
}

// starts are the fake harnesses' starts so far, for one agent or for every one.
func (s *swarmEnv) starts(agent string) []fakeStart {
	s.t.Helper()
	raw, err := os.ReadFile(s.log)
	if err != nil {
		return nil
	}
	var out []fakeStart
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var st fakeStart
		if json.Unmarshal([]byte(line), &st) == nil && (agent == "" || st.Agent == agent) {
			out = append(out, st)
		}
	}
	return out
}

// agentsByName indexes a swarm command's agents by name.
func agentsByName(t *testing.T, v map[string]any) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, a := range field(t, v, "agents").([]any) {
		m := a.(map[string]any)
		out[m["name"].(string)] = m
	}
	return out
}

const trioFile = `board: trio
title: Three harnesses
launcher: tmux
agents:
  - {name: claude, harness: claude-code}
  - {name: codex, harness: codex, role: member}
  - {name: omp, harness: omp, model: fake-model, args: ["--approval-mode", "yolo"]}
`

// swarm up starts one Claude Code, one Codex and one omp in tmux windows of a tmux
// server of the swarm's own, each with its identity in its environment: no join line is
// pasted, and each session takes its seat the way its harness reports in (Claude Code's
// session-start hook, omp's extension, Codex's first command). A second swarm up starts
// nothing; ps shows what runs; down stops the sessions and keeps the seats.
func TestSwarmUpStartsEachHarnessInTmuxWithItsIdentity(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile(trioFile)

	v := s.run("swarm", "up", "--json").json(t)
	matchesCLISpec(t, "SwarmUpOutput", v)
	if field(t, v, "board.name") != "trio" || field(t, v, "board_created") != true || field(t, v, "board.title") != "Three harnesses" {
		t.Fatalf("swarm up should create the board trio from the file: %v", v)
	}
	swarm := field(t, v, "swarm").(string)
	agents := agentsByName(t, v)
	for _, name := range []string{"claude", "codex", "omp"} {
		ag := agents[name]
		if ag["action"] != "started" || ag["seat_created"] != true || ag["seated"] != true || ag["launcher"] != "tmux" || ag["state"] != "running" {
			t.Fatalf("%s after swarm up: %v", name, ag)
		}
		if !strings.HasPrefix(ag["session"].(string), ag["harness"].(string)+":") {
			t.Fatalf("%s is seated in session %v", name, ag["session"])
		}
		if want := "tmux -L " + swarm + " attach -t " + swarm + ":" + name; ag["attach"] != want {
			t.Fatalf("%s's attach line is %v, want %s", name, ag["attach"], want)
		}
		starts := s.starts(name)
		if len(starts) != 1 {
			t.Fatalf("%s started %d times: %+v", name, len(starts), starts)
		}
		st := starts[0]
		if !strings.HasPrefix(st.Launch, "lch_") || st.Cwd != s.dir || !strings.Contains(st.Prompt, "You are "+name+" on the Aboard board trio") {
			t.Fatalf("%s's session started without its identity, folder or first prompt: %+v", name, st)
		}
	}
	if argv := s.starts("omp")[0].Argv; !slices.Equal(argv[:4], []string{"--model", "fake-model", "--approval-mode", "yolo"}) {
		t.Fatalf("omp's model and args should come right after the program: %q", argv)
	}
	// Every launch ticket was taken by the session it was for.
	if entries, _ := os.ReadDir(filepath.Join(s.stateDir(), "launches")); len(entries) != 0 {
		t.Fatalf("launch tickets were left untaken: %v", entries)
	}
	// Each agent acts as itself with no --as: its session holds its seat.
	if r := s.run("swarm", "ps", "--json"); !strings.Contains(r.stdout, `"seated": true`) {
		t.Fatalf("ps:\n%s", r)
	}

	again := s.run("swarm", "up", "--json").json(t)
	for name, ag := range agentsByName(t, again) {
		if ag["action"] != "already_running" || ag["seat_created"] != false {
			t.Fatalf("a second swarm up touched %s: %v", name, ag)
		}
	}
	if n := len(s.starts("")); n != 3 {
		t.Fatalf("a second swarm up started sessions: %d starts in all", n)
	}

	ps := s.run("swarm", "ps", "--json").json(t)
	matchesCLISpec(t, "SwarmPsOutput", ps)
	for name, ag := range agentsByName(t, ps) {
		if ag["state"] != "running" || ag["start"] != "fresh" || ag["seated"] != true {
			t.Fatalf("ps shows %s as %v", name, ag)
		}
	}
	if text := s.run("swarm", "ps").stdout; !strings.Contains(text, "trio · 3 agents · swarm "+swarm) {
		t.Fatalf("ps text:\n%s", text)
	}

	down := s.run("swarm", "down", "--json").json(t)
	matchesCLISpec(t, "SwarmDownOutput", down)
	if got := field(t, down, "stopped").([]any); len(got) != 3 {
		t.Fatalf("down stopped %v", got)
	}
	for name, ag := range agentsByName(t, s.run("swarm", "ps", "--json").json(t)) {
		if ag["state"] != "exited" {
			t.Fatalf("after down, %s is %v", name, ag["state"])
		}
	}
	members := s.ownerRequest("GET", "/v1/boards/trio/members", nil)["members"].([]any)
	if len(members) != 4 { // the person and the three agents: the seats stay
		t.Fatalf("after down the board has %d members, want the person and three agents", len(members))
	}
}

// An agent that had a session is resumed by swarm up, with the harness's own resume and
// the session id it last had, so it keeps its conversation: same seat, no new one, and
// ps says it was resumed. --fresh starts a new session instead.
func TestSwarmUpResumesAnAgentsLastSession(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile(trioFile)
	first := agentsByName(t, s.run("swarm", "up", "--json").json(t))
	s.run("swarm", "down", "codex", "claude")

	v := agentsByName(t, s.run("swarm", "up", "--json").json(t))
	for _, name := range []string{"claude", "codex"} {
		ag := v[name]
		if ag["action"] != "resumed" || ag["seat_created"] != false || ag["seated"] != true || ag["session"] != first[name]["session"] {
			t.Fatalf("%s should resume its session %v: %v", name, first[name]["session"], ag)
		}
		starts := s.starts(name)
		last := starts[len(starts)-1]
		id := strings.SplitN(first[name]["session"].(string), ":", 2)[1]
		if last.Source != "resume" || last.Session != id || last.Launch != "" {
			t.Fatalf("%s wasn't started with its harness's resume of %s: %+v", name, id, last)
		}
	}
	if v["omp"]["action"] != "already_running" {
		t.Fatalf("omp kept running, so swarm up should leave it: %v", v["omp"])
	}
	if argv := s.starts("codex")[1].Argv; !slices.Contains(argv, "resume") {
		t.Fatalf("codex resumed with %q, want codex resume <id>", argv)
	}
	ps := agentsByName(t, s.run("swarm", "ps", "--json").json(t))
	if ps["codex"]["start"] != "resumed" || ps["omp"]["start"] != "fresh" {
		t.Fatalf("ps should say codex was resumed and omp started fresh: %v %v", ps["codex"], ps["omp"])
	}
	members := s.ownerRequest("GET", "/v1/boards/trio/members", nil)["members"].([]any)
	if len(members) != 4 {
		t.Fatalf("resuming made seats: the board has %d members", len(members))
	}

	s.run("swarm", "down", "claude")
	fresh := agentsByName(t, s.run("swarm", "up", "--fresh", "--json").json(t))["claude"]
	if fresh["action"] != "started" || fresh["session"] == first["claude"]["session"] {
		t.Fatalf("--fresh should start a new session: %v", fresh)
	}
}

// The headless launcher runs a runner that waits on the agent's inbox and runs one
// headless turn of the harness per batch of messages, resuming the same harness session;
// the turn's hooks do nothing, since the runner delivers. The agent answers from there.
func TestSwarmUpRunsHeadlessTurns(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: quiet\nagents:\n  - {name: worker, harness: claude-code, launcher: headless}\n")
	ag := agentsByName(t, s.run("swarm", "up", "--json").json(t))["worker"]
	if ag["mode"] != "headless" || ag["seated"] != true || ag["state"] != "running" {
		t.Fatalf("the headless agent after swarm up: %v", ag)
	}
	eventually(t, 20*time.Second, "the first headless turn", func() bool { return len(s.starts("worker")) == 1 })
	first := s.starts("worker")[0]
	if !first.Print || first.Headless != "1" || first.Launch != "" {
		t.Fatalf("the first turn should be claude --print with ABOARD_HEADLESS and no ticket: %+v", first)
	}

	s.postAsOwnerTo("quiet", "@worker", "run: aboard say --to @alex HEADLESS-PONG")
	eventually(t, 30*time.Second, "the worker's reply from a headless turn", func() bool {
		r := s.exec(nil, "", "read", "--as", "worker", "--json")
		return strings.Contains(r.stdout, "HEADLESS-PONG")
	})
	turns := s.starts("worker")
	last := turns[len(turns)-1]
	if !slices.Contains(last.Argv, "--resume") || last.Session != first.Session || !strings.Contains(last.Prompt, "aboard-message") {
		t.Fatalf("the second turn should resume %s with the message as its prompt: %+v", first.Session, last)
	}

	s.run("swarm", "down")
	eventually(t, 10*time.Second, "the runner to report no session", func() bool {
		for _, m := range s.ownerRequest("GET", "/v1/boards/quiet/members", nil)["members"].([]any) {
			if mm := m.(map[string]any); mm["name"] == "worker" {
				return mm["presence"] == "no_session"
			}
		}
		return false
	})
}

// herdr's launcher, an aboard-launcher-herdr on the PATH speaking the launcher protocol,
// starts the agents in panes of a herdr session of the swarm's own, and stops it once
// the last agent stops. Here herdr is a stand-in that serves herdr's socket API.
func TestSwarmUpThroughTheHerdrLauncher(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: panes\nlauncher: herdr\nagents:\n  - {name: claude, harness: claude-code}\n  - {name: omp, harness: omp}\n")
	v := s.run("swarm", "up", "--json").json(t)
	swarm := field(t, v, "swarm").(string)
	for name, ag := range agentsByName(t, v) {
		if ag["launcher"] != "herdr" || ag["seated"] != true || !strings.HasPrefix(ag["handle"].(string), swarm+"/") || ag["attach"] != "herdr session attach "+swarm {
			t.Fatalf("%s through herdr: %v", name, ag)
		}
	}
	s.run("swarm", "down")
	sessions := filepath.Join(s.varValue("XDG_CONFIG_HOME"), "herdr", "sessions")
	if entries, _ := os.ReadDir(sessions); len(entries) != 0 {
		t.Fatalf("herdr's launcher left its session after the last agent stopped: %v", entries)
	}
}

// varValue is the value of one of the env's variables.
func (e *env) varValue(name string) string {
	for _, kv := range e.vars {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// Starting, listing and stopping agents use the person's login on the person's machine,
// so inside a harness session the swarm commands refuse and hand over the command.
func TestSwarmCommandsRefuseInsideASession(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile(trioFile)
	for _, args := range [][]string{{"swarm", "up"}, {"swarm", "ps"}, {"swarm", "down"}} {
		r := s.exec([]string{"CLAUDECODE=1"}, "", append(args, "--json")...)
		if r.code != 1 || field(t, r.json(t), "error.code") != "human_command_in_session" ||
			!strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard "+strings.Join(args, " ")) {
			t.Fatalf("aboard %s inside a session should refuse with the command to hand over:\n%s", strings.Join(args, " "), r)
		}
	}
	if len(s.starts("")) != 0 {
		t.Fatal("a refused swarm up started sessions")
	}
}

// A board file swarm up can't use is refused before anything starts, with each problem
// and where it is; so are a launcher that isn't there and a harness the headless
// launcher can't run.
func TestSwarmUpRefusesWhatItCantStart(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	cases := []struct {
		file, code string
		want       []string
	}{
		{"board: x1\nagents:\n  - {name: a, harness: claude-code, shade: blue}\n", "board_file_invalid", []string{"/agents/0"}},
		{"board: x2\nagents:\n  - {harness: claude-code}\n", "board_file_invalid", []string{"/agents/0", "name"}},
		{"board: x3\nagents:\n  - {name: a, harness: gemini}\n", "board_file_invalid", []string{"/agents/0/harness", "gemini"}},
		{"board: x4\nagents:\n  - {name: a, harness: claude-code}\n  - {name: a, harness: codex}\n", "board_file_invalid", []string{"named twice"}},
		{"board: x5\nroles:\n  writer: {can: [post]}\nagents:\n  - {name: a, harness: claude-code}\n", "board_file_invalid", []string{"/roles"}},
		{"agents:\n  - {name: a, harness: claude-code}\n", "board_file_invalid", []string{"/board"}},
		{"board: x6\nagents:\n  - {name: a, harness: claude-code, launcher: nowhere}\n", "launcher_not_found", []string{"aboard-launcher-nowhere"}},
		{"board: x7\nagents:\n  - {name: a, harness: codex, launcher: headless}\n", "headless_unsupported", []string{"Codex"}},
	}
	for _, c := range cases {
		s.writeBoardFile(c.file)
		r := s.runExit("swarm", "up", "--json")
		if r.code != 1 || field(t, r.json(t), "error.code") != c.code {
			t.Fatalf("swarm up with\n%s\nwant %s:\n%s", c.file, c.code, r)
		}
		for _, w := range c.want {
			if !strings.Contains(r.stdout, w) {
				t.Fatalf("swarm up with\n%s\nshould name %q:\n%s", c.file, w, r)
			}
		}
	}
	if r := s.runExit("swarm", "up", "--file", "missing.yaml", "--json"); field(t, r.json(t), "error.code") != "board_file_not_found" {
		t.Fatalf("a missing board file:\n%s", r)
	}
	if len(s.starts("")) != 0 {
		t.Fatal("a refused swarm up started sessions")
	}
}

// A session holds its seat from the moment it reports in, with no turn: swarm ps shows
// each agent seated within seconds, for every harness. Claude Code and omp start with no
// first prompt, so they run no turn at all; a Codex session exists only from its first
// turn, so it keeps the default prompt.
func TestSwarmPsShowsTheSeatWithNoTurn(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: seats\nagents:\n" +
		"  - {name: claude, harness: claude-code, prompt: \"\"}\n" +
		"  - {name: omp, harness: omp, prompt: \"\"}\n" +
		"  - {name: codex, harness: codex}\n")
	s.run("swarm", "up", "--wait", "0")
	eventually(t, 10*time.Second, "swarm ps to show every agent seated", func() bool {
		for _, ag := range agentsByName(t, s.run("swarm", "ps", "--json").json(t)) {
			if ag["seated"] != true || ag["session"] == nil {
				return false
			}
		}
		return true
	})
	for _, name := range []string{"claude", "omp"} {
		if p := s.starts(name)[0].Prompt; p != "" {
			t.Fatalf("%s started with the prompt %q, want none", name, p)
		}
	}
}

// A harness that quits as it starts fails swarm up at once, not after --wait, naming the
// agent and how to see why; its tmux window stays, dead, showing its last output.
func TestSwarmUpReportsASessionThatEndedBeforeItsSeat(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.vars = append(s.vars, "FAKE_AGENT_EXIT=solo")
	s.writeBoardFile("board: quits\nagents:\n  - {name: solo, harness: claude-code}\n")
	began := time.Now()
	r := s.runExit("swarm", "up", "--wait", "60s", "--json")
	v := r.json(t)
	if r.code != 1 || field(t, v, "error.code") != "swarm_not_ready" || time.Since(began) > 20*time.Second {
		t.Fatalf("swarm up should fail at once when the session ends (took %s):\n%s", time.Since(began), r)
	}
	if ex := field(t, v, "error.details.exited").([]any); len(ex) != 1 || ex[0] != "solo" {
		t.Fatalf("details.exited = %v", ex)
	}
	hint := field(t, v, "error.hint").(string)
	if !strings.Contains(hint, "tmux -L ") {
		t.Fatalf("the hint should say how to see why: %s", hint)
	}
	ps := agentsByName(t, s.run("swarm", "ps", "--json").json(t))["solo"]
	if ps["state"] != "exited" || ps["seated"] != false {
		t.Fatalf("ps: %v", ps)
	}
	swarm := strings.Fields(strings.SplitN(hint, "tmux -L ", 2)[1])[0]
	capture := exec.CommandContext(t.Context(), "tmux", "-L", swarm, "capture-pane", "-p", "-S", "-", "-t", swarm+":solo")
	capture.Env = s.vars
	out, err := capture.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "quitting at start") {
		t.Fatalf("the dead window should show the harness's last output: %v\n%s", err, out)
	}
}

// A session that never ran a turn has no conversation to resume (Claude Code saves one
// only from its first turn, and quits when asked to resume one it doesn't have), so the
// next swarm up starts it fresh, says why, and the agent takes its seat again.
func TestSwarmUpStartsFreshWhenTheLastSessionRanNoTurn(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: idle\nagents:\n" +
		"  - {name: claude, harness: claude-code, prompt: \"\"}\n" +
		"  - {name: omp, harness: omp, prompt: \"\"}\n")
	first := agentsByName(t, s.run("swarm", "up", "--json").json(t))
	s.run("swarm", "down")
	again := s.run("swarm", "up", "--wait", "30s", "--json")
	for name, ag := range agentsByName(t, again.json(t)) {
		if ag["action"] != "started" || ag["start"] != "fresh" || ag["seated"] != true || ag["session"] == first[name]["session"] ||
			!strings.Contains(fmt.Sprint(ag["start_note"]), "never ran a turn") {
			t.Fatalf("%s should start fresh, seated, saying why:\n%v", name, ag)
		}
	}
	if text := s.run("swarm", "ps", "--json"); !strings.Contains(text.stdout, "never ran a turn") {
		t.Fatalf("ps should say why the agents started fresh:\n%s", text)
	}
}

// A resumed session that ends as it starts (its harness lost the conversation) is
// started once more, fresh, and the agent takes its seat; swarm up says so.
func TestSwarmUpStartsFreshWhenAResumeFails(t *testing.T) {
	testSwarmUpStartsFreshWhenAResumeFails(t)
}

// A resumed session never gets its first prompt again: the conversation has it, and an
// instruction such as "do only this" would make the agent turn down what waited. It gets
// a short note that it was restarted instead, and the message that waited for it comes
// with that turn.
func TestSwarmUpResumeDoesNotReplayTheFirstPrompt(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: replay\nagents:\n  - {name: claude, harness: claude-code, prompt: \"Do only this. run: aboard say FIRST-TURN\"}\n")
	s.run("swarm", "up", "--json")
	s.run("swarm", "down")
	s.postAsOwnerTo("replay", "@claude", "WAITED-FOR-YOU")
	ag := agentsByName(t, s.run("swarm", "up", "--json").json(t))["claude"]
	if ag["action"] != "resumed" {
		t.Fatalf("claude should be resumed: %v", ag)
	}
	starts := s.starts("claude")
	last := starts[len(starts)-1]
	if strings.Contains(last.Prompt, "FIRST-TURN") || !strings.Contains(last.Prompt, "restarted this session") {
		t.Fatalf("the resumed session's prompt should be the restart note, not the first prompt: %q", last.Prompt)
	}
	eventually(t, 10*time.Second, "the waiting message to come with the resumed turn", func() bool {
		raw, _ := os.ReadFile(s.log + ".context-" + last.Session)
		return strings.Contains(string(raw), "WAITED-FOR-YOU")
	})
}

func testSwarmUpStartsFreshWhenAResumeFails(t *testing.T) {
	t.Parallel()
	s := newSwarmEnv(t)
	s.writeBoardFile("board: lost\nagents:\n  - {name: claude, harness: claude-code}\n")
	first := agentsByName(t, s.run("swarm", "up", "--json").json(t))["claude"]
	s.run("swarm", "down")
	if err := os.RemoveAll(s.log + ".conversations"); err != nil {
		t.Fatal(err)
	}
	ag := agentsByName(t, s.run("swarm", "up", "--wait", "30s", "--json").json(t))["claude"]
	if ag["action"] != "started" || ag["seated"] != true || ag["session"] == first["session"] ||
		!strings.Contains(fmt.Sprint(ag["start_note"]), "resuming the last session failed") {
		t.Fatalf("claude should start fresh after its resume failed:\n%v", ag)
	}
	starts := s.starts("claude")
	if len(starts) != 3 || starts[1].Source != "resume" || starts[2].Source != "startup" {
		t.Fatalf("want a start, a failed resume and a fresh start: %+v", starts)
	}
}
