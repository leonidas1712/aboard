package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// upgradeOutput is aboard upgrade's --json output (UpgradeOutput in spec/cli.yaml).
type upgradeOutput struct {
	Upgraded         bool         `json:"upgraded"`
	From             string       `json:"from"`
	To               string       `json:"to"`
	Path             string       `json:"path"`
	Launchers        []string     `json:"launchers"`
	SignatureChecked bool         `json:"signature_checked"`
	Setup            upgradeSetup `json:"setup"`
}

type upgradeSetup struct {
	Refreshed bool     `json:"refreshed"`
	Harnesses []string `json:"harnesses"`
}

// upgradeTimeout bounds the downloads of one upgrade.
const upgradeTimeout = 5 * time.Minute

// runUpgrade installs a release over the running binary, the way the install script
// does, and then has the new binary refresh the skill and hooks. Every refusal and check
// comes before anything is written; the programs are renamed into place last.
func runUpgrade(ctx context.Context, a *app, args []string) error {
	use := usageOf("upgrade")
	fs := a.flags("upgrade")
	want := fs.String("version", "", "the version to install, such as 0.2.0 (default: the latest)")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	if err := a.refuseInSession("Upgrading aboard", "aboard upgrade"); err != nil {
		return err
	}
	exe := a.hookExe()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := a.installedFromRelease(exe); err != nil {
		return err
	}
	src, err := a.releaseSource(&http.Client{})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, upgradeTimeout)
	defer cancel()

	target := strings.TrimPrefix(strings.TrimSpace(*want), "v")
	if target != "" && !versionText.MatchString(target) {
		return usageError(fmt.Sprintf("%q is not a version; use one such as 0.2.0.", *want), use)
	}
	if target == "" {
		if target, err = src.latest(ctx); errors.Is(err, errNotFound) {
			return newError("release_not_found", "There is no aboard release for "+osArch()+" yet.",
				"See https://github.com/"+releaseRepo+"/releases.")
		} else if err != nil {
			return downloadError(err)
		}
	}
	out := upgradeOutput{From: version, To: target, Path: exe, Launchers: []string{}, Setup: upgradeSetup{Harnesses: []string{}}}
	if target == version {
		a.emit(out, "aboard "+version+" is already installed; nothing to upgrade.\n")
		return nil
	}

	tmp, err := os.MkdirTemp("", "aboard-upgrade-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	d, err := src.download(ctx, target, tmp)
	if err != nil {
		return err
	}
	out.SignatureChecked = d.signatureChecked
	newExe := filepath.Join(tmp, "aboard")
	if err := os.WriteFile(newExe, d.files["aboard"], 0o700); err != nil { //nolint:gosec // the checked release binary, run once
		return err
	}
	if err := exec.CommandContext(ctx, newExe, "version").Run(); err != nil { //nolint:gosec // the checked release binary
		return newError("archive_invalid", "The aboard in "+archiveName(target)+" doesn't run on this system: "+err.Error()+".",
			"Nothing was changed. Report it at https://github.com/"+releaseRepo+"/issues.")
	}

	names := []string{"aboard"}
	for name := range d.files {
		if strings.HasPrefix(name, "aboard-launcher-") {
			names = append(names, name)
		}
	}
	sort.Strings(names[1:])
	if err := replacePrograms(filepath.Dir(exe), names, d.files); err != nil {
		return err
	}
	out.Upgraded = true
	for _, n := range names[1:] {
		out.Launchers = append(out.Launchers, filepath.Join(filepath.Dir(exe), n))
	}

	out.Setup.Harnesses = a.globalHarnesses()
	if len(out.Setup.Harnesses) > 0 {
		if err := refreshSetup(ctx, exe, out.Setup.Harnesses); err != nil {
			return &Error{
				Code: "upgrade_setup_failed",
				Message: "aboard was upgraded from " + version + " to " + target + ", but refreshing the skill and hooks failed: " +
					err.Error(),
				Hint:    "Run aboard init --yes to refresh them, then aboard doctor to check.",
				Details: map[string]any{"from": version, "to": target},
			}
		}
		out.Setup.Refreshed = true
	}
	a.emit(out, a.upgradeText(out))
	return nil
}

// osArch names this system as release archives do.
func osArch() string {
	return strings.TrimSuffix(strings.TrimPrefix(archiveName(""), "aboard__"), ".tar.gz")
}

