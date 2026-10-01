package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

// flags returns a flag set for one command, with --json bound to the app.
func (a *app) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&a.json, "json", a.json, "print one JSON object")
	return fs
}

// parse parses args with fs, allowing flags before or after positional arguments, and
// checks that there are between minPos and maxPos positional arguments (maxPos < 0
// means no limit).
func (a *app) parse(fs *flag.FlagSet, args []string, usage string, minPos, maxPos int) ([]string, error) {
	pos, err := parseInterspersed(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.WriteString(a.env.Stdout, "Usage: "+usage+"\n")
		return nil, errHelpShown
	}
	if err != nil {
		return nil, usageError(sentence(err.Error()), usage)
	}
	switch {
	case len(pos) < minPos:
		return nil, usageError("Missing arguments.", usage)
	case maxPos >= 0 && len(pos) > maxPos:
		return nil, usageError(fmt.Sprintf("Unexpected argument %q.", pos[maxPos]), usage)
	}
	return pos, nil
}

// errHelpShown means -h was given and the usage is already printed.
var errHelpShown = errors.New("help shown")

// parseInterspersed parses flags wherever they appear in args and returns the
// positional arguments in order. Everything after "--" is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...), nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// sentence turns a flag package error into a sentence.
func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// listFlag collects a flag that may be repeated and may hold comma-separated values.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			*l = append(*l, part)
		}
	}
	return nil
}
