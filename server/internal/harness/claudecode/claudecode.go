// Package claudecode is Claude Code's harness: its profile, read by the generic
// implementation, and the one thing the profile can't say. Claude Code submits the
// text a waiting stop hook woke the session with as the session's next prompt, so that
// prompt is the wake itself, not a sign the session moved on.
package claudecode

import (
	"strings"

	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Harness is Claude Code.
type Harness struct {
	*harness.Generic
}

// New returns Claude Code's harness.
func New() *Harness { return &Harness{harness.MustLoad("claude-code")} }

// HookCall marks a prompt that carries Aboard's messages as the wake it is.
func (h *Harness) HookCall(event string, in harness.HookInput) (harness.Call, bool) {
	c, ok := h.Generic.HookCall(event, in)
	if ok && c.Op == harness.OpPrompt {
		c.Wake = strings.Contains(in.Prompt, "<aboard-messages")
	}
	return c, ok
}
