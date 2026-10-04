//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

// launcherExample is the example launcher docs/extending.mdx shows, relative to /e2e.
const launcherExample = "../examples/launcher-bg/aboard-launcher-bg"

// TestExtendingPageShowsTheExampleLauncher checks that the launcher docs/extending.mdx
// shows is examples/launcher-bg/aboard-launcher-bg byte for byte, so the code a reader
// copies is the code TestExampleLauncherPassesTheKit checks.
func TestExtendingPageShowsTheExampleLauncher(t *testing.T) {
	t.Parallel()
	file, err := os.ReadFile(launcherExample)
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile("../docs/extending.mdx")
	if err != nil {
		t.Fatal(err)
	}
	_, after, ok := strings.Cut(string(page), "```python\n")
	if !ok {
		t.Fatal("docs/extending.mdx has no python code block")
	}
	block, _, ok := strings.Cut(after, "```\n")
	if !ok {
		t.Fatal("docs/extending.mdx: the python code block doesn't end")
	}
	if block != string(file) {
		t.Fatalf("docs/extending.mdx's launcher differs from examples/launcher-bg/aboard-launcher-bg; copy the file into the page\npage:\n%s\nfile:\n%s", block, file)
	}
}

// TestExampleLauncherPassesTheKit runs the launcher kit against
// examples/launcher-bg/aboard-launcher-bg, as make launcher-kit LAUNCHER=bg does with
// the script saved on the PATH, so a change to the launcher protocol that breaks the
// example fails here. It runs with a home, Aboard home and temporary folder of its own.
func TestExampleLauncherPassesTheKit(t *testing.T) {
	t.Parallel()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("The example launcher needs Python 3, and python3 isn't on the PATH. Install it " +
			"(macOS: brew install python, or xcode-select --install; Debian or Ubuntu: apt install python3) and run the tests again.")
	}
	// The interpreter itself, not a version manager's shim (pyenv, asdf), which looks
	// for its settings in the home this test replaces.
	out, err := exec.Command(python, "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Fatalf("python3 -c 'import sys; print(sys.executable)': %v", err)
	}
	interpreter := strings.TrimSpace(string(out))

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	example, err := filepath.Abs(launcherExample)
	if err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"aboard-launcher-bg": example, "python3": interpreter} {
		if err := os.Symlink(target, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}

	// go test keeps this machine's Go settings and caches, found before HOME changes,
	// so the kit doesn't build everything again in the test's home.
	goVars := []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}
	goEnv, err := exec.Command("go", append([]string{"env"}, goVars...)...).Output()
	if err != nil {
		t.Fatalf("go env: %v", err)
	}
	goValues := strings.Split(strings.TrimSuffix(string(goEnv), "\n"), "\n")
	if len(goValues) != len(goVars) {
		t.Fatalf("go env printed %q, want %d lines", goEnv, len(goVars))
	}
	vars := []string{
		"LAUNCHER=bg",
		"PATH=" + bin + string(os.PathListSeparator) + systemPath,
		"HOME=" + filepath.Join(root, "home"),
		"ABOARD_HOME=" + filepath.Join(root, "aboard"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
	}
	for i, name := range goVars {
		vars = append(vars, name+"="+goValues[i])
	}
	for _, dir := range []string{"home", "aboard", "tmp"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "PATH", name == "HOME", name == "TMPDIR", name == "LAUNCHER",
			strings.HasPrefix(name, "ABOARD_"), slices.Contains(goVars, name):
		default:
			vars = append(vars, kv)
		}
	}

	cmd := exec.Command("go", "test", "-tags", "launcherkit", "-count=1", "-v",
		"-run", "^TestLauncherKit$", "./server/internal/launcher/launchertest/")
	cmd.Dir = ".."
	cmd.Env = vars
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("the launcher kit failed against examples/launcher-bg/aboard-launcher-bg: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "--- PASS: TestLauncherKit") {
		t.Fatalf("the launcher kit didn't run:\n%s", output.String())
	}
}
