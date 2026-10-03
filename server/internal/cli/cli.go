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
	"strings"
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
	// daemonChecked and localChecked are set once this command has checked the running
	// delivery daemon and local server for an older build, so it checks each only once.
	daemonChecked bool
	localChecked  bool
	// homeAddr is the local server address read from ABOARD_HOME, once looked up.
	homeAddr string
}

type command struct {
	name  string
	usage string
	run   func(ctx context.Context, a *app, args []string) error
}

func commands() []command {
	return []command{
		{"up", "aboard up [--json]", runUp},
		{"down", "aboard down [--json]", runDown},
		{"pair", "aboard pair [template] [--new] [--board NAME] [--name NAME] [--json]", runPair},
		{"join", "aboard join <join-line|code> [--name NAME] [--harness H] [--json]", runJoin},
		{"say", "aboard say <text> [--to T[,T…]] [--reply MSG] [--urgent] [--expect-reply] [--as AGENT] [--board NAME] [--json]", runSay},
		{"inbox", "aboard inbox [--wait SECONDS] [--peek] [--limit N] [--as AGENT] [--board NAME] [--json]", runInbox},
		{"read", readUsage, runRead},
		{"watch", watchUsage, runWatch},
		{"open", openUsage, runOpen},
		{"status", "aboard status [--as AGENT] [--board NAME] [--json]", runStatus},
		{"delivery", "aboard delivery [auto|humans|off] [--as AGENT] [--board NAME] [--json]", runDelivery},
		{"board", "aboard board policy <starter|recommended> [--board NAME] [--json]", runBoard},
		{"audit", "aboard audit verify [--as AGENT] [--board NAME] [--json]", runAudit},
		{"resume", "aboard resume <agent> [--board NAME] [--json]", runResume},
		{"init", "aboard init [--yes] [--scope global|project] [--harness H[,H]] [--delivery auto|humans|off] [--allow-commands] [--json]", runInit},
		{"doctor", "aboard doctor [--json]", runDoctor},
		{"version", "aboard version [--json]", runVersion},
		{"serve", "aboard serve", runServe},
		{"daemon", "aboard daemon [start] [--json]", runDaemon},
		{"hook", "aboard hook <claude-code|codex> <event>", runHook},
	}
}

// usage is the help text listing the commands people use.
func usage() string {
	var b strings.Builder
	b.WriteString("Usage: aboard <command> [flags]\n\nCommands:\n")
	for _, c := range commands() {
		if c.name == "serve" || c.name == "hook" {
			continue
		}
		b.WriteString("  " + c.usage + "\n")
	}
	b.WriteString("  aboard help\n\nFlags may come before or after the other arguments. --json prints one JSON object.\n")
	return b.String()
}

// Run runs the aboard command with args (without the program name) and returns the
// process exit code.
func Run(ctx context.Context, args []string, env Env) int {
	a := &app{env: env, json: wantsJSON(args)}
	if len(args) == 0 {
		_, _ = io.WriteString(env.Stderr, usage())
		return exitUsage
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		_, _ = io.WriteString(env.Stdout, usage())
		return exitOK
	}
	for _, c := range commands() {
		if c.name == args[0] {
			return a.report(c.run(ctx, a, args[1:]))
		}
	}
	return a.report(usageError(fmt.Sprintf("%q is not an aboard command.", args[0]), usage()))
}

// wantsJSON looks for --json before flags are parsed, so even a usage error can be
// printed as JSON.
func wantsJSON(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--json" || arg == "-json" || arg == "--json=true" || arg == "-json=true" {
			return true
		}
	}
	return false
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
		msg := "Error: " + e.Message + "\n"
		if e.Hint != "" {
			msg += "Hint: " + e.Hint + "\n"
		}
		_, _ = io.WriteString(a.env.Stderr, msg)
	}
	if e.Usage != "" {
		_, _ = io.WriteString(a.env.Stderr, "\nUsage: "+strings.TrimPrefix(e.Usage, "Usage: ")+"\n")
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
	if _, err := a.parse(fs, args, "aboard version [--json]", 0, 0); err != nil {
		return err
	}
	a.emit(currentBuild(), "aboard "+version+"\n")
	return nil
}
