//go:build live

package live

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// swarmAgentRow is one agent of aboard swarm up or ps --json.
type swarmAgentRow struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Action  string `json:"action"`
	State   string `json:"state"`
	Start   string `json:"start"`
	Session string `json:"session"`
	Seated  bool   `json:"seated"`
	Attach  string `json:"attach"`
	Handle  string `json:"handle"`
}

// swarmOut is what aboard swarm up and ps print with --json.
type swarmOut struct {
	Swarm  string          `json:"swarm"`
	Agents []swarmAgentRow `json:"agents"`
}

// swarmLab is a lab set up for aboard swarm up: a folder for the board file, per-agent
// project folders set up the way the live kit sets each harness up, and the variables
// the swarm's own tmux server and herdr session need to stay in folders of the test's
// own.
type swarmLab struct {
	*lab
	dir string
	// env is what aboard swarm commands run with: the lab's, with tmux's and herdr's
	// folders, and a bin folder first on the PATH for the herdr launcher and omp's
	// wrapper.
	env     []string
	sockets string
	entries []string
}

func newSwarmLab(t *testing.T) *swarmLab {
	t.Helper()
	l := newLab(t)
	sockets, err := os.MkdirTemp("/tmp", "abs-")
	if err != nil {
		t.Fatal(err)
	}
	s := &swarmLab{lab: l, dir: filepath.Join(l.dir, "swarm"), sockets: sockets}
	bin := filepath.Join(l.dir, "swarm-bin")
	for _, d := range []string{s.dir, bin} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	s.env = slices.Clone(l.vars)
	for i, kv := range s.env {
		if path, ok := strings.CutPrefix(kv, "PATH="); ok {
			s.env[i] = "PATH=" + bin + string(os.PathListSeparator) + path
		}
	}
	// tmux's and herdr's sockets and herdr's sessions go in a short folder of the test's
	// own, never beside the person's tmux server or in the person's herdr config.
	s.env = append(s.env, "TMUX_TMPDIR="+sockets, "XDG_CONFIG_HOME="+sockets, "XDG_STATE_HOME="+filepath.Join(sockets, "state"))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		// The swarm's panes stop with it, so what they show is kept first.
		s.capturePanes(ctx)
		_ = s.cmd(ctx, "swarm", "down", "--json").Run()
		entries, _ := os.ReadDir(filepath.Join(sockets, "tmux-"+strconv.Itoa(os.Getuid())))
		for _, e := range entries {
			_ = command(ctx, "tmux", "-S", filepath.Join(sockets, "tmux-"+strconv.Itoa(os.Getuid()), e.Name()), "kill-server").Run()
		}
		_ = os.RemoveAll(sockets)
	})
	return s
}

// cmd is an aboard swarm command in the board file's folder, with the swarm's variables.
func (s *swarmLab) cmd(ctx context.Context, args ...string) *exec.Cmd {
	c := command(ctx, s.bin, args...)
	c.Dir, c.Env = s.dir, s.env
	return c
}

