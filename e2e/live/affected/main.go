// Command affected decides which harnesses make live-affected runs the live suite for,
// from the files changed against a base (origin/main unless -base names another). It
// explains its decision on standard error, and prints the plan on standard output for
// the Makefile: "none", "all", or the harnesses to run, separated by commas, which
// make live takes as HARNESS. A harness picked this way runs its own scenarios and
// every cross-harness pair it is in.
//
// Run it on its own to see the decision without running anything:
//
//	go run ./e2e/live/affected -base origin/main
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// rules map a changed file to what the live suite must run for it. The first rule
// whose pattern matches decides. A pattern is a path prefix ending in "/", an exact
// path, "*.md" or "*_test.go" (any file with that ending), or a prefix with "<h>" in
// it, which matches a folder named after a harness.
//
// Keep this table short and readable: it is the whole mapping, and
// affected_test.go checks it.
var rules = []rule{
	// The skill is Markdown, but every agent in every scenario follows it.
	{"skills/", runAll, "the Aboard skill, which every agent in the suite follows"},
	{"*.md", runNone, "Markdown"},
	{"docs/", runNone, "the docs"},
	{"design/", runNone, "the design records"},
	{"web/", runNone, "the board UI, which no live scenario drives"},
	{"examples/", runNone, "the examples, which make e2e runs"},
	{".github/", runNone, "the CI config"},
	{"e2e/live/support.json", runNone, "the results a live run writes"},
	{"e2e/live/affected/", runNone, "this selector, which go test checks"},
	{"e2e/live/", runAll, "the live suite itself"},
	// The live suite reads the profiles and its results through e2e/support.
	{"e2e/support/", runAll, "the support code the live suite uses"},
	{"e2e/", runNone, "the e2e tests and their fakes, which make check runs"},
	{"*_test.go", runNone, "a test, which changes nothing the live suite runs"},
	{"adapters/<h>/", runHarness, "<h>'s adapter"},
	{"server/internal/harness/<h>/", runHarness, "<h>'s harness code"},
	{"server/internal/delivery/", runAll, "delivery: the daemon, hooks and control socket"},
	{"server/internal/cli/", runAll, "the CLI, which runs the hooks and the delivery commands"},
	{"server/", runAll, "the server: its API, store and harness registry"},
	{"", runAll, "a file no rule names, so everything runs to be safe"},
}

// rule is one row of the mapping.
type rule struct {
	pattern string
	runs    runs
	why     string
}

// runs is what a rule asks the live suite to run.
type runs int

const (
	runNone    runs = iota // nothing
	runAll                 // every harness
	runHarness             // the harness the file's path names, and its cross-harness pairs
)

// match reports whether path matches the rule's pattern, and for a "<h>" pattern, the
// harness it names, one of harnesses.
func (r rule) match(path string, harnesses []string) (harness string, ok bool) {
	switch {
	case r.pattern == "":
		return "", true
	case strings.HasPrefix(r.pattern, "*"):
		return "", strings.HasSuffix(path, r.pattern[1:])
	case strings.Contains(r.pattern, "<h>"):
		before, after, _ := strings.Cut(r.pattern, "<h>")
		rest, found := strings.CutPrefix(path, before)
		if !found {
			return "", false
		}
		dir, _, found := strings.Cut(rest, "/")
		if !found || !strings.HasPrefix(rest[len(dir):], after) {
			return "", false
		}
		for _, h := range harnesses {
			// A Go package can't have a dash in its name: claude-code's is claudecode.
			if dir == h || dir == strings.ReplaceAll(h, "-", "") {
				return h, true
			}
		}
		return "", false
	case strings.HasSuffix(r.pattern, "/"):
		return "", strings.HasPrefix(path, r.pattern)
	}
	return "", path == r.pattern
}

// reason is why one changed file asks for what it does.
type reason struct {
	file    string
	runs    runs
	harness string
	why     string
}

// plan is what the live suite runs for a set of changed files.
type plan struct {
	// all is true when every harness runs.
	all bool
	// harnesses run with their cross-harness pairs when all is false; none run when it
	// is empty.
	harnesses []string
	reasons   []reason
}

