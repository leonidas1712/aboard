//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// aboard upgrade and the update notice, against a fake release server (installRelease)
// with an isolated HOME. The binary in the release is this source's aboard, and the one
// it upgrades is the older build, installed where the install script puts it.

// upgradeEnv is an env whose aboard is the older build in ~/.local/bin, with releases
// served by r.
type upgradeEnv struct {
	*env
	r   *installRelease
	exe string // the installed aboard
}

func newUpgradeEnv(t *testing.T) *upgradeEnv {
	t.Helper()
	e := newEnv(t)
	e.harnessHome()
	r := newInstallRelease(t)
	u := &upgradeEnv{env: e, r: r, exe: filepath.Join(e.home, ".local", "bin", "aboard")}
	u.install(oldBinary, u.exe)
	e.bin = u.exe
	// The machine's own cosign stays out: the tests that check signatures bring a fake.
	e.vars = append(e.vars,
		"ABOARD_DOWNLOAD_URL="+r.url,
		"PATH="+fakeBin+string(os.PathListSeparator)+withoutCommand(systemPath, "cosign"),
	)
	r.version = "0.1.0"
	r.publish(u.releaseEntries(readFile(t, binary)))
	return u
}

// releaseEntries is a release archive holding aboard and the herdr launcher.
func (u *upgradeEnv) releaseEntries(aboard string) []archiveEntry {
	return []archiveEntry{
		{name: "aboard", body: aboard},
		{name: "aboard-launcher-herdr", body: "#!/bin/sh\necho herdr launcher\n"},
		{name: "LICENSE", body: "license\n"},
	}
}

// cosign puts the fake cosign first on the PATH, its bundle signed by signer, and
// returns the environment that runs it.
func (u *upgradeEnv) cosign(signer string) []string {
	u.r.withCosign(signer)
	sep := string(os.PathListSeparator)
	return []string{
		"PATH=" + u.r.bin + sep + fakeBin + sep + withoutCommand(systemPath, "cosign"),
		"FAKE_COSIGN_LOG=" + u.r.cosigned, "FAKE_COSIGN_ISSUER=" + githubIssuer, "FAKE_COSIGN_SIGNER=" + signer,
	}
}

// install copies the program at from to path.
func (u *upgradeEnv) install(from, path string) {
	u.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		u.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(readFile(u.t, from)), 0o755); err != nil { //nolint:gosec // an installed program
		u.t.Fatal(err)
	}
}

// fileSum returns the SHA-256 of the file at path.
func fileSum(t *testing.T, path string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(readFile(t, path)))
	return hex.EncodeToString(sum[:])
}

// untouched checks that the folder holding exe has only exe in it, unchanged from sum.
func untouched(t *testing.T, exe, sum string) {
	t.Helper()
	if got := fileSum(t, exe); got != sum {
		t.Fatal("the installed aboard changed")
	}
	ents, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		t.Fatalf("the install folder holds %v, want only aboard", ents)
	}
}

// errorOf returns the error object of a --json failure.
func errorOf(t *testing.T, r result) map[string]any {
	t.Helper()
	var out struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil || out.Error == nil {
		t.Fatalf("not an error:\n%s", r)
	}
	return out.Error
}