// swarm runs an aboard swarm command with --json and decodes its output.
func (s *swarmLab) swarm(args ...string) swarmOut {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.t.Context(), 3*time.Minute)
	defer cancel()
	out, err := s.cmd(ctx, append([]string{"swarm"}, append(args, "--json")...)...).CombinedOutput()
	var v swarmOut
	if err != nil || json.Unmarshal(out, &v) != nil {
		s.t.Fatalf("aboard swarm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return v
}

// agent returns the row of one agent.
func (o swarmOut) agent(t *testing.T, name string) swarmAgentRow {
	t.Helper()
	for _, a := range o.Agents {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no agent %s in %+v", name, o)
	return swarmAgentRow{}
}

// add sets a harness up in a project folder of its own, the way the live kit does for
// that harness (aboard init in the project; the folder trusted; Codex's hooks trusted;
// omp's scratch home), and adds the agent to the board file.
func (s *swarmLab) add(d *driver, name string) {
	s.t.Helper()
	dir := s.project(name+"-project", d.p.Harness)
	var args []string
	switch d.p.Harness {
	case "claude-code":
		s.trustInClaude(dir)
		// Not the suite's other options: --allowedTools takes any number of values, so it
		// would swallow the first prompt that swarm up puts after the options.
	case "codex":
		home := s.codexHome(requireCodex(s.t))
		appendFile(s.t, filepath.Join(home, "config.toml"), fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir))
		if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte("[sandbox_workspace_write]\nnetwork_access = true\n"), 0o600); err != nil {
			s.t.Fatal(err)
		}
		s.scopeCodexHooks(dir, s.env)
		s.trustCodexHooks(dir, home, s.env)
		args = s.codexArgv()[3:] // after codex -m <model>
	case "omp":
		// omp runs with the scratch home the live kit logs it in to; a wrapper on the
		// swarm's PATH gives it that, as the kit's own start does (ompEnv).
		ompPath, err := exec.LookPath("omp")
		if err != nil {
			s.t.Fatal(err)
		}
		var script strings.Builder
		script.WriteString("#!/bin/sh\nexec env")
		for _, kv := range s.ompEnv()[len(s.vars):] {
			script.WriteString(" " + shellQuote(kv))
		}
		script.WriteString(" " + shellQuote(ompPath) + " \"$@\"\n")
		if err := os.WriteFile(filepath.Join(s.lab.dir, "swarm-bin", "omp"), []byte(script.String()), 0o700); err != nil { //nolint:gosec // a launch script must be executable
			s.t.Fatal(err)
		}
	}
	entry := fmt.Sprintf("  - name: %s\n    harness: %s\n    model: %q\n    dir: %q\n", name, d.p.Harness, d.model(), dir)
	if len(args) > 0 {
		quoted := make([]string, len(args))
		for i, a := range args {
			quoted[i] = strconv.Quote(a)
		}
		entry += "    args: [" + strings.Join(quoted, ", ") + "]\n"
	}
	s.entries = append(s.entries, entry)
}

// writeFile writes the board file for the agents added, with launcher.
func (s *swarmLab) writeFile(board, launcher string) {
	s.t.Helper()
	content := "board: " + board + "\nlauncher: " + launcher + "\nagents:\n" + strings.Join(s.entries, "")
	if err := os.WriteFile(filepath.Join(s.dir, "aboard.yaml"), []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// waitSeated waits until every agent of the swarm is seated. In the tmux launcher's
// windows it answers the questions a harness asks on the way, as the kit's drivers do.
func (s *swarmLab) waitSeated(panes []*pane) {
	s.t.Helper()
	var last swarmOut
	ok := waitQuietly(5*time.Minute, func() bool {
		for _, p := range panes {
			p.idle() // answers a hooks or trust question, if one shows
		}
		last = s.swarm("ps")
		for _, a := range last.Agents {
			if a.State == "exited" {
				// Waiting can't help: the window, kept open, says why it ended.
				s.t.Fatalf("%s's session ended before it took its seat (see its pane in the artifacts):\n%+v", a.Name, last.Agents)
			}
		}
		for _, a := range last.Agents {
			if !a.Seated {
				return false
			}
		}
		return true
	})
	if !ok {
		s.t.Fatalf("timed out after 5m0s waiting for every agent of the swarm to take its seat; swarm ps said:\n%+v", last.Agents)
	}
}

// capturePanes saves what every pane of the swarm's own tmux servers and herdr sessions
// shows, as artifacts of the lab.
func (s *swarmLab) capturePanes(ctx context.Context) {
	if s.extra == nil {
		s.extra = map[string][]byte{}
	}
	tmuxDir := filepath.Join(s.sockets, "tmux-"+strconv.Itoa(os.Getuid()))
	servers, _ := os.ReadDir(tmuxDir)
	for _, srv := range servers {
		sock := filepath.Join(tmuxDir, srv.Name())
		out, err := command(ctx, "tmux", "-S", sock, "list-windows", "-a", "-F", "#{window_id} #{window_name}").Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			id, name, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			text, _ := command(ctx, "tmux", "-S", sock, "capture-pane", "-p", "-J", "-S", "-", "-t", id).Output()
			s.extra["pane-"+srv.Name()+"-"+name+".txt"] = text
		}
	}
	sessions, _ := os.ReadDir(filepath.Join(s.sockets, "herdr", "sessions"))
	for _, sess := range sessions {
		sock := filepath.Join(s.sockets, "herdr", "sessions", sess.Name(), "herdr.sock")
		var list struct {
			Panes []struct {
				PaneID string `json:"pane_id"`
				Label  string `json:"label"`
			} `json:"panes"`
		}
		if herdrCall(ctx, sock, "pane.list", map[string]any{}, &list) != nil {
			continue
		}
		for _, p := range list.Panes {
			var read struct {
				Text string `json:"text"`
			}
			if herdrCall(ctx, sock, "pane.read", map[string]any{"pane_id": p.PaneID, "source": "recent_unwrapped", "lines": 2000}, &read) == nil {
				s.extra["pane-"+sess.Name()+"-"+p.Label+".txt"] = []byte(read.Text)
			}
		}
	}
}

// herdrCall sends one request on a herdr session's socket, as the herdr launcher does.
func herdrCall(ctx context.Context, sock, method string, params, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	line, err := json.Marshal(map[string]any{"id": "live", "method": method, "params": params})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return err
	}
	reply, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(reply, &resp); err != nil {
		return err
	}
	if len(resp.Error) > 0 && string(resp.Error) != "null" {
		return fmt.Errorf("herdr %s: %s", method, resp.Error)
	}
	return json.Unmarshal(resp.Result, out)
}

