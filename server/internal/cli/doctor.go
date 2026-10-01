package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/codex"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
)

// Check levels.
const (
	levelOK      = "ok"
	levelWarning = "warning"
	levelError   = "error"
)

// doctorCheck is one line of aboard doctor.
type doctorCheck struct {
	Name    string  `json:"name"`
	Level   string  `json:"level"`
	Code    *string `json:"code"`
	Message string  `json:"message"`
	Fix     *string `json:"fix"`
}

func okCheck(name, message string) doctorCheck {
	return doctorCheck{Name: name, Level: levelOK, Message: message}
}

func problem(name, level, code, message, fix string) doctorCheck {
	return doctorCheck{Name: name, Level: level, Code: &code, Message: message, Fix: &fix}
}

// runDoctor checks each part of delivery and prints one line per check, with a fix for
// each problem. It exits 3 when any check is an error.
func runDoctor(ctx context.Context, a *app, args []string) error {
	flags := a.flags("doctor")
	if _, err := a.parse(flags, args, "aboard doctor [--json]", 0, 0); err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	var checks []doctorCheck
	srv := a.localServer()
	if a.localRunning(ctx) {
		checks = append(checks, okCheck("local_server", "local server running at "+srv.URL))
	} else {
		checks = append(checks, problem("local_server", levelWarning, "server_unreachable",
			"local server not running at "+srv.URL, "run aboard up, or any command that needs it starts it"))
	}
	status, daemonCheck := a.checkDaemon(ctx, p)
	checks = append(checks, daemonCheck...)
	checks = append(checks, a.checkClaudeHooks())
	checks = append(checks, a.checkCodex(ctx)...)
	if status != nil {
		checks = append(checks, statusChecks(status)...)
	}

	failed := false
	var text strings.Builder
	for _, c := range checks {
		switch c.Level {
		case levelOK:
			text.WriteString("✓ " + c.Message + "\n")
		case levelWarning:
			text.WriteString("! " + c.Message + ". Fix: " + deref(c.Fix) + "\n")
		default:
			failed = true
			text.WriteString("✗ " + c.Message + ". Fix: " + deref(c.Fix) + "\n")
		}
	}
	a.emit(struct {
		Checks []doctorCheck `json:"checks"`
	}{checks}, text.String())
	if failed {
		return errCheckFailed
	}
	return nil
}

// checkDaemon starts the daemon if needed and asks it for its status.
func (a *app) checkDaemon(ctx context.Context, p paths) (*delivery.Status, []doctorCheck) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, []doctorCheck{problem("daemon", levelError, "peer_check_unavailable",
			"this system doesn't report a socket peer's user, so delivery is refused here",
			"use aboard inbox --wait in each session instead")}
	}
	if err := control.SelfTest(); err != nil {
		return nil, []doctorCheck{problem("daemon", levelError, "peer_check_unavailable",
			"the kernel didn't report a socket peer's user ("+err.Error()+"), so delivery is refused here",
			"use aboard inbox --wait in each session instead")}
	}
	for _, dir := range []string{p.state, filepath.Dir(p.socket())} {
		if err := control.CheckDir(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, []doctorCheck{problem("daemon", levelError, "socket_unsafe",
				"the delivery daemon's socket directory can be opened by others: "+err.Error(),
				"remove "+dir+"; it is created again with the right permissions")}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpStatus})
	if err != nil || resp.Status == nil {
		msg := "delivery daemon not running"
		if err != nil {
			msg += ": " + asError(err).Message
		}
		return nil, []doctorCheck{problem("daemon", levelError, "daemon_not_running", msg,
			"look at the daemon log at "+p.daemonLog())}
	}
	st := resp.Status
	return st, []doctorCheck{okCheck("daemon",
		fmt.Sprintf("delivery daemon running (pid %d), %d %s", st.PID, st.OpenSessions, plural(st.OpenSessions, "session", "sessions")))}
}

// hooksMissing returns the events of specs that have no Aboard hook in a harness's hook
// settings file.
func hooksMissing(path, harness string, specs []hookSpec) ([]string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}
	var missing []string
	for _, s := range specs {
		found := false
		for _, g := range settings.Hooks[s.event] {
			for _, h := range g.Hooks {
				found = found || isAboardHook(h.Command, harness, s.arg)
			}
		}
		if !found {
			missing = append(missing, s.event)
		}
	}
	return missing, nil
}

