//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
	skill "github.com/leonidas1712/aboard/skills/aboard"
)

// This file is the harness conformance kit's half that drives the real binary; the
// in-process half is server/internal/harness/registry/conformance_test.go. Both run for
// every harness with a profile in adapters/, with make conformance, or for one with
// make conformance HARNESS=<name>. Nothing here names a harness: what a test does comes
// from the profile, a harness reached through an extension is played by a client of the
// extension connection (extension_test.go), and the only per-harness code is the fakes
// in kitFakes.

// kitFake stands in for what a harness does outside Aboard's hooks.
type kitFake struct {
	// queued returns the bundles the harness's own queue took for a session, in order,
	// for a harness that delivers through a queue.
	queued func(e *env, sessionID string) []string
}

// kitFakes are the fakes of the harnesses that need one. A harness whose delivery waits
// for an idle hook needs none: the hook is aboard's own. A new harness that delivers
// another way adds its fake here, and its command to e2e/ (as e2e/fakecodex).
var kitFakes = map[string]kitFake{
	"codex": {queued: func(e *env, id string) []string {
		var out []string
		for _, c := range e.fakeCodexCalls() {
			if c["thread"] == id {
				out = append(out, c["message"])
			}
		}
		return out
	}},
}

// kitProfiles returns the profiles the kit runs for: every one, or those HARNESS names.
func kitProfiles(t *testing.T) []support.Profile {
	t.Helper()
	all, err := support.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("HARNESS")
	var out []support.Profile
	for _, p := range all {
		if only == "" || slices.Contains(strings.Split(only, ","), p.Harness) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no profile in adapters/ for HARNESS=%q", only)
	}
	return out
}

// kitResults records which of a harness's capabilities the kit measured, for the summary
// each harness's run ends with.
type kitResults struct {
	mu  sync.Mutex
	got map[string]string
}

func (r *kitResults) record(t *testing.T, capability string) {
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch {
		case t.Failed():
			r.got[capability] = "failed"
		case t.Skipped() && r.got[capability] == "":
			r.got[capability] = "n/a"
		case !t.Skipped() && r.got[capability] != "failed":
			r.got[capability] = "passed"
		}
	})
}

// TestHarnessConformance is the harness conformance kit's half that drives the real
// binary. For every harness with a profile it checks, without a model: the docs page;
// that aboard init installs exactly the profile's items in each scope, changes nothing
// the second time, and that uninstall leaves every file as it was; what doctor reports;
// that commands find the session from where the profile says; what each hook does; that
// a marked subagent may only read; and delivery: when idle, at a turn's end, the owner's
// messages at a tool boundary, after a killed session, and on resume.
func TestHarnessConformance(t *testing.T) {
	t.Parallel()
	for _, p := range kitProfiles(t) {
		t.Run(p.Harness, func(t *testing.T) {
			t.Parallel()
			res := &kitResults{got: map[string]string{}}
			t.Cleanup(func() {
				var line []string
				for _, c := range support.Capabilities {
					if s := res.got[c.ID]; s != "" {
						line = append(line, c.Label+": "+s)
					}
				}
				t.Logf("%s, measured without a model: %s", p.Name, strings.Join(line, "; "))
			})
			run := func(name, capability string, f func(t *testing.T, p support.Profile)) {
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					res.record(t, capability)
					f(t, p)
				})
			}
			run("DocsPage", "baseline", kitDocsPage)
			run("Init/global", "baseline", func(t *testing.T, p support.Profile) { kitInit(t, p, "global") })
			run("Init/project", "project_scope", func(t *testing.T, p support.Profile) { kitInit(t, p, "project") })
			run("Init/versions", "baseline", kitVersions)
			run("Doctor", "baseline", kitDoctor)
			run("Identity", "baseline", kitIdentity)
			run("Hooks", "presence", kitHooks)
			run("Subagents", "subagents", kitSubagents)
			run("Delivery/Idle", "idle_delivery", kitIdle)
			run("Delivery/TurnEnd", "turn_end", kitTurnEnd)
			run("Delivery/OwnerAtToolBoundary", "owner_mid_turn", kitOwnerAtToolBoundary)
			run("Delivery/WaitingNotice", "waiting_notice", kitWaitingNotice)
			run("Delivery/DeliveredIsRead", "idle_delivery", kitDeliveredIsRead)
			run("Delivery/ReadMidTurnIsNotDelivered", "turn_end", kitReadMidTurnIsNotDelivered)
			run("Delivery/ReadBeforeConfirmed", "idle_delivery", kitReadBeforeConfirmed)
			run("Delivery/AckedThroughTheAPI", "turn_end", kitAckedThroughTheAPI)
			run("Delivery/QuietWaitsForTheNextTurn", "idle_delivery", kitQuietWaitsForTheNextTurn)
			run("Delivery/QuietWhileBusy", "turn_end", kitQuietWhileBusy)
			run("Delivery/WakingCarriesTheQuiet", "idle_delivery", kitWakingCarriesTheQuiet)
			run("Delivery/MentionWakes", "idle_delivery", kitMentionWakes)
			run("Delivery/CombinesWakes", "idle_delivery", kitCombinesWakes)
			run("Delivery/Digest", "idle_delivery", kitDigest)
			run("Delivery/AllMode", "idle_delivery", kitAllMode)
			run("Delivery/ModeChanged", "idle_delivery", kitModeChanged)
			run("Delivery/KilledSession", "idle_delivery", kitKilledSession)
			run("Delivery/Resume", "reconnect", kitResume)
		})
	}
}

// kitEnv is an isolated machine where the harness looks installed: its config folder
// exists, and its command is on the PATH (a stand-in that prints a version, unless e2e/
// has a fake of it).
func kitEnv(t *testing.T, p support.Profile) *env {
	t.Helper()
	e := newEnv(t)
	if p.ConfigDir.Default != "" {
		if err := os.MkdirAll(filepath.Join(e.home, p.ConfigDir.Default), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(fakeBin, p.Command)); err != nil {
		bin := filepath.Join(e.home, "kit-bin")
		v := p.Checks.MinVersion
		if v == "" {
			v = "1.0.0"
		}
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, p.Command), []byte("#!/bin/sh\necho "+v+"\n"), 0o755); err != nil { //nolint:gosec // a test program
			t.Fatal(err)
		}
		for i, kv := range e.vars {
			if path, ok := strings.CutPrefix(kv, "PATH="); ok {
				e.vars[i] = "PATH=" + bin + string(os.PathListSeparator) + path
			}
		}
	}
	return e
}

