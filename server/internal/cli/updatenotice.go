package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The update notice: at most once a day, a command a person runs in a terminal checks
// for a newer release and says so in one line on standard error, after the command's
// own output. It never runs for an agent, a script or --json, so nothing an agent or a
// program reads ever changes, and it never installs anything: aboard upgrade does that.

const (
	// updateCheckEvery is how often the latest release is looked up, and the notice shown.
	updateCheckEvery = 24 * time.Hour
	// updateCheckTimeout bounds the lookup, which runs alongside the command.
	updateCheckTimeout = 2 * time.Second
)

// noUpdateCheck are the commands that never check: the ones aboard and harnesses run
// rather than a person, and upgrade, which checks anyway.
var noUpdateCheck = []string{"hook", "daemon", "serve", "upgrade", "skill"}

// updateCheck is update-check.json in the state folder.
type updateCheck struct {
	// CheckedAt is when the latest release was last looked up, whether or not that worked.
	CheckedAt time.Time `json:"checked_at"`
	// Latest is the latest release's version the last lookup that worked found.
	Latest string `json:"latest,omitempty"`
	// NotifiedAt is when the notice was last shown.
	NotifiedAt time.Time `json:"notified_at,omitzero"`
}

func (p paths) updateCheck() string { return filepath.Join(p.state, "update-check.json") }

// startUpdateCheck starts the day's lookup of the latest release when command, run by a
// person in a terminal, should have one, and returns what to run after the command: it
// waits for the lookup, briefly, and shows the notice. Any failure is silent.
func (a *app) startUpdateCheck(ctx context.Context, command string) func() {
	nothing := func() {}
	if !a.updateNoticeWanted(command) {
		return nothing
	}
	p, err := a.paths()
	if err != nil {
		return nothing
	}
	var c updateCheck
	if raw, err := os.ReadFile(filepath.Clean(p.updateCheck())); err == nil {
		_ = json.Unmarshal(raw, &c)
	}
	now := time.Now()
	found := make(chan string, 1)
	due := now.Sub(c.CheckedAt) >= updateCheckEvery || c.CheckedAt.After(now)
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	if due {
		go func() {
			latest := ""
			if src, err := a.releaseSource(&http.Client{}); err == nil {
				latest, _ = src.latest(ctx)
			}
			found <- latest
		}()
	}
	return func() {
		defer cancel()
		changed := false
		if due {
			select {
			case latest := <-found:
				if latest != "" {
					c.Latest = latest
				}
			case <-ctx.Done():
			}
			c.CheckedAt, changed = now, true
		}
		if c.Latest != "" && compareVersions(c.Latest, version) > 0 && now.Sub(c.NotifiedAt) >= updateCheckEvery {
			_, _ = io.WriteString(a.env.Stderr, a.errStyles().dim("aboard "+c.Latest+" is available (you have "+version+"). Run: aboard upgrade")+"\n")
			c.NotifiedAt, changed = now, true
		}
		if changed {
			if raw, err := json.Marshal(c); err == nil && os.MkdirAll(p.state, 0o700) == nil {
				_ = os.WriteFile(p.updateCheck(), raw, 0o600)
			}
		}
	}
}

// updateNoticeWanted reports whether command may check for a newer release: run by a
// person at a terminal, without --json, not for an agent, not opted out with
// ABOARD_NO_UPDATE_CHECK, and from a release build rather than a dev build.
func (a *app) updateNoticeWanted(command string) bool {
	switch {
	case slices.Contains(noUpdateCheck, command),
		a.json, !a.env.Terminal, !a.env.StderrTerminal,
		strings.Contains(version, "+dev"):
		return false
	}
	if off := strings.TrimSpace(a.env.Getenv("ABOARD_NO_UPDATE_CHECK")); off != "" && off != "0" {
		return false
	}
	return !a.actsForAgent()
}
