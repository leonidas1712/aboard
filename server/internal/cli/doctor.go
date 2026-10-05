package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/harness"
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
	if info, err := a.localInfo(ctx); err == nil {
		if b := infoBuild(info); info.Mode == api.Local && compareBuilds(b, currentBuild()) < 0 {
			checks = append(checks, problem("local_server", levelWarning, "server_outdated",
				"local server at "+srv.URL+" runs aboard "+buildLabel(b)+", older than this aboard "+buildLabel(currentBuild()),
				"run aboard status, or any other command that uses it, which replaces it"))
		} else {
			checks = append(checks, okCheck("local_server", "local server running at "+srv.URL))
		}
	} else if sandbox, ok := a.networkBlocked(); ok {
		checks = append(checks, problem("local_server", levelError, "sandbox_blocks_network",
			"local server at "+srv.URL+" can't be reached from "+sandbox+"'s sandbox, which blocks network access",
			"run "+allowFix+" in a terminal, or approve this command outside the sandbox"))
	} else {
		checks = append(checks, problem("local_server", levelWarning, "server_unreachable",
			"local server not running at "+srv.URL, "run aboard up, or any command that needs it starts it"))
	}
	status, daemonCheck := a.checkDaemon(ctx, p)
	checks = append(checks, daemonCheck...)
	for _, h := range a.registry() {
		checks = append(checks, a.checkHarness(ctx, h)...)
	}
	for _, h := range a.registry() {
		checks = append(checks, a.checkSkill(h)...)
	}
	if status != nil {
		checks = append(checks, a.statusChecks(status)...)
	}
	checks = append(checks, a.checkKeptModes(ctx)...)

	failed := false
	var text strings.Builder
	st := a.out()
	for _, c := range checks {
		switch c.Level {
		case levelOK:
			text.WriteString(st.ok("✓") + " " + c.Message + "\n")
		case levelWarning:
			text.WriteString(st.warn("!") + " " + c.Message + ". " + st.heading("Fix:") + " " + deref(c.Fix) + "\n")
		default:
			failed = true
			text.WriteString(st.bad("✗") + " " + c.Message + ". " + st.heading("Fix:") + " " + deref(c.Fix) + "\n")
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

// checkKeptModes warns about each of this machine's agents whose delivery mode was set
// here before its server held modes (aboard delivery or aboard init --delivery from an
// older aboard) and that its server doesn't have: the server's mode, focused, applies
// now. Moving the mode to the server is up to the agent's person, so the fix is their
// command; nothing uploads it by itself.
func (a *app) checkKeptModes(ctx context.Context) []doctorCheck {
	modes, err := a.journalModes(ctx)
	if err != nil || len(modes) == 0 {
		return nil
	}
	creds, err := a.readCredentials()
	if err != nil {
		return nil
	}
	boards := map[string]int{}
	for _, c := range creds.Agents {
		boards[c.Name]++
	}
	var checks []doctorCheck
	for _, cred := range creds.Agents {
		kept, ok := modes[delivery.AgentRef{Server: cred.Server, Board: cred.Board, Name: cred.Name}]
		if !ok {
			kept, ok = modes[delivery.AgentRef{}]
		}
		kept, parsed := delivery.ParseMode(string(kept))
		if !ok || !parsed {
			continue
		}
		held, ok := a.heldMode(ctx, target{server: a.serverRefFor(cred.Server), board: cred.Board}, cred)
		if !ok || held.Revision > 0 || held.Mode == kept {
			continue
		}
		as := "--as " + cred.Name
		if boards[cred.Name] > 1 {
			as += " --board " + cred.Board
		}
		checks = append(checks, problem("delivery_mode", levelWarning, "delivery_mode_kept_here",
			fmt.Sprintf("%s on %s: this machine kept delivery mode %s for it, which its server doesn't hold, so it is %s now",
				cred.Name, cred.Board, kept, held.Mode),
			fmt.Sprintf("to keep %s, run aboard delivery %s %s in a terminal; to keep %s, run aboard delivery %s %s",
				kept, kept, as, held.Mode, held.Mode, as)))
	}
	return checks
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
	if err != nil {
		if e := asError(err); e.Code == "daemon_in_sandbox" || e.Code == "sandbox_blocks_network" {
			return nil, []doctorCheck{problem("daemon", levelError, e.Code, strings.TrimSuffix(e.Message, "."), e.Hint)}
		}
	}
	if err != nil || resp.Status == nil {
		msg := "delivery daemon not running"
		if err != nil {
			msg += ": " + asError(err).Message
		}
		return nil, []doctorCheck{problem("daemon", levelError, "daemon_not_running", msg,
			"look at the daemon log at "+p.daemonLog())}
	}
	st := resp.Status
	if compareBuilds(st.Build, currentBuild()) < 0 {
		return st, []doctorCheck{problem("daemon", levelWarning, "daemon_outdated",
			fmt.Sprintf("delivery daemon running (pid %d) is from aboard %s, older than this aboard %s, and couldn't be replaced", st.PID, buildLabel(st.Build), buildLabel(currentBuild())),
			"run aboard down; the next command starts the current daemon")}
	}
	return st, []doctorCheck{okCheck("daemon",
		fmt.Sprintf("delivery daemon running (pid %d), %d %s", st.PID, st.OpenSessions, plural(st.OpenSessions, "session", "sessions")))}
}

// hooksMissing returns the events of specs that have no Aboard hook in a harness's hook
// settings file.
func hooksMissing(path, name string, specs []harness.Hook) ([]string, error) {
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
		for _, g := range settings.Hooks[s.Event] {
			for _, h := range g.Hooks {
				found = found || isAboardHook(h.Command, name, s.Arg)
			}
		}
		if !found {
			missing = append(missing, s.Event)
		}
	}
	return missing, nil
}

// checkHarness checks that a harness is there, that its hooks are installed and the
// same as this aboard's, and, when it can't reach Aboard without one, its allow rule.
func (a *app) checkHarness(ctx context.Context, h harness.Harness) []doctorCheck {
	var checks []doctorCheck
	found, installed := h.InstalledChecks(ctx, a.henv())
	for _, c := range found {
		checks = append(checks, fromHarness(c))
	}
	if !installed {
		return checks
	}
	p := h.Profile()
	if len(p.Delivery.Hooks) == 0 {
		// Reached through a file Aboard installs inside it, such as an extension.
		return append(append(checks, a.checkExtension(h)...), a.checkAllow(h, nil)...)
	}
	name := p.CheckName + "_hooks"
	scopes, files, err := a.installedScopes(h, h.Hooks("aboard", harness.Newest))
	if err == nil && len(scopes) > 0 {
		checks = append(checks, a.checkHooksCurrent(name, h, scopes, a.currentHooks(ctx, h),
			okCheck(name, p.Harness+": "+installedText(scopes, files))))
		checks = append(checks, a.checkHookVersion(ctx, h)...)
		return append(checks, a.checkAllow(h, scopes)...)
	}
	level, effect := levelError, ""
	if it, ok := a.item(h, scopeGlobal, harness.ItemHooks); ok && it.Missing != nil {
		if it.Missing.Level != "" {
			level = it.Missing.Level
		}
		if it.Missing.Effect != "" {
			effect = ", " + it.Missing.Effect
		}
	}
	code := p.CheckName + "_hooks_missing"
	missing, err2 := hooksMissing(a.hooksFile(h, scopeGlobal), p.Harness, a.currentHooks(ctx, h))
	if err != nil || err2 != nil {
		checks = append(checks, problem(name, level, code, p.Harness+": "+errors.Join(err, err2).Error(), "fix the file, then run aboard init"))
	} else {
		checks = append(checks, problem(name, level, code,
			p.Harness+": hooks not installed ("+strings.Join(missing, ", ")+")"+effect, "run aboard init"))
	}
	checks = append(checks, a.checkHookVersion(ctx, h)...)
	return append(checks, a.checkAllow(h, nil)...)
}

// checkHookVersion names the hooks aboard init leaves out because the harness's version
// doesn't run them, or because its version can't be read, and what the person loses
// without them. aboard init never writes an event the harness may not know: Claude Code
// before 2.1.101 ignores the whole settings file over one.
func (a *app) checkHookVersion(ctx context.Context, h harness.Harness) []doctorCheck {
	version := h.Version(ctx, a.henv())
	left := h.Unsupported(version)
	if len(left) == 0 {
		return nil
	}
	p := h.Profile()
	var events, losses []string
	need := ""
	for _, s := range left {
		events = append(events, s.Event)
		if s.Without != "" {
			losses = append(losses, s.Without)
		}
		if need == "" || !harness.VersionAtLeast(need, s.Since) {
			need = s.Since
		}
	}
	hooks, them := "hook", "it"
	if len(events) > 1 {
		hooks, them = "hooks", "them"
	}
	effect, unknownEffect := "", ""
	if len(losses) > 0 {
		effect, unknownEffect = ", so "+harness.AndList(losses), "; without "+them+", "+harness.AndList(losses)
	}
	name := p.CheckName + "_version"
	if n := harness.VersionNumber(version); n != "" {
		return []doctorCheck{problem(name, levelWarning, p.CheckName+"_hooks_unsupported",
			fmt.Sprintf("%s: %s %s doesn't run Aboard's %s %s%s", p.Harness, p.Name, n, harness.AndList(events), hooks, effect),
			fmt.Sprintf("update %s to %s or later, then run aboard init", p.Name, need))}
	}
	return []doctorCheck{problem(name, levelWarning, p.CheckName+"_version_unknown",
		fmt.Sprintf("%s: couldn't read %s's version, so aboard init left out Aboard's %s %s, which older versions don't run%s",
			p.Harness, p.Name, harness.AndList(events), hooks, unknownEffect),
		fmt.Sprintf("check that %s prints %s's version, then run aboard init", strings.Join(p.Checks.Installed.Run, " "), p.Name))}
}

// fromHarness turns a harness's own check into a line of aboard doctor.
func fromHarness(c harness.CheckResult) doctorCheck {
	if c.Level == levelOK {
		return okCheck(c.Name, c.Message)
	}
	return problem(c.Name, c.Level, c.Code, c.Message, c.Fix)
}

// installedText says where a harness's hooks are installed.
func installedText(scopes, files []string) string {
	parts := make([]string, len(scopes))
	for i := range scopes {
		parts[i] = scopeText(scopes[i]) + " (" + files[i] + ")"
	}
	return "hooks installed " + strings.Join(parts, " and ")
}

// checkAllow checks, for a harness that can't reach Aboard without its allow rule, that
// a rule it reads allows the aboard command, here or everywhere. Without it, Codex runs
// aboard commands in its sandbox, which by default blocks network access, so they can't
// reach the local server or the daemon. scopes are where the harness's hooks are
// installed, which picks the fix.
func (a *app) checkAllow(h harness.Harness, scopes []string) []doctorCheck {
	req := requiredAllow(h)
	if req == nil {
		return nil
	}
	p := h.Profile()
	name := p.CheckName + "_allow"
	for _, scope := range a.scopes() {
		if file, ok := a.allowedIn(h, scope); ok {
			return []doctorCheck{okCheck(name, p.Harness+": "+req.Allowed+" (allowed in "+file+")")}
		}
	}
	fix := "run " + allowFix
	if slices.Equal(scopes, []string{scopeProject}) {
		fix = "run aboard init --yes --scope project --allow-commands in this project"
	}
	return []doctorCheck{problem(name, levelWarning, p.CheckName+"_aboard_not_allowed", p.Harness+": "+req.NotAllowed, fix)}
}

// statusChecks reports the daemon's servers and the deliveries that need a person.
func (a *app) statusChecks(st *delivery.Status) []doctorCheck {
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
			a.fixFor(ag.Reason)))
	}
	for _, d := range st.Attention {
		checks = append(checks, problem("delivery", levelError, "delivery_attention",
			fmt.Sprintf("1 delivery needs attention: #%s on %s for %s (%s)", seqList(d.Seqs), d.Agent.Board, d.Agent.Name, d.Reason),
			a.fixFor(d.Reason)))
	}
	for _, d := range st.Stalled {
		checks = append(checks, problem("delivery", levelWarning, "delivery_stalled",
			fmt.Sprintf("#%s on %s for %s was handed to its idle session, which started no turn within %s", seqList(d.Seqs), d.Agent.Board, d.Agent.Name, delivery.StallAfter),
			"look at the session: it may be waiting for an answer, or its harness didn't wake; the message isn't sent again, so read it there with aboard read"))
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

// fixFor says how to fix a delivery that stopped for reason: in the words of the
// harness whose adapter gave the reason, if one did.
func (a *app) fixFor(reason string) string {
	for _, h := range a.registry() {
		if fix := h.Fix(reason); fix != "" {
			return fix
		}
	}
	if reason == delivery.ReasonUnauthorized {
		return "the agent's token was rejected; join the board again with aboard join"
	}
	return "look at the daemon log, then run aboard resume in the session to try again"
}
