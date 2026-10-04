// Package launchertest is the launcher kit: one suite every launcher passes, built in or
// external, so "does my launcher work?" has an answer that doesn't depend on reading
// Aboard's code. It starts a stand-in for a harness (a shell script that records its
// folder, arguments and environment, then runs until told to stop), so it needs no
// harness and no model. spec/launcher.md lists what it checks.
package launchertest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/launcher"
)

// script is the stand-in harness: it writes what it was started with to the file its
// first argument names, then runs until a file with ".exit" added appears.
const script = `out="$1"; shift
{
  echo "pid=$$"
  echo "pwd=$(pwd -P)"
  for a in "$@"; do echo "arg=$a"; done
  env | grep -E '^(KIT_|ABOARD_)' | sed 's/^/env=/'
} > "$out.tmp" && mv "$out.tmp" "$out"
while [ ! -e "$out.exit" ]; do sleep 0.1; done
`

// within bounds every wait in the kit; a launcher should take a second or two.
const within = 20 * time.Second

// Run checks l against the launcher protocol. Every session it starts runs in a folder
// of the test's own and is stopped when the test ends.
func Run(t *testing.T, l launcher.Launcher) {
	t.Helper()
	ctx := context.Background()
	info, err := l.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name == "" || len(info.Modes) == 0 {
		t.Fatalf("info names no launcher or no modes: %+v", info)
	}
	for _, m := range info.Modes {
		if m != launcher.ModeInteractive && m != launcher.ModeHeadless {
			t.Fatalf("info lists the mode %q; the modes are interactive and headless", m)
		}
	}
	k := &kit{ctx: ctx, t: t, l: l, mode: info.Modes[0], swarm: "aboard-kit-" + random(), dir: shortDir(t)}
	if err := os.WriteFile(filepath.Join(k.dir, "session.sh"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("StartRunsTheCommandWithItsFolderArgumentsAndEnvironment", func(t *testing.T) {
		s := k.start(t, "alpha")
		got := s.recorded(t)
		want := k.dir
		if resolved, err := filepath.EvalSymlinks(want); err == nil {
			want = resolved
		}
		if got["pwd"][0] != want {
			t.Errorf("the session ran in %s, want %s", got["pwd"][0], want)
		}
		if !slices.Equal(got["arg"], kitArgs) {
			t.Errorf("the session got the arguments %q, want %q exactly", got["arg"], kitArgs)
		}
		for name, value := range s.env {
			if !slices.Contains(got["env"], name+"="+value) {
				t.Errorf("the session's environment lacks %s=%s; it has %q", name, value, got["env"])
			}
		}
		if st := k.status(t, s); st != launcher.Running {
			t.Errorf("status of a running session = %s, want running", st)
		}
		k.stop(t, s)
	})

	t.Run("TwoAgentsRunSideBySide", func(t *testing.T) {
		a, b := k.start(t, "beta"), k.start(t, "gamma")
		if a.handle == b.handle {
			t.Fatalf("two sessions got the same handle %q", a.handle)
		}
		a.recorded(t)
		for _, kv := range b.recorded(t)["env"] {
			if strings.HasPrefix(kv, "KIT_ONLY_BETA=") {
				t.Errorf("gamma's session has beta's variable %s: one session's environment leaked into another's", kv)
			}
		}
		if k.status(t, a) != launcher.Running || k.status(t, b) != launcher.Running {
			t.Fatal("both sessions should run")
		}
		k.stop(t, a)
		if st := k.status(t, b); st != launcher.Running {
			t.Errorf("stopping one session changed the other's status to %s", st)
		}
		k.stop(t, b)
	})

	t.Run("SecondStartOfARunningAgentIsRefused", func(t *testing.T) {
		s := k.start(t, "delta")
		s.recorded(t)
		_, err := l.Start(ctx, k.request("delta", s.out+"-2"))
		var e *launcher.Error
		if !errors.As(err, &e) || e.Code != "already_running" {
			t.Errorf("a second start of a running agent: %v, want the code already_running", err)
		}
		k.stop(t, s)
	})

	t.Run("StopEndsTheSessionAndIsIdempotent", func(t *testing.T) {
		s := k.start(t, "epsilon")
		pid := s.pid(t)
		if st, err := l.Stop(ctx, s.ref()); err != nil || st != launcher.Exited {
			t.Fatalf("stop = %s, %v; want exited", st, err)
		}
		waitFor(t, "the stopped session's process to be gone", func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
		if st := k.status(t, s); st != launcher.Exited {
			t.Errorf("status after stop = %s, want exited", st)
		}
		if st, err := l.Stop(ctx, s.ref()); err != nil || st != launcher.Exited {
			t.Errorf("a second stop = %s, %v; want exited and no error", st, err)
		}
	})

	t.Run("StatusSeesTheSessionEndByItself", func(t *testing.T) {
		s := k.start(t, "zeta")
		s.recorded(t)
		if err := os.WriteFile(s.out+".exit", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "status to say the session exited", func() bool { return k.status(t, s) == launcher.Exited })
		k.stop(t, s)
	})

	t.Run("AnAgentStartsAgainAfterItStopped", func(t *testing.T) {
		s := k.start(t, "eta")
		s.recorded(t)
		k.stop(t, s)
		again := k.start(t, "eta")
		again.recorded(t)
		if st := k.status(t, again); st != launcher.Running {
			t.Errorf("status of the restarted session = %s, want running", st)
		}
		k.stop(t, again)
	})
}

// kitArgs are the arguments the kit passes, so a launcher that splits or quotes them
// on the way shows.
var kitArgs = []string{"two words", `a "quoted" one`, "$HOME", "semi;colon", ""}

type kit struct {
	ctx   context.Context
	t     *testing.T
	l     launcher.Launcher
	mode  string
	swarm string
	dir   string
}

type session struct {
	k      *kit
	agent  string
	handle string
	out    string
	env    map[string]string
}

func (k *kit) request(agent, out string) launcher.StartRequest {
	return launcher.StartRequest{
		Swarm: k.swarm, Agent: agent, Harness: "kit", Mode: k.mode,
		Argv: append([]string{"/bin/sh", filepath.Join(k.dir, "session.sh"), out}, kitArgs...),
		Env: map[string]string{
			"KIT_VALUE": "value with spaces and 'quotes'", "ABOARD_AGENT": agent,
			// Only this agent's session may have it.
			"KIT_ONLY_" + strings.ToUpper(agent): "1",
		},
		Dir: k.dir,
	}
}

func (k *kit) start(t *testing.T, agent string) *session {
	t.Helper()
	out := filepath.Join(k.dir, agent+"-"+random()+".out")
	req := k.request(agent, out)
	started, err := k.l.Start(k.ctx, req)
	if err != nil {
		t.Fatalf("start %s: %v", agent, err)
	}
	if started.Handle == "" {
		t.Fatalf("start %s returned no handle", agent)
	}
	s := &session{k: k, agent: agent, handle: started.Handle, out: out, env: req.Env}
	t.Cleanup(func() { _, _ = k.l.Stop(k.ctx, s.ref()) })
	return s
}

func (k *kit) status(t *testing.T, s *session) launcher.State {
	t.Helper()
	st, err := k.l.Status(k.ctx, s.ref())
	if err != nil {
		t.Fatalf("status %s: %v", s.agent, err)
	}
	return st
}

func (k *kit) stop(t *testing.T, s *session) {
	t.Helper()
	if st, err := k.l.Stop(k.ctx, s.ref()); err != nil || st != launcher.Exited {
		t.Fatalf("stop %s = %s, %v; want exited", s.agent, st, err)
	}
}

func (s *session) ref() launcher.Ref {
	return launcher.Ref{Swarm: s.k.swarm, Agent: s.agent, Handle: s.handle}
}

// recorded waits for what the stand-in harness recorded, by key.
func (s *session) recorded(t *testing.T) map[string][]string {
	t.Helper()
	var raw []byte
	waitFor(t, s.agent+"'s session to start and record what it got", func() bool {
		var err error
		raw, err = os.ReadFile(s.out)
		return err == nil
	})
	got := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[k] = append(got[k], v)
	}
	if len(got["pwd"]) != 1 || len(got["pid"]) != 1 {
		t.Fatalf("the session recorded:\n%s", raw)
	}
	return got
}

func (s *session) pid(t *testing.T) int {
	t.Helper()
	pid, err := strconv.Atoi(s.recorded(t)["pid"][0])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", within, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func random() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// shortDir is a folder for the kit's files with a short path, since a launcher may put
// sockets beside them, and socket paths are short.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "aboard-kit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
