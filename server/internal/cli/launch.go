package cli

import (
	"context"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
)

// headlessEnv marks a turn the headless launcher's runner started: the runner delivers
// the agent's messages itself, so the harness's Aboard hooks do nothing in it.
const headlessEnv = "ABOARD_HEADLESS"

// launchTicket is the launch ticket this command's session was started with, when the
// ticket is still waiting to be taken.
func (a *app) launchTicket() string {
	ticket := a.env.Getenv(launchtickets.Env)
	if !launchtickets.Valid(ticket) {
		return ""
	}
	p, err := a.paths()
	if err != nil || !launchtickets.Dir(p.launches()).Exists(ticket) {
		return ""
	}
	return ticket
}

// claimLaunch hands in the session's launch ticket from a command, for a harness whose
// hooks don't see the environment the harness was started with but whose every command
// carries the session's id (Codex): the first aboard command such a session runs binds
// it to the agent swarm up started it for. Claude Code's session-start hook and omp's
// extension hand the ticket in themselves as the session starts. A command that can't
// hand it in goes on as it would have: ABOARD_AGENT still names the agent.
func (a *app) claimLaunch(ctx context.Context) {
	ticket := a.launchTicket()
	if ticket == "" {
		return
	}
	key, ok := a.sessionKey()
	if !ok {
		return
	}
	if h, known := a.registry().Get(key.Harness); !known || h.Profile().Identity.Kind != "env" {
		return
	}
	_, _ = a.callDaemon(ctx, delivery.Request{Op: delivery.OpRegister, Harness: key.Harness, Session: key.ID, Launch: ticket})
}