// decide maps each changed file through the rules.
func decide(files, harnesses []string) plan {
	var p plan
	for _, f := range files {
		for _, r := range rules {
			h, ok := r.match(f, harnesses)
			if !ok {
				continue
			}
			p.reasons = append(p.reasons, reason{file: f, runs: r.runs, harness: h, why: strings.ReplaceAll(r.why, "<h>", h)})
			switch r.runs {
			case runNone:
			case runAll:
				p.all = true
			case runHarness:
				if !slices.Contains(p.harnesses, h) {
					p.harnesses = append(p.harnesses, h)
				}
			}
			break
		}
	}
	slices.Sort(p.harnesses)
	if len(harnesses) > 0 && len(p.harnesses) == len(harnesses) {
		p.all = true
	}
	if p.all {
		p.harnesses = nil
	}
	return p
}

// output is the plan for the Makefile: "none", "all", or the harnesses for HARNESS.
func (p plan) output() string {
	switch {
	case p.all:
		return "all"
	case len(p.harnesses) == 0:
		return "none"
	}
	return strings.Join(p.harnesses, ",")
}

// summary says in one line what runs and which changes decided it.
func (p plan) summary() string {
	if len(p.reasons) == 0 {
		return "runs nothing: no file changed"
	}
	if !p.all && len(p.harnesses) == 0 {
		var whys []string
		for _, r := range p.reasons {
			if !slices.Contains(whys, r.why) {
				whys = append(whys, r.why)
			}
		}
		if len(p.reasons) == 1 {
			return fmt.Sprintf("runs nothing: the one changed file is %s", whys[0])
		}
		return fmt.Sprintf("runs nothing: the %d changed files are only %s", len(p.reasons), strings.Join(whys, ", "))
	}
	var because []string
	for _, r := range p.reasons {
		if (p.all && r.runs == runAll) || (!p.all && r.runs == runHarness) {
			because = append(because, r.file)
		}
	}
	if len(because) == 0 {
		// Every harness was picked one by one.
		for _, r := range p.reasons {
			if r.runs == runHarness {
				because = append(because, r.file)
			}
		}
	}
	changed := strings.Join(because, ", ")
	if len(because) > 3 {
		changed = fmt.Sprintf("%s and %d more", strings.Join(because[:3], ", "), len(because)-3)
	}
	if p.all {
		return fmt.Sprintf("runs every harness, because %s changed", changed)
	}
	return fmt.Sprintf("runs %s + cross pairs, because %s changed", strings.Join(p.harnesses, " + "), changed)
}

func main() {
	base := flag.String("base", "origin/main", "the commit or branch to compare against")
	flag.Parse()
	if err := run(*base, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "live-affected:", err)
		os.Exit(1)
	}
}

func run(base string, stdout, stderr io.Writer) error {
	root, err := git(".", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root = strings.TrimSpace(root)
	harnesses, err := knownHarnesses(root)
	if err != nil {
		return err
	}
	files, err := changedFiles(root, base)
	if err != nil {
		return err
	}
	p := decide(files, harnesses)
	for _, r := range p.reasons {
		var what string
		switch r.runs {
		case runNone:
			what = "nothing"
		case runAll:
			what = "every harness"
		case runHarness:
			what = r.harness + " + cross pairs"
		}
		_, _ = fmt.Fprintf(stderr, "  %s: %s (%s)\n", r.file, what, r.why)
	}
	_, _ = fmt.Fprintf(stderr, "live-affected: against %s, %s\n", base, p.summary())
	_, err = fmt.Fprintln(stdout, p.output())
	return err
}

// changedFiles lists the files, relative to the repository root, that differ between
// the merge base of base and HEAD and the working tree (committed or not), and
// untracked files. A renamed file counts at both its old and new path.
func changedFiles(root, base string) ([]string, error) {
	mb, err := git(root, "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("find where this branch left %s: %w (fetch it with git fetch, or name another with BASE=<commit>)", base, err)
	}
	diff, err := git(root, "diff", "--name-only", "--no-renames", strings.TrimSpace(mb))
	if err != nil {
		return nil, err
	}
	untracked, err := git(root, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(diff+"\n"+untracked, "\n") {
		if f != "" && !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files, nil
}

// knownHarnesses lists the harnesses with a profile: the folders in adapters/ that hold
// a profile.yaml.
func knownHarnesses(root string) ([]string, error) {
	profiles, err := filepath.Glob(filepath.Join(root, "adapters", "*", "profile.yaml"))
	if err != nil {
		return nil, err
	}
	if len(profiles) == 0 {
		return nil, errors.New("no harness profiles in adapters/*/profile.yaml")
	}
	var out []string
	for _, p := range profiles {
		out = append(out, filepath.Base(filepath.Dir(p)))
	}
	return out, nil
}

// git runs git in dir and returns what it printed.
func git(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...) //nolint:gosec // fixed git subcommands; base is the person's own choice
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