func TestUpgradeInstallsTheReleaseAndRefreshesTheSetup(t *testing.T) {
	t.Parallel()
	u := newUpgradeEnv(t)
	u.run("init", "--yes", "--harness", "claude-code")

	r := u.run("upgrade", "--json")
	out := r.json(t)
	matchesCLISpec(t, "UpgradeOutput", out)
	launcher := filepath.Join(filepath.Dir(u.exe), "aboard-launcher-herdr")
	for path, want := range map[string]any{
		"upgraded": true, "from": oldVersion, "to": "0.1.0", "path": u.exe,
		"signature_checked": false, "setup.refreshed": true,
	} {
		if got := field(t, out, path); got != want {
			t.Fatalf("%s: got %v, want %v\n%s", path, got, want, r)
		}
	}
	if got := field(t, out, "launchers"); !slices.Equal(anyStrings(got), []string{launcher}) {
		t.Fatalf("launchers: %v", got)
	}
	if got := field(t, out, "setup.harnesses"); !slices.Equal(anyStrings(got), []string{"claude-code"}) {
		t.Fatalf("setup.harnesses: %v", got)
	}

	// The new binary is in place, and doctor finds the setup current for it.
	if v := u.run("version", "--json").json(t); field(t, v, "version") != "0.1.0" {
		t.Fatalf("installed version: %v", v)
	}
	if got := readFile(t, launcher); got != "#!/bin/sh\necho herdr launcher\n" {
		t.Fatalf("launcher: %q", got)
	}
	checks := u.doctorChecks()
	for _, name := range []string{"claude_hooks", "claude_skill"} {
		if checks[name]["level"] != "ok" {
			t.Fatalf("doctor's %s check after the upgrade: %v", name, checks[name])
		}
	}
	if ents, _ := os.ReadDir(filepath.Dir(u.exe)); len(ents) != 2 {
		t.Fatalf("install folder: %v", ents)
	}

	// Again: nothing to upgrade, and nothing downloaded but the latest checksums.
	before := len(u.r.requests())
	again := u.run("upgrade")
	if again.stdout != "aboard 0.1.0 is already installed; nothing to upgrade.\n" {
		t.Fatalf("upgrade again:\n%s", again)
	}
	if asked := u.r.requests()[before:]; !slices.Equal(asked, []string{"/releases/latest/download/checksums.txt"}) {
		t.Fatalf("upgrade again downloaded %v", asked)
	}
}

func TestUpgradeChecksTheSignatureWithCosign(t *testing.T) {
	t.Parallel()
	u := newUpgradeEnv(t)
	r := u.exec(u.cosign(strings.Replace(releaseIdentity, "v"+installVersion, "v0.1.0", 1)), "", "upgrade")
	if r.code != 0 {
		t.Fatalf("upgrade:\n%s", r)
	}
	expectLines(t, r,
		"Checked the signature: signed by aboard's release workflow for v0.1.0.",
		"Upgraded aboard "+oldVersion+" to 0.1.0 at ~/.local/bin/aboard.",
		"Installed ~/.local/bin/aboard-launcher-herdr.",
		"Nothing to refresh: aboard init hasn't set up any harness for every project. Run aboard init to set one up.",
		"Restart open sessions so they run the new aboard's skill.",
	)
}

