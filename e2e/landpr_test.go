//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scripts/land-pr --classify picks the checks from the changed paths: none for design
// records, the code checks for anything else, web-check for the UI, and the live note
// for delivery, setup and upgrades.
func TestLandPRClassify(t *testing.T) {
	t.Parallel()
	code := "make fmt-check lint vet generate-check core-size harness-table-check test e2e"
	for _, tc := range []struct {
		name, paths, want string
	}{
		{"nothing", "", ""},
		{"design records", "design/DECISIONS.md\nengineering/testing.md\n", ""},
		{"docs", "design/VISION.md\ndocs/quickstart.mdx\n", code},
		{"web", "web/app/page.tsx\n", code + " web-check"},
		{"delivery", "server/internal/delivery/daemon.go\n", code + "\nlive-note"},
		{"setup", "server/internal/cli/init.go\n", code + "\nlive-note"},
		{"upgrade", "server/internal/cli/upgrade.go\n", code + "\nlive-note"},
		{"adapter", "adapters/codex/profile.yaml\n", code + "\nlive-note"},
		{"a test of delivery", "server/internal/delivery/daemon_test.go\n", code},
		{"other cli", "server/internal/cli/say.go\n", code},
	} {
		cmd := exec.Command(filepath.Join("..", "scripts", "land-pr"), "--classify")
		cmd.Stdin = strings.NewReader(tc.paths)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", tc.name, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// landPRRepo is a repository with a bare origin, a main checkout on main, and fake gh
// and make commands, for running scripts/land-pr without GitHub.
type landPRRepo struct {
	t              *testing.T
	origin, main   string
	fakes, ghState string
	env            []string
}

// fakeGH answers the gh calls scripts/land-pr makes for PR 7 on branch feature. Its
// first merge fails the way GitHub does when main moved underneath it; the next one
// merges feature into the origin's main.
const fakeGH = `#!/bin/sh
set -eu
case "$1 $2" in
"pr view")
	case "$*" in
	*headRefName*) echo "$(cat "$GH_STATE/state") feature main false https://example.test/pr/7" ;;
	*) cat "$GH_STATE/state" ;;
	esac
	;;
"pr merge")
	echo "$*" >>"$GH_STATE/merges"
	if [ ! -f "$GH_STATE/refused" ]; then
		: >"$GH_STATE/refused"
		echo "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)" >&2
		exit 1
	fi
	tree=$(git -C "$ORIGIN" merge-tree --write-tree main feature)
	commit=$(git -C "$ORIGIN" commit-tree "$tree" -p main -p feature -m "Merge pull request #7")
	git -C "$ORIGIN" update-ref refs/heads/main "$commit"
	echo MERGED >"$GH_STATE/state"
	;;
*) echo "fake gh: unexpected $*" >&2; exit 1 ;;
esac
`

// fakeMake records the targets it was asked to run.
const fakeMake = `#!/bin/sh
echo "$*" >>"$GH_STATE/make"
`

func newLandPRRepo(t *testing.T) *landPRRepo {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &landPRRepo{
		t:       t,
		origin:  filepath.Join(dir, "origin.git"),
		main:    filepath.Join(dir, "aboard"),
		fakes:   filepath.Join(dir, "bin"),
		ghState: filepath.Join(dir, "gh"),
	}
	for _, d := range []string{r.fakes, r.ghState, filepath.Join(dir, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"gh": fakeGH, "make": fakeMake} {
		if err := os.WriteFile(filepath.Join(r.fakes, name), []byte(body), 0o755); err != nil { //nolint:gosec // a fake command the script runs
			t.Fatal(err)
		}
	}
	r.write(filepath.Join(r.ghState, "state"), "OPEN\n")
	r.env = []string{
		"HOME=" + filepath.Join(dir, "home"),
		"PATH=" + r.fakes + string(os.PathListSeparator) + systemPath,
		"TMPDIR=" + dir,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Alex", "GIT_AUTHOR_EMAIL=alex@example.test",
		"GIT_COMMITTER_NAME=Alex", "GIT_COMMITTER_EMAIL=alex@example.test",
		"GH_STATE=" + r.ghState,
		"ORIGIN=" + r.origin,
		"LAND_PR_RETRY_DELAY=0",
	}
	r.git(dir, "init", "-q", "--bare", "-b", "main", r.origin)
	r.git(dir, "clone", "-q", r.origin, r.main)
	r.commit(r.main, "README.md", "Aboard\n", "Start")
	r.git(r.main, "push", "-q", "origin", "main")
	return r
}

func (r *landPRRepo) write(path, content string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test files
		r.t.Fatal(err)
	}
}

func (r *landPRRepo) git(dir string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *landPRRepo) commit(dir, file, content, msg string) {
	r.t.Helper()
	r.write(filepath.Join(dir, file), content)
	r.git(dir, "add", file)
	r.git(dir, "commit", "-q", "-m", msg)
}

// branch pushes a branch feature with one commit writing file, from main, without
// leaving a local branch behind, the way a PR from another machine looks.
func (r *landPRRepo) branch(file, content string) {
	r.t.Helper()
	wt := filepath.Join(filepath.Dir(r.main), "elsewhere")
	r.git(r.main, "worktree", "add", "-q", "-b", "feature", wt, "origin/main")
	r.commit(wt, file, content, "Change "+file)
	r.git(wt, "push", "-q", "origin", "feature")
	r.git(r.main, "worktree", "remove", wt)
	r.git(r.main, "branch", "-q", "-D", "feature")
}

// moveMain adds a commit to the origin's main, as another PR landing would.
func (r *landPRRepo) moveMain(file, content string) {
	r.t.Helper()
	r.commit(r.main, file, content, "Change "+file+" on main")
	r.git(r.main, "push", "-q", "origin", "main")
	r.git(r.main, "reset", "-q", "--hard", "HEAD~1")
}

func (r *landPRRepo) land(args ...string) (string, int) {
	r.t.Helper()
	cmd := exec.Command(filepath.Join(r.repoRoot(), "scripts", "land-pr"), args...)
	cmd.Dir = r.main
	cmd.Env = r.env
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	r.t.Fatalf("scripts/land-pr: %v\n%s", err, out)
	return "", 0
}

func (r *landPRRepo) repoRoot() string {
	root, err := filepath.Abs("..")
	if err != nil {
		r.t.Fatal(err)
	}
	return root
}

func (r *landPRRepo) read(name string) string {
	b, err := os.ReadFile(filepath.Join(r.ghState, name))
	if err != nil && !os.IsNotExist(err) {
		r.t.Fatal(err)
	}
	return string(b)
}

// scripts/land-pr merges main into the PR's branch in a worktree of its own, runs the
// checks the change calls for, merges through a refused first attempt, and then removes
// the worktree, the local branch and the remote branch, leaving the main checkout as
// it was.
func TestLandPRMergesAndCleansUp(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("docs/page.mdx", "A page\n")
	r.moveMain("design/notes.md", "Notes\n")
	before := r.git(r.main, "rev-parse", "HEAD")

	plan, code := r.land("--dry-run", "7")
	if code != 0 {
		t.Fatalf("dry run exited %d:\n%s", code, plan)
	}
	for _, want := range []string{"would create", "merges cleanly", "Checks: make fmt-check", "Dry run: nothing changed."} {
		if !strings.Contains(plan, want) {
			t.Errorf("dry run doesn't say %q:\n%s", want, plan)
		}
	}
	if r.read("merges") != "" || r.read("make") != "" {
		t.Fatalf("dry run ran something: merges %q, make %q", r.read("merges"), r.read("make"))
	}

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if got := strings.TrimSpace(r.read("make")); got != "fmt-check lint vet generate-check core-size harness-table-check test e2e" {
		t.Errorf("make ran %q", got)
	}
	if got := strings.Count(r.read("merges"), "\n"); got != 2 {
		t.Errorf("gh pr merge ran %d times, want 2 (one refused):\n%s", got, out)
	}
	for _, want := range []string{"Merged: PR #7", "Removed the worktree", "Deleted the local branch feature", "Deleted origin/feature"} {
		if !strings.Contains(out, want) {
			t.Errorf("output doesn't say %q:\n%s", want, out)
		}
	}
	files := r.git(r.origin, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "docs/page.mdx") || !strings.Contains(files, "design/notes.md") {
		t.Errorf("origin's main has:\n%s", files)
	}
	if got := r.git(r.origin, "branch", "--list", "feature"); got != "" {
		t.Errorf("origin still has %q", got)
	}
	if got := r.git(r.main, "branch", "--list", "feature"); got != "" {
		t.Errorf("the local branch is still there: %q", got)
	}
	if _, err := os.Stat(filepath.Join(r.main, ".claude", "worktrees", "land-pr-7")); !os.IsNotExist(err) {
		t.Errorf("the temporary worktree is still there: %v", err)
	}
	if got := r.git(r.main, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the main checkout is on %s", got)
	}
	if got := r.git(r.main, "rev-parse", "HEAD"); got != before {
		t.Errorf("the main checkout moved from %s to %s", before, got)
	}
}

// A conflict with main stops before any check or push, names the file, and leaves the
// merge in the worktree to resolve.
func TestLandPRStopsOnConflict(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("README.md", "Aboard, from the branch\n")
	r.moveMain("README.md", "Aboard, from main\n")

	plan, code := r.land("--dry-run", "7")
	if code != 0 || !strings.Contains(plan, "would conflict in:\n  README.md") {
		t.Errorf("dry run exited %d without predicting the conflict:\n%s", code, plan)
	}
	out, code := r.land("7")
	if code != 2 {
		t.Fatalf("land-pr exited %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "conflicts in:\n  README.md") {
		t.Errorf("output doesn't name README.md:\n%s", out)
	}
	if r.read("make") != "" || r.read("merges") != "" {
		t.Errorf("ran checks or a merge after a conflict")
	}
	wt := filepath.Join(r.main, ".claude", "worktrees", "land-pr-7")
	if got := r.git(wt, "diff", "--name-only", "--diff-filter=U"); got != "README.md" {
		t.Errorf("the worktree's unmerged files: %q", got)
	}
	if got, want := r.git(r.origin, "rev-parse", "feature"), r.git(wt, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin's feature moved to %s after a conflict; the branch was at %s", got, want)
	}
}

// A worktree that already has the branch checked out is used, and kept, along with
// its branch; only the remote branch goes once the PR is merged.
func TestLandPRKeepsAnExistingWorktree(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("design/idea.md", "An idea\n")
	wt := filepath.Join(filepath.Dir(r.main), "mine")
	r.git(r.main, "worktree", "add", "-q", "--track", "-b", "feature", wt, "origin/feature")

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Checks: none") || r.read("make") != "" {
		t.Errorf("ran checks for a design-only change:\n%s", out)
	}
	if !strings.Contains(out, "Kept the worktree "+wt) {
		t.Errorf("output doesn't say it kept %s:\n%s", wt, out)
	}
	if got := r.git(wt, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Errorf("the existing worktree is on %s", got)
	}
	if got := r.git(r.origin, "branch", "--list", "feature"); got != "" {
		t.Errorf("origin still has %q", got)
	}
}