// swarmPanes are the tmux windows the swarm's tmux launcher opened, driven like the
// kit's own panes.
func (s *swarmLab) swarmPanes(out swarmOut) []*pane {
	sock := filepath.Join(s.sockets, "tmux-"+strconv.Itoa(os.Getuid()), out.Swarm)
	var panes []*pane
	for _, a := range out.Agents {
		var d *driver
		for _, cand := range drivers(s.t) {
			if cand.p.Harness == a.Harness {
				d = cand
			}
		}
		panes = append(panes, &pane{l: s.lab, name: a.Name, harness: d.p.Command, d: d, sock: sock, session: out.Swarm})
	}
	return panes
}

// aboard swarm up starts one Claude Code, one Codex and one omp from a board file, each
// seated with no join line pasted, through tmux and through herdr; then a message from
// one agent to another is answered.
func TestSwarmUpStartsEveryHarness(t *testing.T) {
	t.Parallel()
	all := drivers(t)
	for _, launcherName := range []string{"tmux", "herdr"} {
		t.Run(launcherName, func(t *testing.T) {
			for _, d := range all {
				d.require(t)
			}
			if launcherName == "herdr" {
				if _, err := exec.LookPath("herdr"); err != nil {
					t.Fatal("herdr isn't installed; the herdr launcher's live check needs it. Install it from https://github.com/naaive/herdr.")
				}
			}
			parallel(t)
			for _, d := range all {
				record(t, d, "SwarmUpStartsEveryHarness")
			}
			s := newSwarmLab(t)
			for _, d := range all {
				d.setUp(s.lab)
			}
			if launcherName == "herdr" {
				build := command(t.Context(), "go", "build", "-o", filepath.Join(s.lab.dir, "swarm-bin", "aboard-launcher-herdr"), "./launchers/herdr")
				build.Dir = "../.."
				if out, err := build.CombinedOutput(); err != nil {
					t.Fatalf("build the herdr launcher: %v\n%s", err, out)
				}
			}
			for _, d := range all {
				s.add(d, d.p.Command)
			}
			s.writeFile("swarm-"+launcherName, launcherName)

			up := s.swarm("up", "--wait", "0")
			for _, a := range up.Agents {
				if a.Action != "started" {
					t.Fatalf("%s: %+v", a.Name, a)
				}
			}
			var panes []*pane
			if launcherName == "tmux" {
				panes = s.swarmPanes(up)
			}
			s.waitSeated(panes)
			t.Logf("watch with: %s", up.Agents[0].Attach)

			// claude asks codex for a reply, and codex answers.
			since := time.Now()
			s.say("claude", "--to", "@codex", "Run this command now, and nothing else: aboard say --to @claude PONG-SWARM")
			s.waitMessage("codex", since, "PONG-SWARM", 5*time.Minute)

			s.swarm("down")
			for _, a := range s.swarm("ps").Agents {
				if a.State != "exited" {
					t.Fatalf("after down, %s is %s", a.Name, a.State)
				}
			}
		})
	}
}