func TestUpgradeRefusesBeforeChangingAnything(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// setup prepares the release and returns extra environment for the command.
		setup     func(u *upgradeEnv) []string
		args      []string // after upgrade --json
		code      string
		hint      string
		downloads bool // whether the refusal comes after downloading
	}{
		{
			name: "inside a harness session", code: "human_command_in_session", hint: "aboard upgrade",
			setup: func(*upgradeEnv) []string { return []string{"CLAUDECODE=1"} },
		},
		{
			name: "as an agent", code: "human_command_in_session", hint: "aboard upgrade",
			setup: func(*upgradeEnv) []string { return []string{"ABOARD_AGENT=claude"} },
		},
		{
			name: "a binary from Homebrew", code: "not_installed_by_script", hint: "brew upgrade aboard",
			setup: func(u *upgradeEnv) []string {
				u.moveTo(filepath.Join(u.home, "homebrew", "Cellar", "aboard", oldVersion, "bin", "aboard"))
				return nil
			},
		},
		{
			name: "a binary built from source", code: "not_installed_by_script", hint: "make install",
			setup: func(u *upgradeEnv) []string {
				u.moveTo(filepath.Join(u.home, "go", "bin", "aboard"))
				return []string{"GOPATH=" + filepath.Join(u.home, "go")}
			},
		},
		{
			name: "a plain-HTTP download address", code: "invalid_request", hint: "https://",
			setup: func(*upgradeEnv) []string {
				return []string{"ABOARD_DOWNLOAD_URL=http://releases.example.test/releases"}
			},
		},
		{
			name: "a version with no release", code: "release_not_found", args: []string{"--version", "7.7.7"},
			setup: func(*upgradeEnv) []string { return nil },
		},
		{
			name: "a wrong checksum", code: "checksum_mismatch", downloads: true,
			setup: func(u *upgradeEnv) []string {
				u.r.setFile(u.r.archivePath(), tarGz(u.t, append(u.releaseEntries("x"), archiveEntry{name: "extra", body: "x"})))
				return nil
			},
		},
		{
			name: "an interrupted download", code: "download_failed", downloads: true,
			setup: func(u *upgradeEnv) []string { u.r.cutFile(u.r.archivePath()); return nil },
		},
		{
			name: "a signature from another workflow", code: "signature_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				return u.cosign("https://github.com/someone/aboard/.github/workflows/release.yml@refs/tags/v0.1.0")
			},
		},
		{
			name: "a signature from a branch", code: "signature_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				return u.cosign("https://github.com/leonidas1712/aboard/.github/workflows/release.yml@refs/heads/main")
			},
		},
		{
			name: "a path out of the folder", code: "archive_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				u.r.publish(append(u.releaseEntries(fakeAboard), archiveEntry{name: "../escaped", body: "x"}))
				return nil
			},
		},
		{
			name: "a symbolic link", code: "archive_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				entries := u.releaseEntries(fakeAboard)
				entries[1] = archiveEntry{name: "aboard-launcher-herdr", typ: '2', link: "/bin/sh"}
				u.r.publish(entries)
				return nil
			},
		},
		{
			name: "an aboard that doesn't run", code: "archive_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				u.r.publish(u.releaseEntries("#!/bin/sh\nexit 1\n"))
				return nil
			},
		},
		{
			// One byte over the 64 MiB a file in a release archive may have.
			name: "a file too large", code: "archive_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				big := archiveEntry{name: "padding", body: string(make([]byte, 64<<20+1))}
				u.r.publish(append(u.releaseEntries(fakeAboard), big))
				return nil
			},
		},
		{
			name: "too many files", code: "archive_invalid", downloads: true,
			setup: func(u *upgradeEnv) []string {
				entries := u.releaseEntries(fakeAboard)
				for i := range 40 {
					entries = append(entries, archiveEntry{name: fmt.Sprintf("extra-%02d", i), body: "x"})
				}
				u.r.publish(entries)
				return nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := newUpgradeEnv(t)
			u.t = t
			extra := tc.setup(u)
			sum := fileSum(t, u.exe)
			r := u.exec(extra, "", append([]string{"upgrade", "--json"}, tc.args...)...)
			e := errorOf(t, r)
			if r.code != 1 || e["code"] != tc.code || !strings.Contains(e["hint"].(string), tc.hint) {
				t.Fatalf("want %s with a hint naming %q:\n%s", tc.code, tc.hint, r)
			}
			untouched(t, u.exe, sum)
			downloaded := slices.ContainsFunc(u.r.requests(), func(p string) bool { return strings.HasSuffix(p, ".tar.gz") })
			if downloaded != tc.downloads {
				t.Fatalf("downloaded the archive: %v, want %v (%v)", downloaded, tc.downloads, u.r.requests())
			}
		})
	}
}

// moveTo moves the installed aboard to path, which becomes the one the env runs.
func (u *upgradeEnv) moveTo(path string) {
	u.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		u.t.Fatal(err)
	}
	if err := os.Rename(u.exe, path); err != nil {
		u.t.Fatal(err)
	}
	u.exe, u.bin = path, path
}