func (a *app) checkClaudeHooks() doctorCheck {
	path := filepath.Join(a.env.Getenv("HOME"), ".claude", "settings.json")
	missing, err := hooksMissing(path, "claude-code", claudeHooks("aboard"))
	switch {
	case err != nil:
		return problem("claude_hooks", levelError, "claude_hooks_missing", "claude-code: "+err.Error(), "fix the file, then run aboard init")
	case len(missing) > 0:
		return problem("claude_hooks", levelError, "claude_hooks_missing",
			"claude-code: hooks not installed ("+strings.Join(missing, ", ")+")", "run aboard init")
	}
	return okCheck("claude_hooks", "claude-code: hooks installed in "+path)
}

func (a *app) checkCodex(ctx context.Context) []doctorCheck {
	if _, err := exec.LookPath("codex"); err != nil {
		return []doctorCheck{problem("codex", levelWarning, "codex_not_installed",
			"codex: not on the PATH", "install Codex, or ignore this if you don't use it")}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cx := &codex.Adapter{}
	ver, err := cx.Version(ctx)
	if err != nil {
		return []doctorCheck{problem("codex", levelWarning, "codex_not_installed",
			"codex: "+err.Error(), "reinstall Codex, or ignore this if you don't use it")}
	}
	if !cx.HasQueue(ctx) {
		return []doctorCheck{problem("codex", levelError, "codex_queue_missing",
			ver+": no queue command, so messages can't be delivered to Codex", "update Codex")}
	}
	checks := []doctorCheck{okCheck("codex", ver+": queue available")}
	codexHome := a.env.Getenv("CODEX_HOME")
	if !filepath.IsAbs(codexHome) {
		codexHome = filepath.Join(a.env.Getenv("HOME"), ".codex")
	}
	path := filepath.Join(codexHome, "hooks.json")
	missing, err := hooksMissing(path, "codex", codexHooks("aboard"))
	switch {
	case err != nil:
		checks = append(checks, problem("codex_hooks", levelWarning, "codex_hooks_missing", "codex: "+err.Error(), "fix the file, then run aboard init"))
	case len(missing) > 0:
		checks = append(checks, problem("codex_hooks", levelWarning, "codex_hooks_missing",
			"codex: hooks not installed ("+strings.Join(missing, ", ")+"), so urgent messages wait for the end of a turn",
			"run aboard init"))
	default:
		checks = append(checks, okCheck("codex_hooks", "codex: hooks installed in "+path))
	}
	return checks
}

// statusChecks reports the daemon's servers and the deliveries that need a person.
func statusChecks(st *delivery.Status) []doctorCheck {
	var checks []doctorCheck
	for _, s := range st.Servers {
		switch s.Problem {
		case "login_missing":
			checks = append(checks, problem("server", levelError, "login_missing",
				"no login for "+s.URL+", where agents on this machine are bound", "run aboard connect for that server"))
		case "server_unreachable":
			checks = append(checks, problem("server", levelError, "server_unreachable",
				s.URL+" doesn't answer, and agents on this machine are bound there", "check the server or the network"))
		default:
			if s.Connected {
				checks = append(checks, okCheck("server", "following "+s.URL))
			}
		}
	}
	for _, ag := range st.Agents {
		checks = append(checks, problem("delivery", levelError, "delivery_attention",
			fmt.Sprintf("deliveries for %s on %s stopped (%s)", ag.Agent.Name, ag.Agent.Board, ag.Reason),
			fixFor(ag.Reason)))
	}
	for _, d := range st.Attention {
		checks = append(checks, problem("delivery", levelError, "delivery_attention",
			fmt.Sprintf("1 delivery needs attention: #%s on %s for %s (%s)", seqList(d.Seqs), d.Agent.Board, d.Agent.Name, d.Reason),
			fixFor(d.Reason)))
	}
	if n := len(st.Skipped); n > 0 {
		var where []string
		for _, d := range st.Skipped {
			where = append(where, fmt.Sprintf("#%s on %s for %s", seqList(d.Seqs), d.Agent.Board, d.Agent.Name))
		}
		checks = append(checks, problem("delivery", levelWarning, "delivery_skipped",
			fmt.Sprintf("%d %s too large to deliver automatically: %s", n, plural(n, "message", "messages"), strings.Join(where, ", ")),
			"read them with aboard read"))
	}
	return checks
}

func seqList(seqs []int) string {
	parts := make([]string, 0, len(seqs))
	for _, s := range seqs {
		parts = append(parts, fmt.Sprint(s))
	}
	return strings.Join(parts, ", #")
}

// fixFor says how to fix a delivery that stopped for reason.
func fixFor(reason string) string {
	switch reason {
	case delivery.ReasonTargetAbsent:
		return "open that Codex thread again, or rejoin with aboard join"
	case delivery.ReasonSubAgent:
		return "run aboard resume from the root Codex conversation"
	case delivery.ReasonUnauthorized:
		return "the agent's token was rejected; join the board again with aboard join"
	}
	return "look at the daemon log, then run aboard resume in the session to try again"
}
