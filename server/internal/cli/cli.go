// Package cli implements the aboard command: it parses arguments, talks to an Aboard
// server through its public API, keeps this machine's agent credentials and project
// settings on disk, and prints results as text or as one JSON object.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Env is everything a command reads from or writes to the outside world.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Getenv reads an environment variable.
	Getenv func(string) string
	// Dir is the working directory, where the project's .aboard file lives.
	Dir string
	// Executable returns the path of the running aboard binary, used to start the
	// local server in the background.
	Executable func() (string, error)
	// Rand is the source of randomness for idempotency keys.
	Rand io.Reader
	// Terminal is true when standard input and output are a terminal, so a command may
	// ask questions.
	Terminal bool
	// StdoutTerminal and StderrTerminal are true when that stream is a terminal, so text
	// written to it may be colored.
	StdoutTerminal, StderrTerminal bool
	// OpenBrowser opens a URL in the person's browser, failing when no browser could be
	// started.
	OpenBrowser func(ctx context.Context, url string) error
}

// OSEnv returns the environment of the running process.
func OSEnv() Env {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	return Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Dir: dir,
		Executable: os.Executable, Rand: rand.Reader, Terminal: isTerminal(os.Stdin) && isTerminal(os.Stdout),
		StdoutTerminal: isTerminal(os.Stdout), StderrTerminal: isTerminal(os.Stderr),
		OpenBrowser: openBrowser,
	}
}

// isTerminal reports whether f is a character device, such as a terminal.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// app is one invocation of the aboard command.
type app struct {
	env  Env
	json bool
	// started is when the command started.
	started time.Time
	// daemonChecked and localChecked are set once this command has checked the running
	// delivery daemon and local server for an older build, so it checks each only once.
	daemonChecked bool
	localChecked  bool
	// daemonReplaced and localReplaced are set when this command replaced an older
	// daemon or local server, for the commands that say so.
	daemonReplaced *replacement
	localReplaced  *replacement
	// homeAddr is the local server address read from ABOARD_HOME, once looked up.
	homeAddr string
	// harnesses are the harnesses Aboard knows, once listed.
	harnesses harness.Set
}

type command struct {
	name string
	run  func(ctx context.Context, a *app, args []string) error
}

// commands lists every command; their usage and help live in helpTopics.
func commands() []command {
	return []command{
		{"up", runUp},
		{"down", runDown},
		{"pair", runPair},
		{"join", runJoin},
		{"invite", runInvite},
		{"connect", runConnect},
		{"login", runLogin},
		{"keys", runKeys},
		{"say", runSay},
		{"inbox", runInbox},
		{"read", runRead},
		{"react", runReact},
		{"watch", runWatch},
		{"open", runOpen},
		{"logout", runLogout},
		{"status", runStatus},
		{"delivery", runDelivery},
		{"board", runBoard},
		{"boards", runBoards},
		{"audit", runAudit},
		{"resume", runResume},
		{"init", runInit},
		{"doctor", runDoctor},
		{"uninstall", runUninstall},
		{"version", runVersion},
		{"help", runHelp},
		{"serve", runServe},
		{"daemon", runDaemon},
		{"swarm", runSwarm},
		{"hook", runHook},
	}
}

// Run runs the aboard command with args (without the program name) and returns the
// process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	ctx, stop := exitWith(ctx, env.Getenv(exitWithVar))
	defer stop()
	a := &app{env: env, json: wantsJSON(args), started: time.Now()}
	if len(args) == 0 {
		_, _ = io.WriteString(env.Stderr, overviewText(a.errStyles()))
		return exitUsage
	}
	switch args[0] {
	case "-h", "-help", "--help":
		args = append([]string{"help"}, args[1:]...)
	}
	for _, c := range commands() {
		if c.name != args[0] {
			continue
		}
		// "aboard <command> help" reads like a request for help; say where it is
		// rather than running the command with "help" as its argument.
		if rest := slices.DeleteFunc(slices.Clone(args[1:]), isJSONFlag); len(rest) == 1 && rest[0] == "help" && c.name != "help" {
			return a.report(&Error{
				Code: "invalid_request", Message: "aboard " + c.name + " help is not a command.",
				Hint: "Run aboard help " + c.name + " for its usage, flags and examples.", Exit: exitUsage,
			})
		}
		if err := a.refuseInSubagent(c.name, args[1:]); err != nil {
			return a.report(err)
		}
		if !slices.Contains(noLaunchClaim, c.name) {
			a.claimLaunch(ctx)
		}
		return a.report(c.run(ctx, a, args[1:]))
	}
	e := usageError(fmt.Sprintf("%q is not an aboard command.", args[0]), "")
	e.Hint = "Run aboard help to see the commands."
	return a.report(e)
}

// noLaunchClaim are the commands that never hand in the session's launch ticket: the
// ones a harness or a person runs rather than the agent.
var noLaunchClaim = []string{"hook", "daemon", "serve", "help", "version", "up", "down", "swarm"}

// wantsJSON looks for --json before flags are parsed, so even a usage error can be
// printed as JSON.
func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if isJSONFlag(arg) {
			return true
		}
	}
	return false
}

func isJSONFlag(arg string) bool {
	return arg == "--json" || arg == "-json" || arg == "--json=true" || arg == "-json=true"
}

// errCheckFailed means a check ran, failed and already printed its result.
var errCheckFailed = errors.New("check failed")

// report prints err, if any, and returns the exit code.
func (a *app) report(err error) int {
	if err == nil {
		return exitOK
	}
	if errors.Is(err, errCheckFailed) {
		return exitChecked
	}
	if errors.Is(err, errHelpShown) {
		return exitOK
	}
	var hook hookExit
	if errors.As(err, &hook) {
		return int(hook)
	}
	e := asError(err)
	if a.json {
		a.writeJSON(e.wire())
	} else {
		st := a.errStyles()
		// The code is what --json, the docs and the skill name, so text shows it too, quietly.
		msg := st.render(styleBadBold, "Error:") + " " + e.Message + "\n"
		if e.Code != "" {
			msg = st.render(styleBadBold, "Error") + " " + st.dim("("+e.Code+")") + st.render(styleBadBold, ":") + " " + e.Message + "\n"
		}
		if e.Hint != "" {
			msg += st.warn("Hint:") + " " + e.Hint + "\n"
		}
		_, _ = io.WriteString(a.env.Stderr, msg)
	}
	if e.Usage != "" {
		_, _ = io.WriteString(a.env.Stderr, "\n"+a.errStyles().heading("Usage:")+" "+strings.TrimPrefix(e.Usage, "Usage: ")+"\n")
	}
	return e.exitCode()
}

// emit prints a command's result: v as JSON with --json, otherwise text.
func (a *app) emit(v any, text string) {
	if a.json {
		a.writeJSON(v)
		return
	}
	_, _ = io.WriteString(a.env.Stdout, text)
}

func (a *app) writeJSON(v any) {
	enc := json.NewEncoder(a.env.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func runVersion(_ context.Context, a *app, args []string) error {
	fs := a.flags("version")
	if _, err := a.parse(fs, args, usageOf("version"), 0, 0); err != nil {
		return err
	}
	a.emit(currentBuild(), "aboard "+version+"\n")
	return nil
}
