package cli

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func (a *app) setupRuntimeReady(ctx context.Context, key delivery.SessionKey, setups []harnessSetup) bool {
	for _, setup := range setups {
		for _, change := range setup.Changes {
			if (change.Kind == "hooks" || change.Kind == "file") && change.Action != actionUnchanged {
				return false
			}
		}
	}
	boot := a.env.Getenv("ABOARD_BOOT")
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID, Boot: boot})
	return err == nil && resp.RuntimeReady && (boot == "" || boot == resp.Boot)
}

func (out *setupOutput) confirmSetupHarness(command string) {
	out.Steps[3].State = "complete"
	out.Steps[3].Message = "The current session's hooks or extension were confirmed by the delivery daemon. Run aboard skill now; it loads automatically in your next session."
	out.skillInstalled = true
	out.Next = &api.NextStep{Command: command, Resume: "Run aboard skill now; it loads automatically in your next session. Continue Aboard setup to check delivery independently."}
}
