package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// turnTimeout bounds one headless turn.
const turnTimeout = 30 * time.Minute

// inboxWait is how long the runner holds one request for the agent's inbox; presence is
// renewed between them, well within the 3 minutes after which it runs out.
const inboxWait = 60

// runnerArgv is the command the headless launcher runs for an agent: this aboard's
// swarm runner.
func runnerArgv(exe, swarm string, spec swarmSpec, fresh bool) []string {
	argv := []string{exe, "swarm", "runner", "--harness", spec.Harness, "--swarm", swarm}
	if spec.Model != "" {
		argv = append(argv, "--model", spec.Model)
	}
	if spec.Prompt != nil {
		argv = append(argv, "--prompt", *spec.Prompt)
	}
	if fresh {
		argv = append(argv, "--fresh")
	}
	return append(append(argv, "--"), spec.Args...)
}

// runnerSessionPath is where the runner keeps the harness session its turns resume.
func (a *app) runnerSessionPath(swarm, agent string) (string, error) {
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.swarms(), "headless", swarm, agent+".session"), nil
}

// headlessSessionSaved reports whether an agent's runner has a session to resume.
func headlessSessionSaved(a *app, swarm, agent string) bool {
	path, err := a.runnerSessionPath(swarm, agent)
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	return err == nil && strings.TrimSpace(string(raw)) != ""
}

