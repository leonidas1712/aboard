// Package tmux is the built-in tmux launcher: each swarm gets a tmux server of its own,
// on a socket named after the swarm, so it never touches the person's own tmux server,
// and each agent a window in it, named after the agent. A person watches with
// "tmux -L <swarm> attach".
package tmux

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/launcher"
)

// Name is the launcher's name in the board file.
const Name = "tmux"

// Launcher starts sessions in tmux windows.
type Launcher struct {
	// Command is the tmux program; empty means "tmux" from the PATH.
	Command string
	// Env is the environment tmux runs with, which a new swarm's tmux server, and so
	// every window in it, starts from; nil means this process's.
	Env []string
}

var _ launcher.Launcher = Launcher{}

// Info says the launcher starts interactive sessions.
func (Launcher) Info(context.Context) (launcher.Info, error) {
	return launcher.Info{Name: Name, Modes: []string{launcher.ModeInteractive}}, nil
}

// window is one window of a swarm's tmux server.
type window struct {
	id, name string
	pid      int
	dead     bool
}

// Start opens a window for the agent in the swarm's tmux server, starting the server
// and its session (named after the swarm) with the first window.
func (l Launcher) Start(ctx context.Context, req launcher.StartRequest) (launcher.Started, error) {
	if err := check(req); err != nil {
		return launcher.Started{}, err
	}
	wins, _ := l.windows(ctx, req.Swarm)
	if i := slices.IndexFunc(wins, func(w window) bool { return w.name == req.Agent && !w.dead }); i >= 0 {
		return launcher.Started{}, launcher.Errorf("already_running", "Stop it first with aboard swarm down "+req.Agent+".",
			"%s already runs in window %s of tmux server %s.", req.Agent, wins[i].id, req.Swarm)
	}
	args := []string{"new-window", "-d", "-t", req.Swarm + ":", "-n", req.Agent, "-c", req.Dir, "-P", "-F", "#{window_id} #{pane_pid}"}
	if len(wins) == 0 {
		args = []string{"new-session", "-d", "-s", req.Swarm, "-n", req.Agent, "-c", req.Dir, "-x", "200", "-y", "50", "-P", "-F", "#{window_id} #{pane_pid}"}
	}
	// The variables go to the window's program through env, not tmux's -e: on
	// new-session, -e sets them for the whole tmux session, so every later window would
	// inherit the first agent's identity.
	args = append(args, "--", "env")
	for _, k := range sortedKeys(req.Env) {
		args = append(args, k+"="+req.Env[k])
	}
	args = append(args, req.Argv...)
	out, err := l.run(ctx, req.Swarm, args...)
	if err != nil {
		return launcher.Started{}, err
	}
	id, pid, _ := strings.Cut(strings.TrimSpace(out), " ")
	n, _ := strconv.Atoi(pid)
	return launcher.Started{
		Handle: req.Swarm + "/" + id, PID: n,
		Attach: "tmux -L " + req.Swarm + " attach -t " + req.Swarm + ":" + req.Agent,
	}, nil
}

// Status reports whether the window's program still runs.
func (l Launcher) Status(ctx context.Context, ref launcher.Ref) (launcher.State, error) {
	w, ok, err := l.find(ctx, ref)
	switch {
	case err != nil:
		return "", err
	case !ok, w.dead:
		return launcher.Exited, nil
	}
	return launcher.Running, nil
}

// Stop closes the window, which hangs up its program as closing a terminal does, and
// waits until the program is gone. The tmux server exits by itself once its last window
// closes.
func (l Launcher) Stop(ctx context.Context, ref launcher.Ref) (launcher.State, error) {
	w, ok, err := l.find(ctx, ref)
	if err != nil {
		return "", err
	}
	if !ok {
		return launcher.Exited, nil
	}
	_, _ = l.run(ctx, ref.Swarm, "kill-window", "-t", w.id)
	if w.pid > 0 && !gone(ctx, w.pid, 5*time.Second) {
		_ = syscall.Kill(w.pid, syscall.SIGKILL)
		gone(ctx, w.pid, 5*time.Second)
	}
	return launcher.Exited, nil
}

// find returns the window a handle names.
func (l Launcher) find(ctx context.Context, ref launcher.Ref) (window, bool, error) {
	swarm, id, ok := strings.Cut(ref.Handle, "/")
	if !ok || swarm == "" || !strings.HasPrefix(id, "@") {
		return window{}, false, launcher.Errorf("invalid_request", "Pass the handle start returned.", "%q is not a tmux launcher handle.", ref.Handle)
	}
	wins, err := l.windows(ctx, swarm)
	if err != nil {
		return window{}, false, err
	}
	i := slices.IndexFunc(wins, func(w window) bool { return w.id == id })
	if i < 0 {
		return window{}, false, nil
	}
	return wins[i], true, nil
}

// windows lists the windows of a swarm's tmux server: none when the server isn't
// running.
func (l Launcher) windows(ctx context.Context, swarm string) ([]window, error) {
	// Spaces between the fields: names have none, and tmux without a UTF-8 locale writes a
	// tab as another character.
	out, err := l.run(ctx, swarm, "list-windows", "-a", "-F", "#{window_id} #{window_name} #{pane_pid} #{pane_dead}")
	if err != nil {
		var e *launcher.Error
		if errors.As(err, &e) && e.Code == "host_unavailable" {
			return nil, err
		}
		return nil, nil // no server for this swarm
	}
	var wins []window
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			continue
		}
		pid, _ := strconv.Atoi(f[2])
		wins = append(wins, window{id: f[0], name: f[1], pid: pid, dead: f[3] == "1"})
	}
	return wins, nil
}

// run runs tmux on the swarm's own socket.
func (l Launcher) run(ctx context.Context, swarm string, args ...string) (string, error) {
	command := l.Command
	if command == "" {
		command = "tmux"
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", launcher.Errorf("host_unavailable", "Install tmux (brew install tmux, or apt install tmux), or use another launcher.",
			"tmux isn't installed: %s isn't on the PATH.", command)
	}
	cmd := exec.CommandContext(ctx, path, append([]string{"-L", swarm}, args...)...) //nolint:gosec // tmux, with arguments aboard built
	env := l.Env
	if env == nil {
		env = os.Environ()
	}
	// Inside a tmux session, TMUX names the person's own server; the swarm's is chosen by -L.
	cmd.Env = slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, "TMUX=") })
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", launcher.Errorf("start_failed", "Run the same tmux command in a terminal to see why.",
			"tmux %s failed: %s", args[0], strings.TrimSpace(stderr.String()+" "+err.Error()))
	}
	return stdout.String(), nil
}

// check refuses a start request missing what tmux needs.
func check(req launcher.StartRequest) error {
	switch {
	case req.Swarm == "" || req.Agent == "" || len(req.Argv) == 0 || req.Dir == "":
		return launcher.Errorf("invalid_request", "Send swarm, agent, argv and dir.", "A start request needs a swarm, an agent, a command and a folder.")
	case req.Mode != launcher.ModeInteractive:
		return launcher.Errorf("mode_unsupported", "Use the headless launcher for headless agents.", "The tmux launcher starts only interactive sessions, not %q.", req.Mode)
	}
	return nil
}

// gone waits until a process has exited, for at most d, and reports whether it has.
func gone(ctx context.Context, pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