// kitIDs gives each session in a test its own id, shaped like the UUIDs harnesses use.
var kitIDs atomic.Int64

func kitID() string { return fmt.Sprintf("019a0000-0000-7000-8000-%012d", 500000+kitIDs.Add(1)) }

// kitSession is one session of the harness, played from its profile: it carries the
// environment the harness gives every command, and calls the hooks by what they do.
type kitSession struct {
	*session
	p support.Profile
	// seen is how many bundles of the harness's queue the test has taken.
	seen int
	// ext is the session's extension connection, for a harness whose identity and
	// delivery come from Aboard's extension inside it, and welcome the daemon's answer to
	// its hello.
	ext     *extClient
	welcome extFrame
}

// kitStart starts a session the way the harness does, with base as the environment it
// was started with: the session-start hook runs with the session's id in its input, or
// the harness's extension connects with it, and later commands carry the session's
// variables. source is startup or resume.
func (e *env) kitStart(p support.Profile, id, source string, base []string) *kitSession {
	e.t.Helper()
	s := &kitSession{session: &session{e: e, harness: p.Harness, id: id, vars: slices.Clone(base)}, p: p}
	if p.Identity.Kind == "extension" {
		// The extension sets ABOARD_SESSION for the session's commands, and connects with a
		// boot id of its process.
		s.ext, s.welcome = e.extConnect(p.Harness, id, fmt.Sprintf("%016x", kitIDs.Add(1)), source)
		if s.welcome.Event != "welcome" {
			e.t.Fatalf("the daemon didn't welcome the extension's hello: %+v", s.welcome)
		}
		s.vars = append(s.vars, "ABOARD_SESSION="+p.Harness+":"+id)
		return s
	}
	h, ok := p.Hook("session-start")
	if !ok {
		e.t.Fatalf("%s has no hook of op session-start: the kit needs one to start a session", p.Harness)
	}
	switch p.Identity.Kind {
	case "env":
		s.vars = append(s.vars, p.Identity.Env+"="+id)
		if p.Identity.RootEnv != "" {
			s.vars = append(s.vars, p.Identity.RootEnv+"="+id)
		}
		s.started = e.exec(s.vars, hookInput(id, h.Event, `"source":"`+source+`"`), "hook", p.Harness, h.Run)
	case "hook":
		envFile := filepath.Join(e.home, "env-file-"+id)
		_ = os.Remove(envFile)
		s.started = e.exec(append(slices.Clone(s.vars), p.Identity.EnvFile+"="+envFile),
			hookInput(id, h.Event, `"source":"`+source+`"`), "hook", p.Harness, h.Run)
		s.vars = applyEnvFile(e.t, envFile, s.vars)
		if !slices.Contains(s.vars, "ABOARD_SESSION="+p.Harness+":"+id) {
			e.t.Fatalf("the session-start hook didn't write ABOARD_SESSION=%s:%s to %s", p.Harness, id, p.Identity.EnvFile)
		}
	default:
		e.t.Fatalf("the kit has no driver for identity kind %q yet: add one to kitStart", p.Identity.Kind)
	}
	if s.started.code != 0 {
		e.t.Fatalf("session-start hook failed\n%s", s.started)
	}
	return s
}