// An agent swarm up started, stopped with swarm down, is resumed by the next swarm up in
// the same harness session, and answers the message that waited for it.
func TestSwarmUpResumesTheLastSession(t *testing.T) {
	eachHarness(t, "SwarmUpResumesTheLastSession", func(t *testing.T, d *driver, _ *recorder) {
		s := newSwarmLab(t)
		d.setUp(s.lab)
		s.add(d, "solo")
		s.writeFile("resume", "tmux")
		up := s.swarm("up", "--wait", "0")
		s.waitSeated(s.swarmPanes(up))
		first := s.swarm("ps").agent(t, "solo")

		s.swarm("down", "solo")
		since := time.Now()
		s.postAsOwner("resume", "@solo", "Run this command now, and nothing else: aboard say RESUMED-PONG")
		again := s.swarm("up", "--wait", "0")
		if a := again.agent(t, "solo"); a.Action != "resumed" {
			t.Fatalf("swarm up should resume solo's session %s: %+v", first.Session, a)
		}
		s.waitSeated(s.swarmPanes(again))
		if a := s.swarm("ps").agent(t, "solo"); a.Session != first.Session || a.Start != "resumed" {
			t.Fatalf("solo should be back in %s, resumed: %+v", first.Session, a)
		}
		s.waitMessage("solo", since, "RESUMED-PONG", 5*time.Minute)
	})
}

// The herdr launcher passes the launcher kit against the real herdr, in a home and
// config folders of the test's own, so herdr's sessions, plugins and state are never
// the person's. It needs no model, only herdr.
func TestHerdrLauncherPassesTheKit(t *testing.T) {
	t.Parallel()
	herdr, err := exec.LookPath("herdr")
	if err != nil {
		t.Fatal("herdr isn't installed; the herdr launcher's live check needs it. Install it from https://github.com/naaive/herdr.")
	}
	scratch, err := os.MkdirTemp("/tmp", "abh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	bin := filepath.Join(scratch, "bin")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(herdr, filepath.Join(bin, "herdr")); err != nil {
		t.Fatal(err)
	}
	build := command(t.Context(), "go", "build", "-o", filepath.Join(bin, "aboard-launcher-herdr"), "./launchers/herdr")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the herdr launcher: %v\n%s", err, out)
	}
	kit := command(t.Context(), "go", "test", "-tags", "launcherkit", "-count=1", "-v", "-run", "^TestLauncherKit$", "./server/internal/launcher/launchertest/")
	kit.Dir = "../.."
	kit.Env = append(cleanEnv(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "LAUNCHER=herdr",
		"HOME="+filepath.Join(scratch, "home"), "XDG_CONFIG_HOME="+filepath.Join(scratch, "config"),
		"XDG_STATE_HOME="+filepath.Join(scratch, "state"), "XDG_DATA_HOME="+filepath.Join(scratch, "data"),
		"GOCACHE="+goEnv(t, "GOCACHE"), "GOMODCACHE="+goEnv(t, "GOMODCACHE"), "GOPATH="+goEnv(t, "GOPATH"),
	)
	out, err := kit.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("the launcher kit against herdr failed: %v", err)
	}
}

// goEnv is one of go env's values, so a go command with a home of the test's own still
// uses the person's module and build caches instead of downloading into the scratch home.
func goEnv(t *testing.T, name string) string {
	t.Helper()
	out, err := command(t.Context(), "go", "env", name).Output()
	if err != nil {
		t.Fatalf("go env %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}
