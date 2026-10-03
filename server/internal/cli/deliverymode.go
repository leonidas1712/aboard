package cli

import (
	"context"
	"fmt"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

// inSession reports the harness whose session this command runs in, if any, from the
// variables each harness's profile lists for its sessions and its sandbox. harness is
// "harness" when the session is of a harness Aboard doesn't know by name, such as one
// that sets another harness's variables too.
func (a *app) inSession() (harness string, ok bool) {
	title, ok := a.registry().InSession(a.henv())
	if ok && title == "" {
		title = "harness"
	}
	return title, ok
}

// refuseInSession refuses a command that is up to a person (it acts or reads with the
// human login, or changes a delivery mode) when it runs inside a harness session, where
// an allow rule for aboard would let an agent run it without asking. what says what the
// command does; command is the command to run in a terminal instead.
func (a *app) refuseInSession(what, command string) error {
	harness, in := a.inSession()
	if !in {
		return nil
	}
	return newError("human_command_in_session",
		what+" is up to a person, and this command runs inside a "+harness+" session.",
		"Give your human this command to run in their own terminal, outside any agent session: "+command)
}

// modeText explains what each delivery mode does, for text output.
var modeText = map[delivery.Mode]string{
	delivery.ModeAuto:   "wakes for every message",
	delivery.ModeHumans: "wakes only for messages from people",
	delivery.ModeOff:    "delivers nothing; the agent reads its inbox itself",
}

// runDelivery shows the acting agent's delivery mode, or changes it. Changing it is a
// person's decision, so it refuses inside a harness session, where an agent runs it.
func runDelivery(ctx context.Context, a *app, args []string) error {
	use := usageOf("delivery")
	fs := a.flags("delivery")
	as := fs.String("as", "", "the agent whose delivery mode to show or change")
	boardFlag := fs.String("board", "", "the agent's board, when its name is used on several")
	pos, err := a.parse(fs, args, use, 0, 1)
	if err != nil {
		return err
	}
	var want delivery.Mode
	if len(pos) == 1 {
		m, ok := delivery.ParseMode(pos[0])
		if !ok {
			return usageError(fmt.Sprintf("%q is not a delivery mode; use auto, humans or off.", pos[0]), use)
		}
		want = m
		if _, in := a.inSession(); in {
			agent := "AGENT"
			if _, cred, err := a.agentTarget(ctx, *boardFlag, *as); err == nil {
				agent = cred.Name
			}
			return a.refuseInSession("Changing the delivery mode", "aboard delivery "+string(m)+" --as "+agent)
		}
	}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	ref := delivery.AgentRef{Server: t.server.URL, Board: cred.Board, Name: cred.Name}
	out := struct {
		Board   string        `json:"board"`
		Agent   string        `json:"agent"`
		Mode    delivery.Mode `json:"mode"`
		Changed bool          `json:"changed"`
	}{Board: cred.Board, Agent: cred.Name}
	if want == "" {
		if out.Mode, err = a.deliveryMode(ctx, ref); err != nil {
			return err
		}
	} else {
		resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpMode, Agent: &ref, Mode: want})
		if err != nil {
			if asError(err).Code == "invalid_request" {
				return newError("daemon_outdated",
					"The delivery daemon running on this machine is from an older aboard and can't change delivery modes.",
					"Run aboard down to stop it, then run this command again; it starts the current daemon.")
			}
			return err
		}
		out.Mode, out.Changed = resp.Mode, resp.Changed
	}
	now := ""
	if out.Changed {
		now = "now "
	}
	a.emit(out, fmt.Sprintf("%s on %s: delivery %s%s (%s)\n", out.Agent, out.Board, now, out.Mode, modeText[out.Mode]))
	return nil
}

// deliveryMode reads an agent's delivery mode from the daemon's journal, without
// starting the daemon. An agent without its own mode has the machine's default, kept
// under the empty AgentRef.
func (a *app) deliveryMode(ctx context.Context, agent delivery.AgentRef) (delivery.Mode, error) {
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	modes, err := sqlitejournal.ReadModes(ctx, p.deliveryDB())
	if err != nil {
		return "", fmt.Errorf("read delivery modes: %w", err)
	}
	if m, ok := modes[agent]; ok {
		return m, nil
	}
	if m, ok := modes[delivery.AgentRef{}]; ok {
		return m, nil
	}
	return delivery.ModeAuto, nil
}
