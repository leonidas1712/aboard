// Package external runs a launcher that lives outside aboard: an aboard-launcher-<name>
// command that reads one JSON request on standard input and answers with one JSON
// response on standard output (spec/launcher.md).
package external

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/launcher"
)

// CallTimeout bounds one call; a launcher that hasn't answered by then is killed.
const CallTimeout = 60 * time.Second

// Prefix is what an external launcher's command is called before its name.
const Prefix = "aboard-launcher-"

// Launcher is an external launcher's command.
type Launcher struct {
	// Name is the launcher's name, as the board file names it.
	Name string
	// Path is the command to run.
	Path string
	// Env is the environment the command runs with; nil means this process's.
	Env []string
}

var _ launcher.Launcher = Launcher{}

// Info asks the launcher what it is.
func (l Launcher) Info(ctx context.Context) (launcher.Info, error) {
	resp, err := l.call(ctx, launcher.Request{Op: launcher.OpInfo})
	if err != nil {
		return launcher.Info{}, err
	}
	return launcher.Info{Name: resp.Name, Modes: resp.Modes}, nil
}

// Start asks the launcher to start a session.
func (l Launcher) Start(ctx context.Context, req launcher.StartRequest) (launcher.Started, error) {
	resp, err := l.call(ctx, launcher.Request{
		Op: launcher.OpStart, Swarm: req.Swarm, Agent: req.Agent, Harness: req.Harness, Mode: req.Mode,
		Argv: req.Argv, Env: req.Env, Dir: req.Dir,
	})
	if err != nil {
		return launcher.Started{}, err
	}
	if resp.Handle == "" {
		return launcher.Started{}, l.broken("answered start with no handle", "")
	}
	return launcher.Started{Handle: resp.Handle, Attach: resp.Attach, PID: resp.PID}, nil
}

// Status asks the launcher whether a session runs.
func (l Launcher) Status(ctx context.Context, ref launcher.Ref) (launcher.State, error) {
	return l.state(ctx, launcher.OpStatus, ref)
}

var _ launcher.Asker = Launcher{}

// Blocked asks the launcher whether a running session waits on the person: its status
// answer's blocked, which a launcher that can't tell leaves out.
func (l Launcher) Blocked(ctx context.Context, ref launcher.Ref) (bool, error) {
	resp, err := l.call(ctx, launcher.Request{Op: launcher.OpStatus, Swarm: ref.Swarm, Agent: ref.Agent, Handle: ref.Handle})
	if err != nil {
		return false, err
	}
	return resp.State == launcher.Running && resp.Blocked, nil
}

// Stop asks the launcher to end a session.
func (l Launcher) Stop(ctx context.Context, ref launcher.Ref) (launcher.State, error) {
	return l.state(ctx, launcher.OpStop, ref)
}

func (l Launcher) state(ctx context.Context, op string, ref launcher.Ref) (launcher.State, error) {
	resp, err := l.call(ctx, launcher.Request{Op: op, Swarm: ref.Swarm, Agent: ref.Agent, Handle: ref.Handle})
	if err != nil {
		return "", err
	}
	switch resp.State {
	case launcher.Running, launcher.Exited, launcher.Unknown:
		return resp.State, nil
	}
	return "", l.broken(fmt.Sprintf("answered %s with the state %q", op, resp.State), "")
}

// call runs the command once with req and reads its answer.
func (l Launcher) call(ctx context.Context, req launcher.Request) (launcher.Response, error) {
	req.V = launcher.ProtocolVersion
	in, err := json.Marshal(req)
	if err != nil {
		return launcher.Response{}, fmt.Errorf("encode the launcher request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.Path) //nolint:gosec // the launcher the person named, from their PATH
	cmd.Env = l.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Stdin = bytes.NewReader(append(in, '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = time.Second
	runErr := cmd.Run()
	errText := strings.TrimSpace(stderr.String())
	if ctx.Err() != nil {
		return launcher.Response{}, l.broken(fmt.Sprintf("didn't answer %s within %s", req.Op, CallTimeout), errText)
	}
	var resp launcher.Response
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		var exit *exec.ExitError
		if runErr != nil && !errors.As(runErr, &exit) {
			return launcher.Response{}, l.broken("couldn't run: "+runErr.Error(), errText)
		}
		return launcher.Response{}, l.broken("answered "+req.Op+" with something that isn't one JSON object", errText)
	}
	if resp.Error != nil {
		e := *resp.Error
		e.Stderr = errText
		return launcher.Response{}, &e
	}
	if runErr != nil {
		return launcher.Response{}, l.broken("answered "+req.Op+" but exited with "+runErr.Error(), errText)
	}
	return resp, nil
}

// broken is the error for a launcher that didn't keep to the protocol.
func (l Launcher) broken(what, stderr string) *launcher.Error {
	return &launcher.Error{
		Code: "launcher_broken", Message: fmt.Sprintf("The launcher %s (%s) %s.", l.Name, l.Path, what),
		Hint: "Run its kit with make launcher-kit LAUNCHER=" + l.Name + " to see what it does differently from spec/launcher.md.", Stderr: stderr,
	}
}
