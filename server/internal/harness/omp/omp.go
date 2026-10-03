// Package omp is omp's (oh-my-pi's) harness: its profile, read by the generic
// implementation, and the one check the profile can't say. omp has no hooks; Aboard's
// extension inside it gives sessions their identity and holds their delivery
// connection, and it relies on how omp passes environment to commands, which only omp
// 18.5.1 and later do, so doctor checks omp's version.
package omp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Harness is omp.
type Harness struct {
	*harness.Generic
}

// New returns omp's harness.
func New() *Harness { return &Harness{harness.MustLoad("omp")} }

// InstalledChecks checks omp runs and is new enough for Aboard's extension.
func (h *Harness) InstalledChecks(ctx context.Context, e harness.Env) ([]harness.CheckResult, bool) {
	p := h.Profile()
	if !h.Detected(e) {
		return []harness.CheckResult{{
			Name: p.CheckName, Level: harness.LevelWarning, Code: "omp_not_installed",
			Message: "omp: not installed", Fix: "install omp, or ignore this if you don't use it",
		}}, false
	}
	path := e.LookPath(p.Command)
	if path == "" {
		// Its agent folder is there, but omp isn't on the PATH here: the extension is still
		// checked.
		return nil, true
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, p.Checks.Installed.Run[1:]...) //nolint:gosec // the harness's own command, from its profile
	cmd.Env = append(os.Environ(), "HOME="+e.Home(), "PATH="+e.Getenv("PATH"))
	out, err := cmd.Output()
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	switch {
	case err != nil || version == "":
		return []harness.CheckResult{{
			Name: p.CheckName, Level: harness.LevelWarning, Code: "omp_not_installed",
			Message: "omp: omp --version didn't run", Fix: "reinstall omp, or ignore this if you don't use it",
		}}, false
	case !harness.VersionAtLeast(version, p.Checks.MinVersion):
		return []harness.CheckResult{{
			Name: p.CheckName, Level: harness.LevelWarning, Code: "omp_outdated",
			Message: version + " is older than " + p.Checks.MinVersion + ", the first omp whose commands carry the session Aboard's extension sets",
			Fix:     "update omp with omp update",
		}}, true
	}
	return []harness.CheckResult{{Name: p.CheckName, Level: harness.LevelOK, Message: version}}, true
}
