//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// hashRe matches the shortened head hash `aboard audit verify` prints, which differs on
// every run.
var hashRe = regexp.MustCompile(`sha256:[0-9a-f]{8}…`)

// TestExampleHelloPair runs examples/hello-pair twice on one machine and checks the
// first run prints exactly what the example's README shows.
func TestExampleHelloPair(t *testing.T) {
	t.Parallel()
	e := newEnv(t)

	first := e.runExample("hello-pair/hello-pair.sh")
	want := readmeOutput(t, "hello-pair/README.md", "$ ./hello-pair.sh")
	if got := hashRe.ReplaceAllString(first.stdout, "sha256:…"); got != hashRe.ReplaceAllString(want, "sha256:…") {
		t.Fatalf("output differs from the README\nwant:\n%s\ngot:\n%s", want, first)
	}

	// A second run on the same machine works and makes a new board, even though the
	// machine now has agents called member and member-2 on another board.
	second := e.runExample("hello-pair/hello-pair.sh")
	if !strings.Contains(second.stdout, "Joined board general-2 as member-2") ||
		!strings.Contains(second.stdout, "OK: 7 events on general-2 verified") {
		t.Fatalf("second run didn't pair on a new board\n%s", second)
	}

	// The directory the example ran from isn't linked to a board.
	if _, err := os.Stat(filepath.Join(e.dir, ".aboard")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the example linked its working directory to a board (stat: %v)", err)
	}
}

// runExample runs a script from /examples in the env's project directory, with the
// aboard binary under test first on the PATH, and fails the test unless it exits 0.
func (e *env) runExample(script string) result {
	e.t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "examples", script))
	if err != nil {
		e.t.Fatal(err)
	}
	cmd := exec.Command("sh", path)
	cmd.Dir = e.dir
	cmd.Env = append(append([]string{}, e.vars...),
		"PATH="+filepath.Dir(binary)+string(os.PathListSeparator)+fakeBin+string(os.PathListSeparator)+systemPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	r := result{args: []string{"(example)", script}}
	err = cmd.Run()
	r.stdout, r.stderr = stdout.String(), stderr.String()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		r.code = exit.ExitCode()
	default:
		e.t.Fatalf("run example %s: %v", script, err)
	}
	if r.code != 0 {
		e.t.Fatalf("example failed:\n%s\nserver log:\n%s", r, e.serverLog())
	}
	return r
}

// readmeOutput returns the output an example's README shows under the console line
// prompt, up to the end of that code block.
func readmeOutput(t *testing.T, readme, prompt string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "examples", readme))
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(raw), prompt+"\n")
	if !ok {
		t.Fatalf("%s has no line %q", readme, prompt)
	}
	out, _, ok := strings.Cut(after, "```")
	if !ok {
		t.Fatalf("%s: the code block after %q doesn't end", readme, prompt)
	}
	return out
}