// A failure after the new binary is in place says so, exits 1 and gives the way on.
func TestUpgradeReportsASetupThatFailedAfterTheSwap(t *testing.T) {
	t.Parallel()
	u := newUpgradeEnv(t)
	u.run("init", "--yes", "--harness", "claude-code")
	// The release's aboard runs, but its init fails.
	broken := "#!/bin/sh\ncase \"$1\" in\nversion) echo 'aboard 0.1.0' ;;\ninit) echo 'Error (internal): disk full' >&2; exit 1 ;;\nesac\n"
	u.r.publish(u.releaseEntries(broken))

	r := u.runExit("upgrade", "--json")
	e := errorOf(t, r)
	if r.code != 1 || e["code"] != "upgrade_setup_failed" {
		t.Fatalf("upgrade:\n%s", r)
	}
	if msg := e["message"].(string); !strings.Contains(msg, "aboard was upgraded from "+oldVersion+" to 0.1.0") || !strings.Contains(msg, "disk full") {
		t.Fatalf("message: %q", msg)
	}
	if hint := e["hint"].(string); !strings.Contains(hint, "aboard init --yes") || !strings.Contains(hint, "aboard doctor") {
		t.Fatalf("hint: %q", hint)
	}
	if d := e["details"].(map[string]any); d["from"] != oldVersion || d["to"] != "0.1.0" {
		t.Fatalf("details: %v", d)
	}
	if readFile(t, u.exe) != broken {
		t.Fatal("the new binary isn't in place")
	}

	// The same in text.
	u.install(oldBinary, u.exe)
	text := u.runExit("upgrade")
	if text.code != 1 || !strings.Contains(text.stderr, "Error (upgrade_setup_failed): aboard was upgraded from "+oldVersion+" to 0.1.0, but refreshing the skill and hooks failed") ||
		!strings.Contains(text.stderr, "Hint: Run aboard init --yes to refresh them, then aboard doctor to check.") {
		t.Fatalf("upgrade in text:\n%s", text)
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, s := range v.([]any) {
		out = append(out, s.(string))
	}
	return out
}

// The update notice: once a day, to a person in a terminal, after the command's output.
func TestUpdateNotice(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	r := newInstallRelease(t) // the latest release is installVersion, newer than this aboard
	on := []string{"ABOARD_NO_UPDATE_CHECK=", "ABOARD_DOWNLOAD_URL=" + r.url}
	notice := "aboard " + installVersion + " is available (you have 0.1.0). Run: aboard upgrade"

	term := e.startTerminal(on, "version")
	if code := term.exit(); code != 0 {
		t.Fatalf("exit %d:\n%s", code, term.text())
	}
	if got := term.text(); !strings.HasPrefix(got, "aboard 0.1.0") || !strings.Contains(got, notice) || strings.Index(got, notice) < strings.Index(got, "aboard 0.1.0") {
		t.Fatalf("version in a terminal:\n%q", got)
	}
	var cache struct {
		Latest string `json:"latest"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(e.stateDir(), "update-check.json"))), &cache); err != nil || cache.Latest != installVersion {
		t.Fatalf("update-check.json: %+v %v", cache, err)
	}
	checks := len(r.requests())

	// Once a day: the next command neither checks nor says it again.
	term = e.startTerminal(on, "version")
	if code := term.exit(); code != 0 || strings.Contains(term.text(), "is available") {
		t.Fatalf("second command:\n%s", term.text())
	}
	if len(r.requests()) != checks {
		t.Fatalf("checked again: %v", r.requests())
	}

	// Never for an agent, a script, --json, or with the opt-out; and a release server
	// that can't be reached changes nothing.
	for name, extra := range map[string][]string{
		"in a session":   {"CLAUDECODE=1"},
		"as an agent":    {"ABOARD_AGENT=claude"},
		"opted out":      {"ABOARD_NO_UPDATE_CHECK=1"},
		"no server":      {"ABOARD_DOWNLOAD_URL=http://127.0.0.1:1/releases"},
		"with --json":    nil,
		"not a terminal": nil,
	} {
		f := newEnv(t)
		args := []string{"version"}
		if name == "with --json" {
			args = append(args, "--json")
		}
		var out string
		if name == "not a terminal" {
			res := f.exec(on, "", args...)
			out = res.stdout + res.stderr
		} else {
			term := f.startTerminal(append(slices.Clone(on), extra...), args...)
			if code := term.exit(); code != 0 {
				t.Fatalf("%s: exit %d:\n%s", name, code, term.text())
			}
			out = term.text()
		}
		if strings.Contains(out, "is available") {
			t.Fatalf("%s: showed the notice:\n%s", name, out)
		}
	}
	if len(r.requests()) != checks {
		t.Fatalf("checked for an agent, a script, --json or with the opt-out: %v", r.requests())
	}
}
