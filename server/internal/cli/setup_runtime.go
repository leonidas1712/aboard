package cli

import (
	"github.com/leonidas1712/aboard/server/internal/api"
)

func (out *setupOutput) confirmSetupHarness(command string) {
	out.Steps[3].State = "complete"
	out.Steps[3].Message = "The current session's hooks or extension were confirmed by the delivery daemon. Run aboard skill now; it loads automatically in your next session."
	out.skillInstalled = true
	out.Next = &api.NextStep{Command: command, Resume: "Run aboard skill now; it loads automatically in your next session. Continue Aboard setup to check delivery independently."}
}
