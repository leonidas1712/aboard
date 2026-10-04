package cli

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
)

// headlessEnv marks a turn the headless launcher's runner started: the runner delivers
// the agent's messages itself, so the harness's Aboard hooks do nothing in it.
const headlessEnv = "ABOARD_HEADLESS"

// launchTicket is the launch ticket this command's session was started with, when the
// ticket is still waiting to be taken.
func (a *app) launchTicket() string {
	return a.waitingTicket(a.env.Getenv(launchtickets.Env))
}

// waitingTicket is ticket when it has a ticket's shape and is still waiting to be taken,
// else "".
func (a *app) waitingTicket(ticket string) string {
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
// carries the session's id: the first aboard command such a session runs binds it to
// the agent swarm up started it for. Claude Code's session-start hook and omp's
// extension hand the ticket in themselves as the session starts, and Codex gets its
// ticket in its first prompt instead (claimTicket). A command that can't hand it in goes
// on as it would have: ABOARD_AGENT still names the agent.
func (a *app) claimLaunch(ctx context.Context) {
	a.claimTicket(ctx, a.launchTicket())
}

// claimTicket hands in a waiting launch ticket for the session this command runs in, so
// the daemon binds the session to the ticket's agent. It does nothing for a ticket that
// is gone (taken already, by the session's prompt hook for one), or outside a session
// whose commands carry its id.
func (a *app) claimTicket(ctx context.Context, ticket string) {
	if ticket = a.waitingTicket(ticket); ticket == "" {
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

// promptLaunch finds, on a prompt's first line, the command swarm up's first prompt
// gives a session whose launch ticket travels in the prompt (launchPrompt). Only a first
// line that starts as launchPrompt's does counts, so a ticket quoted in a message, which
// arrives in a later prompt inside an <aboard-message> element, is never taken.
var promptLaunch = regexp.MustCompile(`^You are .*\baboard status --launch (lch_[0-9a-f]{24})\b`)

// ticketInPrompt is the launch ticket a session's first prompt hands in, or "".
func ticketInPrompt(prompt string) string {
	first, _, _ := strings.Cut(prompt, "\n")
	if m := promptLaunch.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

// launchPrompt is the first prompt of a session whose launch ticket travels in the
// prompt (a profile's interactive.launch is prompt): its first line asks the agent to
// run aboard status --launch with the ticket. The prompt hook hands the ticket in as the
// turn starts, before the model runs; the command hands it in when no hook ran (the
// harness's hooks aren't trusted yet). The ticket works once and only on this machine,
// where it names a file in Aboard's state.
func launchPrompt(spec swarmSpec, board, ticket string) string {
	if spec.Prompt == nil {
		return fmt.Sprintf("You are %s on the Aboard board %s, started by aboard swarm up. Run aboard status --launch %s now: "+
			"it takes your seat (the ticket works once). Then wait: messages from the board arrive in this session.", spec.Name, board, ticket)
	}
	line := fmt.Sprintf("You are %s on the Aboard board %s. Run aboard status --launch %s first: it takes your seat (the ticket works once).",
		spec.Name, board, ticket)
	if strings.TrimSpace(*spec.Prompt) == "" {
		return line
	}
	return line + "\n\n" + *spec.Prompt
}
