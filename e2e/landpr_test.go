//go:build e2e

package e2e

import (
	"errors"
	"fmt"
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
		{"self-upgrade", "server/internal/cli/selfupgrade.go\n", code + "\nlive-note"},
		{"release", "server/internal/cli/release.go\n", code + "\nlive-note"},
		{"update notice", "server/internal/cli/updatenotice.go\n", code + "\nlive-note"},
		{"install script", "scripts/install.sh\n", code + "\nlive-note"},
		{"doctor", "server/internal/cli/doctor.go\n", code + "\nlive-note"},
		{"init setup", "server/internal/cli/initsetup.go\n", code + "\nlive-note"},
		{"launcher", "server/internal/launcher/tmux/tmux.go\n", code + "\nlive-note"},
		{"external launcher", "launchers/herdr/main.go\n", code + "\nlive-note"},
		{"a test of upgrades", "server/internal/cli/selfupgrade_test.go\n", code},
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
// merges feature into the origin's main; with $GH_STATE/move-main, the first merge
// moves main before refusing, as another PR landing at that moment would.
//
// gh api for the check workflow's runs answers with the blocks of $GH_STATE/runs in
// turn, blocks separated by a "--" line and the last repeated, each line a run as
// scripts/ci-status's --jq prints it, with SHA standing for the commit asked about. An
// empty block is no runs; with no such file, the pull request's run passed.
//
// $GH_STATE/move-main-after-ci moves main once, right after the first answer about
// CI; $GH_STATE/move-main-after-every-ci after every one. $GH_STATE/accept makes every
// merge succeed, as GitHub does while main doesn't require up-to-date branches. A merge
// of a feature that doesn't contain main is recorded in $GH_STATE/stale-merges.
const fakeGH = `#!/bin/sh
set -eu
move_main() {
	n=$(git -C "$ORIGIN" rev-list --count main)
	blob=$(echo "Landed meanwhile" | git -C "$ORIGIN" hash-object -w --stdin)
	export GIT_INDEX_FILE="$GH_STATE/index"
	git -C "$ORIGIN" read-tree main
	git -C "$ORIGIN" update-index --add --cacheinfo "100644,$blob,design/meanwhile-$n.md"
	tree=$(git -C "$ORIGIN" write-tree)
	unset GIT_INDEX_FILE
	commit=$(git -C "$ORIGIN" commit-tree "$tree" -p main -m "Land another PR")
	git -C "$ORIGIN" update-ref refs/heads/main "$commit"
}
case "$1 $2" in
"api repos/{owner}/{repo}/actions/workflows/check.yml/runs?"*)
	echo "$*" >>"$GH_STATE/run-lists"
	sha=$(printf '%s\n' "$2" | sed -n 's/.*head_sha=\([0-9a-f]*\).*/\1/p')
	if [ ! -f "$GH_STATE/runs" ]; then
		echo "pull_request feature $sha o/aboard o/aboard 1 completed success https://example.test/runs/1"
	else
		awk '$0 == "--" { exit } { print }' "$GH_STATE/runs" | sed "s/SHA/$sha/g"
		if grep -qx -- -- "$GH_STATE/runs"; then
			awk 'found { print } $0 == "--" && !found { found = 1 }' "$GH_STATE/runs" >"$GH_STATE/runs.next"
			mv "$GH_STATE/runs.next" "$GH_STATE/runs"
		fi
	fi
	if [ -f "$GH_STATE/move-main-after-ci" ]; then
		rm "$GH_STATE/move-main-after-ci"
		move_main
	fi
	[ ! -f "$GH_STATE/move-main-after-every-ci" ] || move_main
	;;
"pr view")
	case "$*" in
	*headRefName*) echo "$(cat "$GH_STATE/state") feature main false https://example.test/pr/7" ;;
	*) cat "$GH_STATE/state" ;;
	esac
	;;
"pr merge")
	echo "$*" >>"$GH_STATE/merges"
	if [ ! -f "$GH_STATE/refused" ] && [ ! -f "$GH_STATE/accept" ]; then
		: >"$GH_STATE/refused"
		[ ! -f "$GH_STATE/move-main" ] || move_main
		echo "GraphQL: Base branch was modified. Review and try the merge again. (mergePullRequest)" >&2
		exit 1
	fi
	git -C "$ORIGIN" merge-base --is-ancestor main feature || git -C "$ORIGIN" rev-parse feature >>"$GH_STATE/stale-merges"
	tree=$(git -C "$ORIGIN" merge-tree --write-tree main feature)
	commit=$(git -C "$ORIGIN" commit-tree "$tree" -p main -p feature -m "Merge pull request #7")
	git -C "$ORIGIN" update-ref refs/heads/main "$commit"
	echo MERGED >"$GH_STATE/state"
	;;
*) echo "fake gh: unexpected $*" >&2; exit 1 ;;
esac
`

// fakeMake records the targets it was asked to run, and fails make live-affected while
// $GH_STATE/live-fails exists.
const fakeMake = `#!/bin/sh
echo "$*" >>"$GH_STATE/make"
if [ "$*" = live-affected ] && [ -f "$GH_STATE/live-fails" ]; then
	echo "--- FAIL: TestPingPong/codex"
	exit 2
fi
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
		writeProgram(t, filepath.Join(r.fakes, name), []byte(body))
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
		"CI_STATUS_INTERVAL=0",
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

// With --local, scripts/land-pr merges main into the PR's branch in a worktree of its
// own, runs the checks the change calls for here, merges through a refused first
// attempt, and then removes the worktree, the local branch and the remote branch,
// leaving the main checkout as it was.
func TestLandPRMergesAndCleansUp(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("docs/page.mdx", "A page\n")
	r.moveMain("design/notes.md", "Notes\n")
	before := r.git(r.main, "rev-parse", "HEAD")

	plan, code := r.land("--dry-run", "--local", "7")
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

	out, code := r.land("--local", "7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if r.read("run-lists") != "" {
		t.Errorf("--local asked GitHub for CI runs: %q", r.read("run-lists"))
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
	if r.read("make") != "" {
		t.Errorf("ran checks here when CI had passed:\n%s", out)
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

// With --live, make live-affected runs after the checks, and a failure stops before the
// push and the merge; once it passes, the PR merges.
func TestLandPRRunsTheLiveTestsWithLive(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("server/internal/delivery/daemon.go", "package delivery\n")
	r.write(filepath.Join(r.ghState, "live-fails"), "")

	out, code := r.land("--live", "7")
	if code != 3 {
		t.Fatalf("land-pr exited %d, want 3:\n%s", code, out)
	}
	if !strings.Contains(out, "--- FAIL: TestPingPong/codex") {
		t.Errorf("output doesn't show the live failure:\n%s", out)
	}
	if r.read("merges") != "" {
		t.Errorf("merged after the live tests failed")
	}
	if got := r.git(r.origin, "rev-parse", "main"); got != r.git(r.main, "rev-parse", "HEAD") {
		t.Errorf("origin's main moved to %s", got)
	}

	if err := os.Remove(filepath.Join(r.ghState, "live-fails")); err != nil {
		t.Fatal(err)
	}
	out, code = r.land("--live", "7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Live tests passed.") || !strings.Contains(out, "Merged: PR #7") {
		t.Errorf("output doesn't say the live tests passed and the PR merged:\n%s", out)
	}
	if got := strings.Count(r.read("make"), "live-affected"); got != 2 {
		t.Errorf("make live-affected ran %d times, want 2:\n%s", got, r.read("make"))
	}
}

// By default CI is the gate: when the check workflow already passed on the branch's
// head, nothing runs here and the PR merges at that exact commit.
func TestLandPRMergesWhenCIPassedOnTheHead(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("server/internal/cli/say.go", "package cli\n")
	head := r.git(r.origin, "rev-parse", "feature")

	plan, code := r.land("--dry-run", "7")
	if code != 0 || !strings.Contains(plan, "Checks: the check workflow on GitHub") ||
		!strings.Contains(plan, "CI: passed on the branch's head") {
		t.Errorf("dry run exited %d without the CI plan:\n%s", code, plan)
	}

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if r.read("make") != "" {
		t.Errorf("ran make when CI had passed: %q", r.read("make"))
	}
	if !strings.Contains(r.read("run-lists"), "actions/workflows/check.yml/runs?head_sha="+head+"&event=pull_request") {
		t.Errorf("didn't ask for the check workflow on %s: %q", head, r.read("run-lists"))
	}
	if !strings.Contains(r.read("merges"), "--match-head-commit "+head) {
		t.Errorf("didn't merge at %s: %q", head, r.read("merges"))
	}
	if !strings.Contains(out, "CI: passed on") || !strings.Contains(out, "Merged: PR #7") {
		t.Errorf("output doesn't say CI passed and the PR merged:\n%s", out)
	}
}

// When main moved, the merge with main is a commit CI has never seen: land-pr pushes it,
// waits while the run is missing and then running, and merges once it passes, at that
// commit.
func TestLandPRWaitsForCIOnTheMergedHead(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("docs/page.mdx", "A page\n")
	r.moveMain("design/notes.md", "Notes\n")
	r.write(filepath.Join(r.ghState, "runs"),
		"\n--\n\n--\n"+prRun("in_progress", "-", 2)+"--\n"+prRun("completed", "success", 2))

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if r.read("make") != "" {
		t.Errorf("ran make in the CI path: %q", r.read("make"))
	}
	merged := r.git(r.origin, "rev-parse", "main^2")
	if parents := strings.Fields(r.git(r.origin, "rev-list", "--parents", "-n", "1", merged)); len(parents) != 3 {
		t.Errorf("the merged head %s isn't the merge with main: %v", merged, parents)
	}
	if got := strings.Count(r.read("run-lists"), "head_sha="+merged); got != 4 {
		t.Errorf("asked about %s %d times, want 4:\n%s", merged, got, r.read("run-lists"))
	}
	if !strings.Contains(r.read("merges"), "--match-head-commit "+merged) {
		t.Errorf("didn't merge at %s: %q", merged, r.read("merges"))
	}
	for _, want := range []string{"for CI to run on", "CI: waiting up to 30 min", "CI: passed (https://example.test/runs/2)", "Merged: PR #7"} {
		if !strings.Contains(out, want) {
			t.Errorf("output doesn't say %q:\n%s", want, out)
		}
	}
}

// A check workflow run that failed on the head stops the landing before the merge,
// names the run, and says what to do about a known flake.
func TestLandPRStopsWhenCIFailed(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("server/internal/cli/say.go", "package cli\n")
	r.write(filepath.Join(r.ghState, "runs"), prRun("completed", "failure", 3))

	out, code := r.land("7")
	if code != 3 {
		t.Fatalf("land-pr exited %d, want 3:\n%s", code, out)
	}
	for _, want := range []string{"ended with failure", "https://example.test/runs/3", "known flake", "gh run rerun --failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output doesn't say %q:\n%s", want, out)
		}
	}
	if r.read("merges") != "" || r.read("make") != "" {
		t.Errorf("merged or ran make after CI failed")
	}
}

// CI that hasn't finished within LAND_PR_CI_WAIT stops with exit 6, the branch pushed
// so a later run finds the result.
func TestLandPRStopsWhenCIIsStillRunning(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("docs/page.mdx", "A page\n")
	r.moveMain("design/notes.md", "Notes\n")
	r.write(filepath.Join(r.ghState, "runs"), prRun("queued", "-", 4))
	r.env = append(r.env, "LAND_PR_CI_WAIT=0")

	out, code := r.land("7")
	if code != 6 {
		t.Fatalf("land-pr exited %d, want 6:\n%s", code, out)
	}
	if !strings.Contains(out, "CI hasn't finished") || r.read("merges") != "" {
		t.Errorf("didn't stop for unfinished CI:\n%s", out)
	}
	wt := filepath.Join(r.main, ".claude", "worktrees", "land-pr-7")
	if got, want := r.git(r.origin, "rev-parse", "feature"), r.git(wt, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin's feature is %s, want the merged head %s", got, want)
	}
}

// prRun is a pull request's check workflow run on the commit asked about, as the fake
// gh prints it, ending the block of runs.
func prRun(status, conclusion string, n int) string {
	return fmt.Sprintf("pull_request feature SHA o/aboard o/aboard 1 %s %s https://example.test/runs/%d\n", status, conclusion, n)
}

// When main moves while the PR is merging, land-pr doesn't merge the commit CI checked
// without the new main: it starts again, merges the new main, checks the new head, and
// merges that.
func TestLandPRStartsAgainWhenMainMovesWhileMerging(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("docs/page.mdx", "A page\n")
	first := r.git(r.origin, "rev-parse", "feature")
	r.write(filepath.Join(r.ghState, "move-main"), "")

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "Main moved to") || !strings.Contains(out, "Merged: PR #7") {
		t.Errorf("output doesn't say it started again and merged:\n%s", out)
	}
	merged := r.git(r.origin, "rev-parse", "main^2")
	if merged == first {
		t.Fatalf("merged %s, the head checked before main moved", first)
	}
	if r.read("stale-merges") != "" {
		t.Errorf("merged a head without the current main: %s", r.read("stale-merges"))
	}
	merges := r.read("merges")
	if !strings.Contains(merges, "--match-head-commit "+first) || !strings.Contains(merges, "--match-head-commit "+merged) {
		t.Errorf("merges weren't at the first head and then the new one:\n%s", merges)
	}
	if !strings.Contains(r.read("run-lists"), "head_sha="+merged) {
		t.Errorf("didn't check CI on the new head %s:\n%s", merged, r.read("run-lists"))
	}
}

// When main moves after CI passed but before the merge, and GitHub would accept the
// merge anyway (main not requiring up-to-date branches), land-pr doesn't try it: it
// starts again, merges the new main, checks the new head, and merges only that.
func TestLandPRNeverMergesAHeadWithoutTheCurrentMain(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("server/internal/cli/say.go", "package cli\n")
	first := r.git(r.origin, "rev-parse", "feature")
	r.write(filepath.Join(r.ghState, "accept"), "")
	r.write(filepath.Join(r.ghState, "move-main-after-ci"), "")

	out, code := r.land("7")
	if code != 0 {
		t.Fatalf("land-pr exited %d:\n%s", code, out)
	}
	if r.read("stale-merges") != "" {
		t.Fatalf("merged a head without the current main: %s\n%s", r.read("stale-merges"), out)
	}
	merges := r.read("merges")
	if strings.Contains(merges, first) || strings.Count(merges, "\n") != 1 {
		t.Errorf("tried to merge the head CI checked before main moved, %s:\n%s", first, merges)
	}
	merged := r.git(r.origin, "rev-parse", "main^2")
	if !strings.Contains(merges, "--match-head-commit "+merged) || !strings.Contains(r.read("run-lists"), "head_sha="+merged) {
		t.Errorf("didn't check CI on and merge the new head %s:\nmerges %s\nruns %s", merged, merges, r.read("run-lists"))
	}
	if !strings.Contains(out, "Main moved to") {
		t.Errorf("output doesn't say main moved:\n%s", out)
	}
}

// When main moves after every check, land-pr gives up after three rounds with exit 4,
// having merged nothing.
func TestLandPRFailsClosedWhenMainKeepsMoving(t *testing.T) {
	t.Parallel()
	r := newLandPRRepo(t)
	r.branch("server/internal/cli/say.go", "package cli\n")
	r.write(filepath.Join(r.ghState, "accept"), "")
	r.write(filepath.Join(r.ghState, "move-main-after-every-ci"), "")

	out, code := r.land("7")
	if code != 4 {
		t.Fatalf("land-pr exited %d, want 4:\n%s", code, out)
	}
	if r.read("merges") != "" || !strings.Contains(out, "main moved during each of 3 tries") {
		t.Errorf("merged, or didn't say main kept moving:\nmerges %s\n%s", r.read("merges"), out)
	}
}

// scripts/ci-status reads the check workflow's newest qualifying run for a commit: one
// for the event (and branch) asked for, on exactly that commit, from this repository.
// Its exit code says passed, failed, missing or still running, and with --wait it waits
// for a run that is still going.
func TestCIStatus(t *testing.T) {
	t.Parallel()
	sha := strings.Repeat("ab", 20)
	other := strings.Repeat("cd", 20)
	run := func(event, branch, commit, headRepo, status, conclusion string) string {
		return fmt.Sprintf("%s %s %s %s o/aboard 2 %s %s https://example.test/runs/1\n", event, branch, commit, headRepo, status, conclusion)
	}
	release := []string{"--event", "push", "--branch", "main"}
	land := []string{"--event", "pull_request"}
	for _, tc := range []struct {
		name, runs string
		args       []string
		code       int
		want       string
	}{
		{"passed", run("push", "main", "SHA", "o/aboard", "completed", "success"), release, 0, "success https://example.test/runs/1"},
		{"failed", run("push", "main", "SHA", "o/aboard", "completed", "failure"), release, 2, "failure https://example.test/runs/1"},
		{"cancelled", run("pull_request", "feature", "SHA", "o/aboard", "completed", "cancelled"), land, 2, "cancelled https://example.test/runs/1"},
		{"no run", "", append([]string{"--wait", "60"}, release...), 3, "missing -"},
		{"still running", run("push", "main", "SHA", "o/aboard", "in_progress", "-"), release, 4, "pending https://example.test/runs/1"},
		{"a pull request's run doesn't count for a release", run("pull_request", "main", "SHA", "o/aboard", "completed", "success"), release, 3, "missing -"},
		{"a push to another branch doesn't count for a release", run("push", "feature", "SHA", "o/aboard", "completed", "success"), release, 3, "missing -"},
		{"a run on another commit doesn't count", run("push", "main", other, "o/aboard", "completed", "success"), release, 3, "missing -"},
		{"a fork's run doesn't count", run("pull_request", "feature", "SHA", "fork/aboard", "completed", "success"), land, 3, "missing -"},
		{
			"the newest qualifying run decides",
			run("pull_request", "feature", "SHA", "fork/aboard", "completed", "success") +
				run("pull_request", "feature", "SHA", "o/aboard", "completed", "failure") +
				run("pull_request", "feature", "SHA", "o/aboard", "completed", "success"),
			land, 2, "failure https://example.test/runs/1",
		},
		{
			"waits for the run",
			run("push", "main", "SHA", "o/aboard", "queued", "-") + "--\n" +
				run("push", "main", "SHA", "o/aboard", "in_progress", "-") + "--\n" +
				run("push", "main", "SHA", "o/aboard", "completed", "success"),
			append([]string{"--wait", "60"}, release...),
			0, "success https://example.test/runs/1",
		},
		{
			"waits for the run to start",
			"\n--\n" + run("pull_request", "feature", "SHA", "o/aboard", "completed", "failure"),
			append([]string{"--wait", "60", "--expect"}, land...),
			2, "failure https://example.test/runs/1",
		},
	} {
		r := newLandPRRepo(t)
		r.write(filepath.Join(r.ghState, "runs"), tc.runs)
		cmd := exec.Command(filepath.Join(r.repoRoot(), "scripts", "ci-status"), append(tc.args, sha)...) //nolint:gosec // the script under test
		cmd.Env = r.env
		var stdout strings.Builder
		cmd.Stdout = &stdout
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if code != tc.code || strings.TrimSpace(stdout.String()) != tc.want {
			t.Errorf("%s: exit %d and %q, want %d and %q", tc.name, code, stdout.String(), tc.code, tc.want)
		}
	}
	for _, args := range [][]string{{"--event", "push", "abc123"}, {strings.Repeat("ab", 20)}, {"--event", "schedule", strings.Repeat("ab", 20)}} {
		if err := exec.Command(filepath.Join("..", "scripts", "ci-status"), args...).Run(); err == nil { //nolint:gosec // the script under test
			t.Errorf("ci-status accepted %q", args)
		}
	}
}
