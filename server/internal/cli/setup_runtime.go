package cli

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func (a *app) setupRuntimeReady(ctx context.Context, key delivery.SessionKey) bool {
	boot := a.env.Getenv("ABOARD_BOOT")
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID, Boot: boot})
	// A read cannot establish trust. Old daemons omit the flag, and an old boot cannot
	// confirm the hooks loaded by this session after its restart.
	return err == nil && resp.RuntimeReady && (boot == "" || resp.Boot == boot)
}

func (out *setupOutput) confirmSetupHarness(command string) {
	out.Steps[3].State = "complete"
	out.Steps[3].Message = "The current session's hooks or extension were confirmed by the delivery daemon. Run aboard skill now; it loads automatically in your next session."
	out.skillInstalled = true
	out.Next = &api.NextStep{Command: command, Resume: "Run aboard skill now; it loads automatically in your next session. Continue Aboard setup to check delivery independently."}
}
