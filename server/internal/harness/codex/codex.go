// Package codex is Codex's harness: its profile, read by the generic implementation,
// and what the profile can't say. Delivery goes through `codex queue`, and a thread is
// read through Codex's app server before an agent is bound to it; doctor checks the
// codex command and its queue; and a delivery stopped for a thread that is gone or a
// sub-agent is fixed in Codex.
package codex

import (
	"context"
	"os/exec"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	deliverycodex "github.com/leonidas1712/aboard/server/internal/delivery/codex"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Harness is Codex.
type Harness struct {
	*harness.Generic
}

// New returns Codex's harness.
func New() *Harness { return &Harness{harness.MustLoad("codex")} }

// InstalledChecks checks that codex is on the PATH and has the queue command that
// delivery needs.
func (h *Harness) InstalledChecks(ctx context.Context, _ harness.Env) ([]harness.CheckResult, bool) {
	if _, err := exec.LookPath("codex"); err != nil {
		return []harness.CheckResult{{
			Name: "codex", Level: harness.LevelWarning, Code: "codex_not_installed",
			Message: "codex: not on the PATH", Fix: "install Codex, or ignore this if you don't use it",
		}}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cx := &deliverycodex.Adapter{}
	ver, err := cx.Version(ctx)
	if err != nil {
		return []harness.CheckResult{{
			Name: "codex", Level: harness.LevelWarning, Code: "codex_not_installed",
			Message: "codex: " + err.Error(), Fix: "reinstall Codex, or ignore this if you don't use it",
		}}, false
	}
	if !cx.HasQueue(ctx) {
		return []harness.CheckResult{{
			Name: "codex", Level: harness.LevelError, Code: "codex_queue_missing",
			Message: ver + ": no queue command, so messages can't be delivered to Codex", Fix: "update Codex",
		}}, false
	}
	return []harness.CheckResult{{Name: "codex", Level: harness.LevelOK, Message: ver + ": queue available"}}, true
}

// Adapter queues bundles with `codex queue`.
func (h *Harness) Adapter(clientVersion string) delivery.Adapter {
	return &deliverycodex.Adapter{ClientVersion: clientVersion}
}

// Fix says how to fix a delivery the Codex adapter stopped.
func (h *Harness) Fix(reason string) string {
	switch reason {
	case delivery.ReasonTargetAbsent:
		return "open that Codex thread again, or rejoin with aboard join"
	case delivery.ReasonSubAgent:
		return "run aboard resume from the root Codex conversation"
	}
	return ""
}