// kitStartIn starts a session whose session-start hook runs under a harness process of
// its own (e2e/fakeharness), and returns that process, which a test can kill as a crash
// would.
func (e *env) kitStartIn(p support.Profile, id string) (*kitSession, *exec.Cmd) {
	e.t.Helper()
	h, _ := p.Hook("session-start")
	s := &kitSession{session: &session{e: e, harness: p.Harness, id: id}, p: p}
	extra := []string{}
	envFile := filepath.Join(e.home, "env-file-"+id)
	switch p.Identity.Kind {
	case "env":
		s.vars = []string{p.Identity.Env + "=" + id}
		if p.Identity.RootEnv != "" {
			s.vars = append(s.vars, p.Identity.RootEnv+"="+id)
		}
		extra = s.vars
	case "hook":
		extra = []string{p.Identity.EnvFile + "=" + envFile}
	default:
		e.t.Fatalf("the kit has no driver for identity kind %q yet", p.Identity.Kind)
	}
	cmd := exec.Command(fakeHarness, e.bin, "hook", p.Harness, h.Run)
	cmd.Dir = e.dir
	cmd.Env = append(slices.Clone(e.vars), extra...)
	cmd.Stdin = strings.NewReader(hookInput(id, h.Event, `"source":"startup"`))
	out, err := cmd.StdoutPipe()
	if err != nil {
		e.t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "exit 0" {
		e.t.Fatalf("session-start hook under the harness: %q %v\n%s", line, err, stderr.String())
	}
	if p.Identity.Kind == "hook" {
		s.vars = applyEnvFile(e.t, envFile, nil)
	}
	return s, cmd
}

// applyEnvFile applies the export and unset lines of a session's environment file to
// vars, as the harness does when it loads the file into each command.
func applyEnvFile(t *testing.T, path string, vars []string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("the session-start hook wrote no environment file: %v", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if kv, ok := strings.CutPrefix(line, "export "); ok {
			vars = append(vars, strings.Trim(kv, `'"`))
		}
		if name, ok := strings.CutPrefix(line, "unset "); ok {
			vars = slices.DeleteFunc(vars, func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
		}
	}
	return vars
}

// extOps are what an extension sends on its connection for a hook's operation.
var extOps = map[string]string{"prompt": "prompt", "turn-end": "turn_end", "end": "goodbye"}

// op runs the session's hook of an operation, such as "prompt", with extra hook input,
// or, for a session an extension connected, sends what the extension sends for it.
func (s *kitSession) op(op, extra string) result {
	s.e.t.Helper()
	if s.ext != nil {
		msg, ok := extOps[op]
		if !ok {
			s.e.t.Fatalf("an extension has no message for the operation %s", op)
		}
		s.ext.send(map[string]any{"op": msg})
		return result{}
	}
	h, ok := s.p.Hook(op)
	if !ok {
		s.e.t.Fatalf("%s has no hook of op %s", s.p.Harness, op)
	}
	return s.e.exec(s.vars, hookInput(s.id, h.Event, extra), "hook", s.p.Harness, h.Run)
}

// startOp starts the session's hook of an operation in the background, as a harness
// starts its stop hook.
func (s *kitSession) startOp(op string) *proc {
	s.e.t.Helper()
	h, ok := s.p.Hook(op)
	if !ok {
		s.e.t.Fatalf("%s has no hook of op %s", s.p.Harness, op)
	}
	cmd := exec.Command(s.e.bin, "hook", s.p.Harness, h.Run)
	cmd.Dir = s.e.dir
	cmd.Env = append(slices.Clone(s.e.vars), s.vars...)
	cmd.Stdin = strings.NewReader(hookInput(s.id, h.Event, ""))
	p := &proc{t: s.e.t, cmd: cmd, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, done: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = p.out, p.errb
	if err := cmd.Start(); err != nil {
		s.e.t.Fatal(err)
	}
	go func() { p.done <- cmd.Wait() }()
	s.e.t.Cleanup(func() { _ = cmd.Process.Kill() })
	return p
}

// idle makes the session idle the way the harness does when a turn ends: its stop hook
// waits (returned, so the test can see what it is given), or its turn-end hook runs.
func (s *kitSession) idle() *proc {
	s.e.t.Helper()
	if s.ext != nil {
		s.op("turn-end", "")
		return nil
	}
	if s.p.WaitsForIdle() {
		return s.startOp("wait")
	}
	if _, ok := s.p.Hook("turn-end"); ok {
		if r := s.op("turn-end", ""); r.code != 0 {
			s.e.t.Fatalf("turn-end hook failed\n%s", r)
		}
	}
	return nil
}

// nextBundle waits for the next bundle the session is given: from its waiting hook w,
// which wakes the session with exit code 2 and the bundle on standard error, or from
// the harness's own queue.
func (s *kitSession) nextBundle(w *proc, within time.Duration) string {
	s.e.t.Helper()
	if s.ext != nil {
		return s.ext.deliver(within, true).Bundle
	}
	if s.p.WaitsForIdle() {
		r := w.wait(within)
		if r.code != 2 || !strings.Contains(r.stderr, "<aboard-messages") {
			s.e.t.Fatalf("the waiting hook should exit 2 with a bundle\n%s", r)
		}
		return r.stderr
	}
	fake, ok := kitFakes[s.p.Harness]
	if !ok {
		s.e.t.Fatalf("add a fake for %s to kitFakes: the kit needs to see what its queue receives", s.p.Harness)
	}
	var got string
	eventually(s.e.t, within, "the harness's queue to take a bundle", func() bool {
		q := fake.queued(s.e, s.id)
		if len(q) <= s.seen {
			return false
		}
		got = q[s.seen]
		s.seen++
		return true
	})
	return got
}

// queuedText is everything the harness's own queue took for the session.
func (s *kitSession) queuedText() string {
	if fake, ok := kitFakes[s.p.Harness]; ok {
		return strings.Join(fake.queued(s.e, s.id), "\n")
	}
	return ""
}

// kitPair makes a board from the person's terminal, with the person's agent writer, and
// has the session join it as reviewer.
func kitPair(t *testing.T, e *env, s *kitSession) {
	t.Helper()
	line := field(t, e.run("pair", "writer-reviewer", "--json").json(t), "join.line").(string)
	s.run("join", line, "--name", "reviewer")
}

// writerSays posts a message from writer to the reviewer and returns its sequence number.
func writerSays(t *testing.T, e *env, body string) int {
	t.Helper()
	return int(field(t, e.run("say", "--as", "writer", "--to", "@reviewer", body, "--json").json(t), "message.seq").(float64))
}

func kitDocsPage(t *testing.T, p support.Profile) {
	path := filepath.Join("..", "docs", "harnesses", p.Harness+".mdx")
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("every harness has a docs page a person or an agent can debug from: %v", err)
	}
	text := string(raw)
	for _, want := range []string{"aboard init", "aboard uninstall", "aboard doctor", "subagent", "aboard swarm up"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/harnesses/%s.mdx doesn't mention %s", p.Harness, want)
		}
	}
}

// kitItemPath is where an install item goes in a scope, relative to the home directory,
// or "" for an item without a file there.
func kitItemPath(p support.Profile, it support.Item, scope string) string {
	if scope == "project" {
		if it.Project == "" {
			return ""
		}
		return filepath.Join("project", filepath.FromSlash(it.Project))
	}
	if it.Global == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(it.Global, "{config_dir}/"); ok {
		return filepath.Join(p.ConfigDir.Default, filepath.FromSlash(rest))
	}
	return filepath.FromSlash(it.Global)
}

// personHooksFile is a hooks file a person already had, with an entry of their own.
func personHooksFile(event string) string {
	return `{
  "model": "their own setting",
  "hooks": {
    "` + event + `": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "echo their own hook"
          }
        ]
      }
    ]
  }
}
`
}

// kitInit checks aboard init installs exactly the profile's items in a scope, keeps the
// person's own hooks, changes nothing when run again, and that aboard uninstall leaves
// every file byte for byte as it was before.
func kitInit(t *testing.T, p support.Profile, scope string) {
	e := kitEnv(t, p)
	hooksItem, hasHooks := p.Item("hooks")
	var hooksRel string
	if hasHooks && len(p.Delivery.Hooks) > 0 {
		hooksRel = kitItemPath(p, hooksItem, scope)
		path := filepath.Join(e.home, hooksRel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(personHooksFile(p.Delivery.Hooks[0].Event)), 0o644); err != nil { //nolint:gosec // a test file
			t.Fatal(err)
		}
	}
	before := goldenFiles(t, e)

	args := []string{"init", "--yes", "--harness", p.Harness, "--allow-commands", "--json"}
	if scope == "project" {
		args = append(args, "--scope", "project")
	}
	e.run(args...)
	after := goldenFiles(t, e)

	var want, changed []string
	for _, it := range p.Install {
		if rel := kitItemPath(p, it, scope); rel != "" && !slices.Contains(want, rel) {
			want = append(want, rel)
		}
	}
	for rel, content := range after {
		if before[rel] != content {
			changed = append(changed, rel)
		}
	}
	slices.Sort(want)
	slices.Sort(changed)
	if !slices.Equal(changed, want) {
		t.Fatalf("aboard init --scope %s wrote %q; the profile's install items are %q", scope, changed, want)
	}
	if it, ok := p.Item("skill"); ok {
		if got := after[kitItemPath(p, it, scope)]; got != string(skill.Skill) {
			t.Errorf("the skill installed isn't the one built into aboard")
		}
	}
	if hooksRel != "" {
		kitCheckHooksFile(t, e, p, after[hooksRel])
	}
	if it, ok := p.Item("file"); ok {
		// A file inside the harness, such as an extension, runs this aboard by its path.
		if rel := kitItemPath(p, it, scope); !strings.Contains(after[rel], e.bin) {
			t.Errorf("%s doesn't name this aboard, %s", rel, e.bin)
		}
	}

	again := e.run(args...).json(t)
	for _, h := range field(t, again, "harnesses").([]any) {
		for _, c := range h.(map[string]any)["changes"].([]any) {
			if a := c.(map[string]any)["action"]; a != "unchanged" {
				t.Errorf("aboard init again would %v %v", a, c.(map[string]any)["path"])
			}
		}
	}
	if !mapsEqual(goldenFiles(t, e), after) {
		t.Errorf("running aboard init again changed files")
	}

	e.run("uninstall", "--json")
	if left := goldenFiles(t, e); !mapsEqual(left, before) {
		for _, rel := range sortedKeys(left) {
			if left[rel] != before[rel] {
				t.Errorf("after aboard uninstall, %s differs from before aboard init:\n%s", rel, left[rel])
			}
		}
		for _, rel := range sortedKeys(before) {
			if _, ok := left[rel]; !ok {
				t.Errorf("aboard uninstall removed %s, which was there before aboard init", rel)
			}
		}
	}
}

// kitVersions checks aboard init writes only the hook events the installed version of
// the harness runs, reading the version from its command: the oldest version Aboard
// works with, one just older, and one that can't be read (which gets what the oldest
// runs) each get exactly the hooks and fallbacks whose since they have reached.
func kitVersions(t *testing.T, p support.Profile) {
	if !slices.ContainsFunc(p.Delivery.Hooks, func(h support.Hook) bool { return h.Since != "" }) {
		t.Skip("no hook depends on the harness's version")
	}
	oldest := p.Checks.MinVersion
	cases := []struct{ prints, as string }{
		{oldest, oldest},
		{kitBelow(oldest), kitBelow(oldest)},
		{"unknown", oldest},
	}
	for _, c := range cases {
		t.Run(c.prints, func(t *testing.T) {
			e := kitEnv(t, p)
			bin := filepath.Join(e.home, "version-bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, p.Command), []byte("#!/bin/sh\necho '"+c.prints+" ("+p.Name+")'\n"), 0o755); err != nil { //nolint:gosec // a test program
				t.Fatal(err)
			}
			for i, kv := range e.vars {
				if path, ok := strings.CutPrefix(kv, "PATH="); ok {
					e.vars[i] = "PATH=" + bin + string(os.PathListSeparator) + path
				}
			}
			e.run("init", "--yes", "--harness", p.Harness)
			it, _ := p.Item("hooks")
			var file struct {
				Hooks map[string][]struct {
					Hooks []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			content := ""
			if raw, err := os.ReadFile(filepath.Join(e.home, kitItemPath(p, it, "global"))); err == nil {
				content = string(raw)
				if err := json.Unmarshal(raw, &file); err != nil {
					t.Fatal(err)
				}
			}
			var want []string
			for _, h := range p.Delivery.Hooks {
				if support.VersionAtLeast(c.as, h.Since) {
					want = append(want, h.Event+" "+h.Run)
					continue
				}
				for _, f := range h.Fallback {
					if support.VersionAtLeast(c.as, f.Since) {
						want = append(want, f.Event+" "+h.Run)
					}
				}
			}
			var got []string
			for event, groups := range file.Hooks {
				for _, g := range groups {
					for _, entry := range g.Hooks {
						if _, run, ok := strings.Cut(entry.Command, " hook "+p.Harness+" "); ok {
							got = append(got, event+" "+run)
						}
					}
				}
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("%s printing %q: aboard init wrote %q, want %q\n%s", p.Command, c.prints, got, want, content)
			}
		})
	}
}

// kitBelow returns a version just older than v: 2.0.21 for 2.0.22, 0.148.999 for
// 0.149.0.
func kitBelow(v string) string {
	parts := strings.Split(v, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		if n, err := strconv.Atoi(parts[i]); err == nil && n > 0 {
			parts[i] = strconv.Itoa(n - 1)
			for j := i + 1; j < len(parts); j++ {
				parts[j] = "999"
			}
			break
		}
	}
	return strings.Join(parts, ".")
}

// kitCheckHooksFile checks a JSON hooks file holds exactly the profile's hooks, each
// running this aboard, with the person's own entry kept, and the allow rule of a profile
// that puts it there.
func kitCheckHooksFile(t *testing.T, e *env, p support.Profile, content string) {
	t.Helper()
	var file struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(content), &file); err != nil {
		t.Fatalf("the hooks file isn't JSON: %v\n%s", err, content)
	}
	if file.Model != "their own setting" {
		t.Errorf("aboard init dropped the person's own setting")
	}
	marker := " hook " + p.Harness + " "
	found := 0
	personKept := false
	for event, groups := range file.Hooks {
		for _, g := range groups {
			for _, entry := range g.Hooks {
				command, _ := entry["command"].(string)
				if command == "echo their own hook" {
					personKept = true
				}
				if !strings.Contains(command, marker) {
					continue
				}
				found++
				i := slices.IndexFunc(p.Delivery.Hooks, func(h support.Hook) bool {
					return h.Event == event && strings.HasSuffix(command, marker+h.Run)
				})
				if i < 0 {
					t.Errorf("%s runs %q, which the profile doesn't list for that event", event, command)
					continue
				}
				h := p.Delivery.Hooks[i]
				if !strings.Contains(command, e.bin) {
					t.Errorf("%s's hook doesn't run this aboard by its path: %q", event, command)
				}
				if g.Matcher != h.Matcher {
					t.Errorf("%s's hook has matcher %q; the profile says %q", event, g.Matcher, h.Matcher)
				}
				if got, _ := entry["timeout"].(float64); h.Timeout != 0 && int(got) != h.Timeout {
					t.Errorf("%s's hook has timeout %v; the profile says %d", event, entry["timeout"], h.Timeout)
				}
				for k, v := range h.Options {
					if fmt.Sprint(entry[k]) != fmt.Sprint(v) {
						t.Errorf("%s's hook has %s %v; the profile says %v", event, k, entry[k], v)
					}
				}
			}
		}
	}
	if found != len(p.Delivery.Hooks) {
		t.Errorf("the hooks file has %d of Aboard's hooks; the profile lists %d", found, len(p.Delivery.Hooks))
	}
	if !personKept {
		t.Errorf("aboard init dropped the person's own hook")
	}
	if it, ok := p.Item("allow-rule"); ok && it.Global == "" && !slices.Contains(file.Permissions.Allow, it.Rule) {
		t.Errorf("the allow rule %q isn't in the hooks file's permissions.allow", it.Rule)
	}
}

// kitDoctor checks what aboard doctor reports about the harness's hooks: missing before
// init, installed after it, edited once the person changes an entry, and outdated when
// an older aboard at another path wrote them.
func kitDoctor(t *testing.T, p support.Profile) {
	if _, ok := p.Item("file"); ok && len(p.Delivery.Hooks) == 0 {
		kitDoctorFile(t, p)
		return
	}
	if len(p.Delivery.Hooks) == 0 {
		t.Skip("the harness has no hooks")
	}
	e := kitEnv(t, p)
	name := p.CheckName + "_hooks"
	check := func(want string) map[string]any {
		t.Helper()
		c := e.doctorChecks()[name]
		if code, _ := c["code"].(string); code != want {
			t.Fatalf("doctor's %s check: %v, want code %q", name, c, want)
		}
		return c
	}
	missing := check(p.CheckName + "_hooks_missing")
	level := "error"
	if it, _ := p.Item("hooks"); it.Missing != nil && it.Missing.Level != "" {
		level = it.Missing.Level
	}
	if missing["level"] != level {
		t.Errorf("missing hooks are reported at level %v; the profile says %s", missing["level"], level)
	}

	e.run("init", "--yes", "--harness", p.Harness, "--allow-commands")
	if c := check(""); c["level"] != "ok" {
		t.Fatalf("doctor after aboard init: %v", c)
	}
	if c := e.doctorChecks()[p.CheckName+"_skill"]; c["level"] != "ok" {
		t.Errorf("doctor's %s_skill check after aboard init: %v", p.CheckName, c)
	}

	it, _ := p.Item("hooks")
	path := filepath.Join(e.home, kitItemPath(p, it, "global"))
	var file map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &file); err != nil {
		t.Fatal(err)
	}
	h := p.Delivery.Hooks[0]
	entry := file["hooks"].(map[string]any)[h.Event].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	entry["timeout"] = float64(h.Timeout + 1)
	edited, _ := json.MarshalIndent(file, "", "  ")
	if err := os.WriteFile(path, append(edited, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	check("hooks_edited")
	e.run("init", "--yes", "--harness", p.Harness, "--allow-commands")
	check("")

	older := filepath.Join(e.home, "old", "aboard")
	installAt(t, oldBinary, older)
	e.bin = older
	e.run("init", "--yes", "--harness", p.Harness, "--allow-commands")
	e.bin = binary
	check("hooks_outdated")
}

// kitDoctorFile checks what aboard doctor reports about the file Aboard installs inside a
// harness without hooks, such as an extension: missing before init, installed after it,
// edited once the person changes it, and outdated when an older aboard at another path
// wrote it.
func kitDoctorFile(t *testing.T, p support.Profile) {
	e := kitEnv(t, p)
	name := p.CheckName + "_extension"
	check := func(want string) map[string]any {
		t.Helper()
		c := e.doctorChecks()[name]
		if code, _ := c["code"].(string); code != want {
			t.Fatalf("doctor's %s check: %v, want code %q", name, c, want)
		}
		return c
	}
	if c := check(p.CheckName + "_extension_missing"); c["level"] != "error" {
		t.Errorf("a missing extension is reported at level %v, want error: the harness's sessions get no agent", c["level"])
	}
	e.run("init", "--yes", "--harness", p.Harness)
	if c := check(""); c["level"] != "ok" {
		t.Fatalf("doctor after aboard init: %v", c)
	}
	if c := e.doctorChecks()[p.CheckName+"_skill"]; c["level"] != "ok" {
		t.Errorf("doctor's %s_skill check after aboard init: %v", p.CheckName, c)
	}
	it, _ := p.Item("file")
	path := filepath.Join(e.home, kitItemPath(p, it, "global"))
	if err := os.WriteFile(path, []byte(readFile(t, path)+"\n// the person's own line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("extension_edited")
	e.run("init", "--yes", "--harness", p.Harness)
	check("")

	older := filepath.Join(e.home, "old", "aboard")
	installAt(t, oldBinary, older)
	e.bin = older
	e.run("init", "--yes", "--harness", p.Harness)
	e.bin = binary
	if c := check("extension_outdated"); !strings.Contains(c["message"].(string), "written by aboard "+oldVersion) {
		t.Errorf("an outdated extension's message should name the aboard that wrote it: %v", c["message"])
	}
}

// kitIdentity checks a command finds its session where the profile says, and that a
// harness started inside the session is taken for itself, not for this one.
func kitIdentity(t *testing.T, p support.Profile) {
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	if got := field(t, s.run("status", "--json").json(t), "agent"); got != "reviewer" {
		t.Fatalf("in the session, status shows agent %v, want reviewer", got)
	}
	others, err := support.Profiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range others {
		if o.Harness == p.Harness || o.Identity.Kind != "env" || !slices.Contains(p.Identity.YieldsTo, o.Identity.Env) {
			continue
		}
		// o started from a command in this session inherits the session's environment.
		outer := slices.Clone(s.vars)
		for _, m := range p.SessionEnv {
			if m != "ABOARD_SESSION" {
				outer = append(outer, m+"=1")
			}
		}
		inner := e.kitStart(o, kitID(), "startup", outer)
		line := field(t, e.run("invite", "--json").json(t), "join_line").(string)
		inner.run("join", line, "--name", "nested")
		if got := field(t, inner.run("status", "--json").json(t), "agent"); got != "nested" {
			t.Errorf("a %s started inside a %s session acts as %v, want its own agent", o.Name, p.Name, got)
		}
		if got := field(t, s.run("status", "--json").json(t), "agent"); got != "reviewer" {
			t.Errorf("the %s session now acts as %v, want reviewer", p.Name, got)
		}
	}
	if p.Identity.Kind == "hook" {
		// Started from a command of another harness's session, the session-start hook
		// unsets that harness's session variable in this session's environment.
		for _, o := range others {
			if o.Harness == p.Harness || o.Identity.Kind != "env" || !slices.Contains(p.Identity.YieldsTo, o.Identity.Env) {
				continue
			}
			nested := e.kitStart(p, kitID(), "startup", []string{o.Identity.Env + "=019a0000-0000-7000-8000-0000000000ff"})
			if slices.ContainsFunc(nested.vars, func(kv string) bool { return strings.HasPrefix(kv, o.Identity.Env+"=") }) {
				t.Errorf("a %s started from a %s command keeps %s", p.Name, o.Name, o.Identity.Env)
			}
		}
	}
}

// kitHooks checks what each hook does to the session: the agent works after a prompt,
// a waiting hook is released by one, it is idle when the turn ends, and disconnected
// when the session ends. A hook fired inside a subagent changes nothing.
func kitHooks(t *testing.T, p support.Profile) {
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	e.presenceIs("writer-reviewer", "reviewer", "idle", "")
	if s.ext != nil {
		// The extension reports turns and the session's end on its connection.
		s.op("prompt", "")
		e.presenceIs("writer-reviewer", "reviewer", "working", "")
		s.idle()
		e.presenceIs("writer-reviewer", "reviewer", "idle", "")
		s.op("end", "")
		s.ext.closed()
		e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
		return
	}
	if _, ok := p.Hook("prompt"); ok {
		var w *proc
		if p.WaitsForIdle() {
			// Registration already reports idle. Start a turn first, so idle below
			// proves the stop hook reached the daemon before the next prompt.
			if r := s.op("prompt", `"prompt":"before the wait"`); r.code != 0 {
				t.Fatalf("prompt before the wait failed\n%s", r)
			}
			e.presenceIs("writer-reviewer", "reviewer", "working", "")
			w = s.idle()
			e.presenceIs("writer-reviewer", "reviewer", "idle", "")
		}
		if r := s.op("prompt", `"prompt":"work"`); r.code != 0 {
			t.Fatalf("prompt hook failed\n%s", r)
		}
		e.presenceIs("writer-reviewer", "reviewer", "working", "")
		if w != nil {
			if r := w.wait(5 * time.Second); r.code != 0 {
				t.Fatalf("a prompt should release the waiting hook with exit 0\n%s", r)
			}
		}
	}
	if _, ok := p.Hook("tool"); ok {
		if r := s.op("tool", ""); r.code != 0 || strings.TrimSpace(r.stdout) != "" {
			t.Fatalf("a tool hook with nothing waiting should print nothing\n%s", r)
		}
	}
	if f := p.Lifecycle.SubagentField; f != "" {
		for _, h := range p.Delivery.Hooks {
			if h.Op == "mark-subagent" {
				continue
			}
			start := time.Now()
			r := s.e.exec(s.vars, hookInput(s.id, h.Event, `"`+f+`":"019a0000-0000-7000-8000-0000000000aa"`), "hook", p.Harness, h.Run)
			if r.code != 0 || r.stdout != "" || r.stderr != "" || time.Since(start) > 5*time.Second {
				t.Fatalf("the %s hook fired inside a subagent did something:\n%s", h.Op, r)
			}
		}
		if _, ok := p.Hook("prompt"); ok {
			e.presenceIs("writer-reviewer", "reviewer", "working", "")
		}
	}
	if w := s.idle(); w != nil || p.Delivery.Method == "queue" {
		e.presenceIs("writer-reviewer", "reviewer", "idle", "")
	}
	if r := s.op("end", ""); r.code != 0 {
		t.Fatalf("end hook failed\n%s", r)
	}
	e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
}

// kitSubagents checks a marked subagent's commands may read but never act as its parent.
func kitSubagents(t *testing.T, p support.Profile) {
	switch p.SubagentIdentity {
	case "marked":
	case "", "none":
		t.Skip("subagent_identity none: a subagent may act as its parent, which the docs page states")
	default:
		t.Fatalf("the kit has no check for subagent_identity %q", p.SubagentIdentity)
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	// asSubagent runs an aboard command line as one of the session's subagents.
	var asSubagent func(line string) result
	switch {
	case p.Identity.Kind == "extension":
		// The extension marks a subagent's shell command that runs aboard (its own tests
		// prove it does); the command then runs in the session's environment.
		asSubagent = func(line string) result { return s.shell("export ABOARD_SUBAGENT=0-Explore; " + line) }
	case p.Identity.RootEnv != "":
		sub := slices.DeleteFunc(slices.Clone(s.vars), func(kv string) bool { return strings.HasPrefix(kv, p.Identity.Env+"=") })
		sub = append(sub, p.Identity.Env+"="+kitID())
		subSession := &kitSession{session: &session{e: e, harness: p.Harness, id: s.id, vars: sub}, p: p}
		asSubagent = subSession.shell
	default:
		if _, ok := p.Hook("mark-subagent"); !ok {
			t.Fatal("subagent_identity marked, but neither identity.root_env nor a hook of op mark-subagent marks subagents")
		}
		asSubagent = func(line string) result {
			t.Helper()
			in := `"tool_name":"Bash","tool_input":{"command":` + jsonString(line) + `},"` + p.Lifecycle.SubagentField + `":"a1b2c3d4e5"`
			r := s.op("mark-subagent", in)
			var out struct {
				HookSpecificOutput struct {
					UpdatedInput struct {
						Command string `json:"command"`
					} `json:"updatedInput"`
				} `json:"hookSpecificOutput"`
			}
			if r.code != 0 || json.Unmarshal([]byte(r.stdout), &out) != nil || out.HookSpecificOutput.UpdatedInput.Command == "" {
				t.Fatalf("the mark-subagent hook didn't mark a subagent's aboard command\n%s", r)
			}
			return s.shell(out.HookSpecificOutput.UpdatedInput.Command)
		}
		if r := s.op("mark-subagent", `"tool_name":"Bash","tool_input":{"command":"aboard say hello"}`); r.stdout != "" {
			t.Errorf("the mark-subagent hook changed the main conversation's command\n%s", r)
		}
	}
	// A subagent's commands may name the parent's agent, as an inherited ABOARD_AGENT
	// would: reading is still allowed, acting as it isn't.
	if r := asSubagent("aboard read --as reviewer --json"); r.code != 0 {
		t.Errorf("a subagent couldn't read its parent's board\n%s", r)
	}
	r := asSubagent("aboard say --as reviewer --json 'from the subagent'")
	if r.code != 1 || !strings.Contains(r.stdout, `"subagent_without_seat"`) {
		t.Fatalf("a subagent's say wasn't refused with subagent_without_seat\n%s", r)
	}
	if read := e.run("read", "--as", "writer", "--json"); strings.Contains(read.stdout, "from the subagent") {
		t.Fatalf("the subagent posted as its parent:\n%s", read)
	}

	// aboard status says the same in every harness: a subagent of the harness's session,
	// the agent that session holds, and no seat of its own.
	st := asSubagent("aboard status --json")
	if st.code != 0 {
		t.Fatalf("a subagent's status failed\n%s", st)
	}
	var got struct {
		Agent    *string `json:"agent"`
		Subagent *struct {
			Harness     string  `json:"harness"`
			Session     string  `json:"session"`
			ParentAgent *string `json:"parent_agent"`
			Seat        *string `json:"seat"`
		} `json:"subagent"`
	}
	if err := json.Unmarshal([]byte(st.stdout), &got); err != nil {
		t.Fatalf("status --json: %v\n%s", err, st)
	}
	if got.Agent == nil || *got.Agent != "reviewer" || got.Subagent == nil || got.Subagent.Harness != p.Harness ||
		got.Subagent.Session != p.Harness+":"+s.id || got.Subagent.ParentAgent == nil || *got.Subagent.ParentAgent != "reviewer" || got.Subagent.Seat != nil {
		t.Fatalf("a subagent's status should name its parent's session and agent and no seat:\n%s", st.stdout)
	}
	text := asSubagent("aboard status").stdout
	for _, want := range []string{"Subagent: runs in a subagent of " + withArticle(p.Name) + " session", "would act as reviewer", "no seat of its own"} {
		if !strings.Contains(text, want) {
			t.Fatalf("a subagent's status lacks %q:\n%s", want, text)
		}
	}
	if top := e.run("status", "--as", "reviewer", "--json"); strings.Contains(top.stdout, `"subagent": {`) {
		t.Fatalf("status outside a subagent names one:\n%s", top.stdout)
	}
}

// withArticle puts "a" or "an" before a harness's name, as the CLI's text does.
func withArticle(name string) string {
	if strings.ContainsRune("aeiouAEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// kitDelivers skips a delivery check for a harness with no automatic delivery.
func kitDelivers(t *testing.T, p support.Profile) {
	t.Helper()
	if !p.Delivers() {
		t.Skip("no automatic delivery: the skill has the agent run aboard inbox --wait")
	}
}

// kitIdle checks a message reaches an idle session, and is acknowledged once confirmed.
func kitIdle(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	w := s.idle()
	if w != nil && !w.running(300*time.Millisecond) {
		t.Fatalf("the wait hook returned with nothing to deliver\n%s", w.wait(time.Second))
	}
	seq := writerSays(t, e, "Draft is in notes.md.")
	bundle := s.nextBundle(w, 10*time.Second)
	for _, want := range []string{`count="1"`, `from="@writer"`, `seq="` + strconv.Itoa(seq) + `"`, "Draft is in notes.md."} {
		if !strings.Contains(bundle, want) {
			t.Fatalf("the bundle lacks %q:\n%s", want, bundle)
		}
	}
	if p.WaitsForIdle() {
		if n := s.unread(); n != 1 {
			t.Fatalf("acknowledged before the session confirmed it: %d unread", n)
		}
		s.idle() // the woken turn ends: the session's next event confirms the bundle
	}
	eventually(t, 10*time.Second, "the message to be acknowledged", func() bool { return s.unread() == 0 })
}

// kitTurnEnd checks messages sent while a turn runs never reach it, and arrive once it
// ends: one bundle for a harness whose hook waits for idle; each in the harness's own
// queue, which holds them until the turn ends, for one that queues.
func kitTurnEnd(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	if _, ok := p.Hook("prompt"); !ok && !p.Has("extension") {
		t.Skip("no prompt hook: the daemon can't tell a turn runs")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	if r := s.op("prompt", `"prompt":"a long task"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	for _, body := range []string{"one", "two", "three"} {
		writerSays(t, e, "peer note "+body)
	}
	if p.HoldsWhileBusy() {
		if n := s.unread(); n != 3 {
			t.Fatalf("%d unread while the turn runs; want all 3 waiting", n)
		}
		bundle := s.nextBundle(s.idle(), 10*time.Second)
		if !strings.Contains(bundle, `count="3"`) || strings.Index(bundle, "peer note one") > strings.Index(bundle, "peer note three") {
			t.Fatalf("want one bundle of all three, oldest first:\n%s", bundle)
		}
		return
	}
	eventually(t, 10*time.Second, "the harness's queue to take all three", func() bool {
		return strings.Contains(s.queuedText(), "peer note three")
	})
	all := s.queuedText()
	for _, body := range []string{"one", "two", "three"} {
		if strings.Count(all, "peer note "+body) != 1 {
			t.Fatalf("peer note %s was queued %d times:\n%s", body, strings.Count(all, "peer note "+body), all)
		}
	}
	if strings.Index(all, "peer note one") > strings.Index(all, "peer note three") {
		t.Fatalf("queued out of order:\n%s", all)
	}
}

// toolContext runs the session's tool hook and returns what it added to the turn,
// checking it names the hook's own event and never decides on the tool call.
func (s *kitSession) toolContext(t *testing.T) string {
	t.Helper()
	if s.ext != nil {
		return s.ext.boundary()
	}
	h, _ := s.p.Hook("tool")
	r := s.op("tool", "")
	if r.code != 0 {
		t.Fatalf("tool hook failed\n%s", r)
	}
	got := parseToolContext(t, r.stdout)
	if got.text != "" && got.event != h.Event {
		t.Fatalf("the tool hook answered for %q, want the event it ran for, %s", got.event, h.Event)
	}
	return got.text
}

// kitOwnerAtToolBoundary checks the owner's message reaches a busy turn at the next tool
// boundary, in full, while a peer's message never enters the turn.
func kitOwnerAtToolBoundary(t *testing.T, p support.Profile) {
	if !p.Has("tool-boundary") && !p.Has("extension") {
		t.Skip("no tool-boundary capability: the owner's messages wait for the turn's end")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	s.op("prompt", `"prompt":"a long task"`)
	writerSays(t, e, "a peer's note")
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: switch to the docs")
	var text string
	eventually(t, 10*time.Second, "a tool boundary to add the owner's message", func() bool {
		text = s.toolContext(t)
		if strings.Contains(text, "a peer's note") {
			t.Fatalf("a peer's message entered the busy turn:\n%s", text)
		}
		return strings.Contains(text, "owner: switch to the docs")
	})
	if !strings.Contains(text, `sender="owner"`) {
		t.Fatalf("the owner's message isn't marked as the owner's:\n%s", text)
	}
	s.idle()
	if strings.Contains(s.queuedText(), "owner: switch to the docs") {
		t.Fatalf("the owner's message also went into the harness's queue")
	}
}

// kitWaitingNotice checks a tool boundary names a waiting peer's message once, without
// its content, while the message itself waits for the turn's end.
func kitWaitingNotice(t *testing.T, p support.Profile) {
	if !p.Delivery.WaitingNotice {
		t.Skip("the profile declares no waiting notice")
	}
	if !p.HoldsWhileBusy() {
		t.Skip("a peer's message goes straight into the harness's queue, so nothing waits to be named")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	s.op("prompt", `"prompt":"a long task"`)
	seq := writerSays(t, e, "a peer's private detail")
	var notice string
	eventually(t, 10*time.Second, "a tool boundary to name the waiting message", func() bool {
		notice = s.toolContext(t)
		return notice != ""
	})
	if !strings.Contains(notice, "<aboard-notice") || !strings.Contains(notice, "#"+strconv.Itoa(seq)) || strings.Contains(notice, "private detail") {
		t.Fatalf("want a notice naming #%d without its content:\n%s", seq, notice)
	}
	if again := s.toolContext(t); again != "" {
		t.Fatalf("the waiting message was announced twice:\n%s", again)
	}
}

// kitKilledSession checks the daemon closes a session whose harness process was killed,
// and, for a harness whose bundles are confirmed by the session's next event, that the
// bundle the killed session never confirmed goes to the next session for its agent.
func kitKilledSession(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	switch p.Lifecycle.Liveness {
	case "process":
	case "connection":
		kitKilledConnection(t, p)
		return
	default:
		t.Skipf("liveness %q: the kit has no way to kill such a session", p.Lifecycle.Liveness)
	}
	e := kitEnv(t, p)
	first := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, first)
	// The harness process starts the session again, so it is the process the daemon
	// watches: a command run in the session (aboard join) registers the session with the
	// process it runs under, which here is the test.
	s, harnessProc := e.kitStartIn(p, first.id)
	var seq int
	if p.WaitsForIdle() {
		w := s.idle()
		seq = writerSays(t, e, "please review")
		s.nextBundle(w, 10*time.Second)
	}
	if err := harnessProc.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = harnessProc.Wait()
	e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
	if !p.WaitsForIdle() {
		t.Log("the harness's queue confirms a bundle when it takes it, so there is nothing to hand again")
		return
	}
	next := e.kitStart(p, kitID(), "startup", nil)
	next.run("resume", "reviewer")
	bundle := next.nextBundle(next.idle(), 10*time.Second)
	if !strings.Contains(bundle, "please review") || !strings.Contains(bundle, `seq="`+strconv.Itoa(seq)+`"`) {
		t.Fatalf("the unconfirmed bundle should go to the next session, with the same seq:\n%s", bundle)
	}
}

// kitKilledConnection checks, for a harness whose session lives as long as its
// extension's connection, that the connection closing without a goodbye (the harness
// killed) closes the session, and that a bundle sent on it and never confirmed goes to
// the next session for its agent, with the same seq.
func kitKilledConnection(t *testing.T, p support.Profile) {
	e := kitEnv(t, p)
	first := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, first)
	seq := writerSays(t, e, "please review")
	first.ext.deliver(10*time.Second, false)
	_ = first.ext.conn.Close()
	e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
	next := e.kitStart(p, kitID(), "startup", nil)
	next.run("resume", "reviewer")
	bundle := next.nextBundle(next.idle(), 10*time.Second)
	if !strings.Contains(bundle, "please review") || !strings.Contains(bundle, `seq="`+strconv.Itoa(seq)+`"`) {
		t.Fatalf("the unconfirmed bundle should go to the next session, with the same seq:\n%s", bundle)
	}
}

// kitResume checks a session that closed and is resumed with the same id is its agent
// again with no aboard resume, and gets what waited for it.
func kitResume(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)
	var handed int
	if p.WaitsForIdle() {
		w := s.idle()
		handed = writerSays(t, e, "handed before the close")
		s.nextBundle(w, 10*time.Second)
	}
	s.op("end", "")
	e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
	writerSays(t, e, "sent while closed")
	if strings.Contains(s.queuedText(), "sent while closed") {
		t.Fatalf("a closed session's queue was given a bundle")
	}
	if !p.Lifecycle.ResumeKeepsID {
		t.Skip("resuming gives the session a new id, so it can't reconnect by itself")
	}
	back := e.kitStart(p, s.id, "resume", nil)
	back.seen = s.seen
	switch {
	case back.ext != nil:
		if w := back.welcome; !w.Reopened || len(w.Agents) != 1 || w.Agents[0].Name != "reviewer" || w.Mode != "focused" {
			t.Fatalf("the daemon's welcome doesn't say the session is reviewer again: %+v", w)
		}
		if note := back.welcome.Note; !strings.Contains(note, "this session is reviewer on writer-reviewer again") ||
			!strings.HasSuffix(note, " Delivery mode: focused. "+focusedRule) {
			t.Fatalf("the welcome's note doesn't say the session is reviewer again in focused mode: %q", note)
		}
	case !strings.Contains(back.started.stdout, "this session is reviewer on writer-reviewer again"):
		t.Fatalf("the session-start hook didn't say the session is reviewer again\n%s", back.started)
	case !strings.Contains(back.started.stdout, " Delivery mode: focused. "+focusedRule+"\n"):
		t.Fatalf("the session-start hook didn't name the session's delivery mode and its rule\n%s", back.started)
	}
	if got := field(t, back.run("status", "--json").json(t), "agent"); got != "reviewer" {
		t.Fatalf("the resumed session acts as %v, want reviewer", got)
	}
	if p.WaitsForIdle() {
		first := back.nextBundle(back.idle(), 10*time.Second)
		if !strings.Contains(first, "handed before the close") || !strings.Contains(first, `seq="`+strconv.Itoa(handed)+`"`) {
			t.Fatalf("the resumed session should get the bundle it never confirmed again:\n%s", first)
		}
	}
	if got := back.nextBundle(back.idle(), 10*time.Second); !strings.Contains(got, "sent while closed") {
		t.Fatalf("the resumed session should get the message that waited:\n%s", got)
	}
}