// installedFromRelease refuses a binary that another tool installed, naming the command
// that upgrades it instead.
func (a *app) installedFromRelease(exe string) error {
	refuse := func(how, fix string) error {
		return newError("not_installed_by_script", "This aboard ("+exe+") was "+how+", so aboard upgrade leaves it alone.", fix)
	}
	if strings.Contains(exe, "/Cellar/") {
		return refuse("installed with Homebrew", "Upgrade it with: brew upgrade aboard")
	}
	source := "In your aboard checkout, run git pull, then make install, then aboard init --yes."
	if strings.Contains(version, "+dev") {
		return refuse("built from source (a dev build)", source)
	}
	dir := filepath.Dir(exe)
	var goBins []string
	if b := a.env.Getenv("GOBIN"); b != "" {
		goBins = append(goBins, b)
	}
	for _, p := range filepath.SplitList(a.env.Getenv("GOPATH")) {
		if p != "" {
			goBins = append(goBins, filepath.Join(p, "bin"))
		}
	}
	if home := a.env.Getenv("HOME"); home != "" {
		goBins = append(goBins, filepath.Join(home, "go", "bin"))
	}
	for _, b := range goBins {
		if resolved, err := filepath.EvalSymlinks(b); err == nil {
			b = resolved
		}
		if b == dir {
			return refuse("built from source into Go's bin folder", source)
		}
	}
	return nil
}

// replacePrograms writes each program next to the running one into a new file with a
// random name, created exclusively so no link planted beforehand is followed, then
// renames each into place. A failure before the renames removes what it wrote, so the
// old programs stay as they were.
func replacePrograms(dir string, names []string, files map[string][]byte) error {
	parts := make([]string, 0, len(names))
	cleanup := func() {
		for _, p := range parts {
			_ = os.Remove(p)
		}
	}
	for _, n := range names {
		part, err := stageProgram(dir, n, files[n])
		if part != "" {
			parts = append(parts, part)
		}
		if err != nil {
			cleanup()
			return upgradeFailed(dir, err)
		}
	}
	for i, n := range names {
		if err := os.Rename(parts[i], filepath.Join(dir, n)); err != nil {
			cleanup()
			if i == 0 {
				return upgradeFailed(dir, err)
			}
			return fmt.Errorf("aboard was upgraded, but installing %s failed: %w", n, err)
		}
	}
	return nil
}

// stageProgram writes data to a new executable file in dir and returns its path, which
// it also returns with an error once the file exists, so the caller removes it.
func stageProgram(dir, name string, data []byte) (string, error) {
	// CreateTemp opens with O_CREATE|O_EXCL under a random name: a new file this process made.
	f, err := os.CreateTemp(dir, "."+name+".upgrade.*")
	if err != nil {
		return "", err
	}
	part := f.Name()
	_, werr := f.Write(data)
	cerr := f.Chmod(0o755)
	created, serr := f.Stat()
	if err := errors.Join(werr, cerr, serr, f.Close()); err != nil {
		return part, err
	}
	// The path still names the regular file this process created.
	now, err := os.Lstat(part)
	if err != nil {
		return part, err
	}
	if !now.Mode().IsRegular() || !os.SameFile(created, now) {
		return part, fmt.Errorf("%s changed while it was being written", part)
	}
	return part, nil
}

func upgradeFailed(dir string, err error) error {
	return &Error{
		Code: "upgrade_failed", Message: "Couldn't write the new aboard into " + dir + ": " + err.Error() + ".",
		Hint: "Nothing was changed. Make the folder writable, or install with the install script into a folder you own (ABOARD_INSTALL_DIR).", Err: err,
	}
}

// globalHarnesses are the harnesses the install manifest records in the global scope:
// the setup an upgrade refreshes.
func (a *app) globalHarnesses() []string {
	var hs []string
	for _, r := range a.loadManifest().Files {
		if r.Scope == scopeGlobal && r.Harness != "" && !slices.Contains(hs, r.Harness) {
			hs = append(hs, r.Harness)
		}
	}
	sort.Strings(hs)
	if hs == nil {
		return []string{}
	}
	return hs
}

// refreshSetup runs the new binary's aboard init --yes for the harnesses, so the skill
// and hooks are what the new build writes.
func refreshSetup(ctx context.Context, exe string, harnesses []string) error {
	cmd := exec.CommandContext(ctx, exe, "init", "--yes", "--scope", scopeGlobal, "--harness", strings.Join(harnesses, ",")) //nolint:gosec // the binary just installed
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}

// upgradeText is aboard upgrade's text output.
func (a *app) upgradeText(out upgradeOutput) string {
	home := a.env.Getenv("HOME")
	var b strings.Builder
	if out.SignatureChecked {
		b.WriteString("Checked the signature: signed by aboard's release workflow for v" + out.To + ".\n")
	} else {
		b.WriteString("cosign isn't installed, so the checksums' signature wasn't checked, only their SHA-256. Install cosign and aboard upgrade checks it too.\n")
	}
	b.WriteString("Upgraded aboard " + out.From + " to " + out.To + " at " + shortPath(out.Path, home) + ".\n")
	for _, l := range out.Launchers {
		b.WriteString("Installed " + shortPath(l, home) + ".\n")
	}
	if out.Setup.Refreshed {
		b.WriteString("Refreshed the skill and hooks for " + strings.Join(out.Setup.Harnesses, ", ") + ".\n")
	} else {
		b.WriteString("Nothing to refresh: aboard init hasn't set up any harness for every project. Run aboard init to set one up.\n")
	}
	b.WriteString("Restart open sessions so they run the new aboard's skill.\n")
	return b.String()
}