// runSwarmRunner runs headless turns for the agent ABOARD_AGENT names until it is
// stopped: it waits on the agent's inbox, and when a message that concerns the agent
// arrives (the rule of the focused delivery mode), runs one non-interactive turn of the
// harness with every unread message as the prompt, resuming the same harness session
// each time, then acknowledges what the turn was given. The headless launcher starts it.
func runSwarmRunner(ctx context.Context, a *app, args []string) error {
	fls := a.flags("swarm runner")
	harnessName := fls.String("harness", "", "the harness to run")
	swarm := fls.String("swarm", "", "the swarm, which names where the session is kept")
	model := fls.String("model", "", "the model")
	prompt := fls.String("prompt", "", "the first prompt")
	fresh := fls.Bool("fresh", false, "start a new harness session instead of resuming the last one")
	extra, err := a.parse(fls, args, swarmUsage, 0, -1)
	if err != nil {
		return err
	}
	promptSet := false
	fls.Visit(func(f *flag.Flag) { promptSet = promptSet || f.Name == "prompt" })
	h, ok := a.registry().Get(*harnessName)
	if !ok {
		return usageError(fmt.Sprintf("%q is not a harness: use %s.", *harnessName, strings.Join(a.registry().Names(), ", ")), swarmUsage)
	}
	prof := h.Profile()
	if prof.Headless.Via != "native" || len(prof.Headless.Run) == 0 {
		return newError("headless_unsupported",
			fmt.Sprintf("%s can't run headless turns: its profile runs them through %s, which the headless launcher doesn't drive.", prof.Name, viaText(prof.Headless.Via)),
			"Start it with an interactive launcher, such as tmux or herdr.")
	}
	t, cred, err := a.agentTarget(ctx, "", "")
	if err != nil {
		return err
	}
	if *swarm == "" {
		p, err := a.paths()
		if err != nil {
			return err
		}
		*swarm = swarmName(t.server.URL, p.state, t.board)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	c, err := a.client(ctx, t.server, cred.Token, time.Duration(inboxWait)*time.Second+requestTimeout)
	if err != nil {
		return err
	}
	path, err := a.runnerSessionPath(*swarm, cred.Name)
	if err != nil {
		return err
	}
	r := &runner{
		a: a, c: c, prof: prof, board: t.board, agent: cred.Name, path: path,
		spec: swarmSpec{Name: cred.Name, Harness: *harnessName, Model: *model, Args: extra},
	}
	if *fresh {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("forget the last session: %w", err)
		}
	}
	r.logf("runner started for %s on %s (%s)", cred.Name, t.board, prof.Name)
	if !headlessSessionSaved(a, *swarm, cred.Name) {
		first := *prompt
		if !promptSet {
			first = firstPrompt(swarmSpec{Name: cred.Name}, t.board, false)
		}
		if first != "" {
			r.presence(ctx, api.PresenceWorking)
			if err := r.turn(ctx, first); err != nil {
				r.logf("first turn failed: %v", err)
			}
		}
	}
	err = r.loop(ctx)
	r.presence(context.WithoutCancel(ctx), api.PresenceNoSession)
	r.logf("runner stopped")
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// runner runs one agent's headless turns.
type runner struct {
	a           *app
	c           *client
	prof        *harness.Profile
	board       string
	agent, path string
	spec        swarmSpec
}

func (r *runner) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(r.a.env.Stderr, "%s aboard swarm runner: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

// presence reports the agent's presence; the runner delivers as focused mode does.
func (r *runner) presence(ctx context.Context, p api.Presence) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	mode := api.DeliveryMode(delivery.ModeFocused)
	if _, err := r.c.api.SetPresenceWithResponse(ctx, &api.SetPresenceParams{}, api.SetPresenceJSONRequestBody{Presence: p, Delivery: &mode}); err != nil {
		r.logf("couldn't report presence: %v", err)
	}
}

// loop waits on the inbox and runs a turn for each batch that concerns the agent, until
// ctx ends. Messages that don't concern it wait for the next turn, as in focused mode.
func (r *runner) loop(ctx context.Context) error {
	quietUpTo := 0
	failures := 0
	for ctx.Err() == nil {
		r.presence(ctx, api.PresenceIdle)
		wait := inboxWait
		params := &api.GetInboxParams{Wait: &wait, Limit: ptrTo(1)}
		if quietUpTo > 0 {
			params.After = &quietUpTo
		}
		res, err := r.c.api.GetInboxWithResponse(ctx, params)
		if ctx.Err() != nil {
			return fmt.Errorf("stopped: %w", ctx.Err())
		}
		if err != nil || res.JSON200 == nil {
			r.logf("couldn't read the inbox: %v", errText(err))
			pause(ctx, 5*time.Second)
			continue
		}
		if len(res.JSON200.Messages) == 0 {
			continue
		}
		all, err := r.c.api.GetInboxWithResponse(ctx, &api.GetInboxParams{})
		if err != nil || all.JSON200 == nil || len(all.JSON200.Messages) == 0 {
			continue
		}
		msgs := all.JSON200.Messages
		concerns := false
		for _, m := range msgs {
			concerns = concerns || delivery.Concerns(textMessage(m), r.agent)
		}
		last := msgs[len(msgs)-1].Seq
		if !concerns {
			quietUpTo = last
			continue
		}
		r.presence(ctx, api.PresenceWorking)
		if err := r.turn(ctx, bundleText(r.board, msgs)); err != nil {
			failures++
			r.logf("turn failed (%d in a row), trying again: %v", failures, err)
			pause(ctx, time.Duration(min(failures, 6))*10*time.Second)
			continue
		}
		failures, quietUpTo = 0, 0
		actx, cancel := context.WithTimeout(ctx, requestTimeout)
		if _, err := r.c.api.AckInboxWithResponse(actx, &api.AckInboxParams{}, api.AckInboxJSONRequestBody{UpTo: last}); err != nil {
			r.logf("couldn't acknowledge up to #%d: %v", last, err)
		}
		cancel()
	}
	return fmt.Errorf("stopped: %w", ctx.Err())
}

// turn runs one headless turn with prompt, resuming the saved session, and saves the
// session the harness reports.
func (r *runner) turn(ctx context.Context, prompt string) error {
	raw, _ := os.ReadFile(filepath.Clean(r.path))
	session := strings.TrimSpace(string(raw))
	base := r.prof.Headless.Run
	if session != "" && len(r.prof.Headless.Resume) > 0 {
		base = r.prof.Headless.Resume
	}
	argv := harnessArgv(base, r.prof.Headless.Model, r.spec, session, prompt)
	ctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the harness's command line from its profile
	// The runner hands the turn its messages, so Aboard's hooks in the harness do nothing.
	cmd.Env = append(os.Environ(), headlessEnv+"=1", "ABOARD_AGENT="+r.agent)
	var stdout bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, r.a.env.Stdout)
	cmd.Stderr = r.a.env.Stderr
	r.logf("turn: %s (%d bytes of prompt)", argv[0], len(prompt))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", argv[0], err)
	}
	if id := sessionFrom(stdout.Bytes(), r.prof.Headless.SessionField); id != "" && id != session {
		if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
			return fmt.Errorf("keep the session: %w", err)
		}
		if err := os.WriteFile(r.path, []byte(id+"\n"), 0o600); err != nil {
			return fmt.Errorf("keep the session: %w", err)
		}
	}
	return nil
}

// sessionFrom reads the session id from a headless run's output: the field of the JSON
// object it printed, or of the last line that is one.
func sessionFrom(out []byte, field string) string {
	if field == "" {
		return ""
	}
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	candidates := append([][]byte{bytes.TrimSpace(out)}, lines...)
	for i := len(candidates) - 1; i >= 0; i-- {
		var obj map[string]any
		if json.Unmarshal(candidates[i], &obj) != nil {
			continue
		}
		if id, ok := obj[field].(string); ok && sessionIDPattern.MatchString(id) {
			return id
		}
	}
	return ""
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
