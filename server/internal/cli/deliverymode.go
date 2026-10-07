package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
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

// withArticle puts "a" or "an" before a harness's name: "a Codex", "an omp".
func withArticle(name string) string {
	if name != "" && strings.ContainsRune("aeiouAEIOU", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// actsForAgent reports whether the command runs for an agent rather than its person:
// inside a harness session, or with ABOARD_AGENT naming an agent, which is how an
// agent's environment says who it is. A person's own command refuses then.
func (a *app) actsForAgent() bool {
	_, in := a.inSession()
	return in || strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")) != ""
}

// refuseInSession refuses a command that is up to a person (it acts or reads with the
// human login, or changes a delivery mode) when it runs inside a harness session, where
// an allow rule for aboard would let an agent run it without asking, or when
// ABOARD_AGENT names an agent, which is how an agent's environment says who it is. It
// refuses before anything reads the person's key. what says what the command does;
// command is the command to run in a terminal instead.
func (a *app) refuseInSession(what, command string) error {
	if agent := strings.TrimSpace(a.env.Getenv("ABOARD_AGENT")); agent != "" {
		return newError("human_command_in_session",
			what+" is up to a person, and ABOARD_AGENT says this command runs as the agent "+agent+".",
			"Give your human this command to run in their own terminal, without ABOARD_AGENT set: "+command)
	}
	harness, in := a.inSession()
	if !in {
		return nil
	}
	return newError("human_command_in_session",
		what+" is up to a person, and this command runs inside "+withArticle(harness)+" session.",
		"Give your human this command to run in their own terminal, outside any agent session: "+command)
}

// modeText explains what each delivery mode does, for text output.
var modeText = map[delivery.Mode]string{
	delivery.ModeFocused: "wakes for messages that concern it; the rest arrive at its next turn",
	delivery.ModeAll:     "wakes for every message",
	delivery.ModeHumans:  "wakes only for messages from people",
	delivery.ModeOff:     "delivers nothing; the agent reads its inbox itself",
}

// deliveryOutput is aboard delivery's --json output (DeliveryOutput in spec/cli.yaml).
type deliveryOutput struct {
	Board   string        `json:"board"`
	Agent   string        `json:"agent"`
	Mode    delivery.Mode `json:"mode"`
	Changed bool          `json:"changed"`
	// Revision is the server's revision of the mode, or nil when the server doesn't hold
	// delivery modes and the mode is this machine's.
	Revision *int64 `json:"revision"`
	// Unconfirmed is true when the agent's server couldn't be read, so the mode shown is
	// the one this machine kept.
	Unconfirmed bool `json:"unconfirmed"`
}

// runDelivery shows an agent's delivery mode, or changes it. The agent's server holds
// the mode, so a change made here, with the person's own login, reaches the agent's
// delivery daemon on whichever machine runs it. Changing it is a person's decision, so
// it refuses where an agent runs it: inside a harness session, or with ABOARD_AGENT set.
func runDelivery(ctx context.Context, a *app, args []string) error {
	use := usageOf("delivery")
	fs := a.flags("delivery")
	as := fs.String("as", "", "the agent whose delivery mode to show or change; any of your agents, on this machine or another")
	boardFlag := fs.String("board", "", "the agent's board, when its name is used on several or it runs on another machine")
	pos, err := a.parse(fs, args, use, 0, 1)
	if err != nil {
		return err
	}
	var want delivery.Mode
	if len(pos) == 1 {
		m, ok := delivery.ParseMode(pos[0])
		if !ok {
			return usageError(fmt.Sprintf("%q is not a delivery mode; use focused, all, humans or off.", pos[0]), use)
		}
		want = m
		if a.actsForAgent() {
			agent := "AGENT"
			if _, cred, err := a.agentTarget(ctx, *boardFlag, *as); err == nil {
				agent = cred.Name
			}
			return a.refuseInSession("Changing the delivery mode", "aboard delivery "+string(m)+" --as "+agent)
		}
	}
	t, cred, here, err := a.deliveryTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	out := deliveryOutput{Board: cred.Board, Agent: cred.Name}
	if want == "" {
		out.Mode, out.Revision, out.Unconfirmed, err = a.showDelivery(ctx, t, cred, here)
	} else {
		err = a.changeDelivery(ctx, t, cred, here, want, &out)
	}
	if err != nil {
		return err
	}
	now := ""
	if out.Changed {
		now = "now "
	}
	kept := ""
	if out.Unconfirmed {
		kept = keptHereText
	}
	a.emit(out, fmt.Sprintf("%s on %s: delivery %s%s%s (%s)\n", out.Agent, out.Board, now, out.Mode, kept, modeText[out.Mode]))
	return nil
}

// deliveryTarget finds the agent aboard delivery acts on: one of this machine's agents
// (here is then true), or, named with --as in a person's own terminal, any agent of the
// person's on the board that --board or this directory's .aboard file names, reached
// with the person's login. A command that acts for an agent (inside a harness session,
// or with ABOARD_AGENT set) never falls back to the person's login.
func (a *app) deliveryTarget(ctx context.Context, boardFlag, as string) (t target, cred agentCredential, here bool, err error) {
	t, cred, err = a.agentTarget(ctx, boardFlag, as)
	if err == nil {
		return t, cred, true, nil
	}
	name := strings.TrimPrefix(strings.TrimSpace(as), "@")
	if name == "" || asError(err).Code != "agent_not_selected" || a.actsForAgent() {
		return target{}, agentCredential{}, false, err
	}
	if t, err = a.selectBoard(boardFlag); err != nil {
		return target{}, agentCredential{}, false, err
	}
	return t, agentCredential{Server: t.server.URL, Board: t.board, Name: name}, false, nil
}

// showDelivery reads an agent's delivery mode from its server: with the agent's own
// token when this machine has it, else from the board's members with the person's
// login. A server that doesn't hold delivery modes leaves this machine's journal to say,
// and the revision nil; unconfirmed is true when the server couldn't be read and the
// journal's mode is shown instead.
func (a *app) showDelivery(ctx context.Context, t target, cred agentCredential, here bool) (mode delivery.Mode, rev *int64, unconfirmed bool, err error) {
	if !here {
		c, err := a.humanClient(ctx, t)
		if err != nil {
			return "", nil, false, err
		}
		rctx, cancel := context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		r, err := c.api.ListMembersWithResponse(rctx, t.board, nil)
		if err != nil {
			return "", nil, false, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return "", nil, false, apiError(r.StatusCode(), r.Body)
		}
		for _, m := range r.JSON200.Members {
			if m.Name == cred.Name && m.Kind == "agent" && m.DeliveryMode != nil {
				rev := revisionOf(m.DeliveryRevision)
				return delivery.Mode(*m.DeliveryMode), &rev, false, nil
			}
		}
		return "", nil, false, newError("agent_not_found", fmt.Sprintf("Board %s has no agent called %s.", t.board, cred.Name),
			"Run aboard board people --board "+t.board+" or look at the board view to see its agents.")
	}
	held, ok, reached := a.readHeldMode(ctx, t, cred)
	if ok {
		return held.Mode, &held.Revision, false, nil
	}
	mode, err = a.deliveryMode(ctx, delivery.AgentRef{Server: t.server.URL, Board: cred.Board, Name: cred.Name, MemberID: cred.MemberID})
	return mode, nil, !reached, err
}

// keptHereText marks a mode shown from this machine's journal because the agent's
// server couldn't be read.
const keptHereText = " (kept on this machine; the server couldn't be reached)"

// heldMode reads the agent's delivery mode as its server holds it, with the agent's
// token. ok is false when the server doesn't hold modes, or couldn't be asked.
func (a *app) heldMode(ctx context.Context, t target, cred agentCredential) (delivery.HeldMode, bool) {
	h, ok, _ := a.readHeldMode(ctx, t, cred)
	return h, ok
}

// readHeldMode is heldMode, and also reports whether the server answered at all, so a
// mode it couldn't confirm is marked as this machine's.
func (a *app) readHeldMode(ctx context.Context, t target, cred agentCredential) (h delivery.HeldMode, ok, reached bool) {
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return delivery.HeldMode{}, false, false
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.GetMeWithResponse(rctx)
	if err != nil || r.JSON200 == nil {
		return delivery.HeldMode{}, false, false
	}
	if r.JSON200.DeliveryMode == nil {
		return delivery.HeldMode{}, false, true
	}
	return delivery.HeldMode{Mode: delivery.Mode(*r.JSON200.DeliveryMode), Revision: revisionOf(r.JSON200.DeliveryRevision)}, true, true
}

// changeDelivery sets an agent's delivery mode on its server with the person's login,
// then tells this machine's delivery daemon, if it runs, so the change applies here at
// once rather than when the daemon reads the server next. A server that doesn't hold
// delivery modes gets the mode kept on this machine instead, as before servers held
// them.
func (a *app) changeDelivery(ctx context.Context, t target, cred agentCredential, here bool, want delivery.Mode, out *deliveryOutput) error {
	c, err := a.humanClient(ctx, t)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.SetDeliveryModeWithResponse(rctx, cred.Board, cred.Name, nil, api.SetDeliveryModeJSONRequestBody{Mode: api.DeliveryModeSetting(want)})
	if err != nil {
		return c.unreachable(err)
	}
	ref := delivery.AgentRef{Server: t.server.URL, Board: cred.Board, Name: cred.Name, MemberID: cred.MemberID}
	if r.JSON200 == nil {
		e := apiError(r.StatusCode(), r.Body)
		if here && (e.Code == "not_found" || e.Code == "not_implemented") {
			return a.changeDeliveryHere(ctx, ref, want, out)
		}
		return e
	}
	set := r.JSON200
	rev := int64(set.Revision)
	out.Mode, out.Changed, out.Revision = delivery.Mode(set.Mode), set.Changed, &rev
	a.tellRunningDaemon(ctx, delivery.Request{Op: delivery.OpMode, Agent: &ref, Mode: out.Mode, Revision: rev})
	return nil
}

// changeDeliveryHere keeps an agent's delivery mode in this machine's journal, through
// the delivery daemon, for a server that doesn't hold delivery modes.
func (a *app) changeDeliveryHere(ctx context.Context, ref delivery.AgentRef, want delivery.Mode, out *deliveryOutput) error {
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
	if m, ok := delivery.ParseMode(string(out.Mode)); ok {
		out.Mode = m // an older daemon answers auto for all
	}
	return nil
}

// tellRunningDaemon sends one request to this machine's delivery daemon if it runs,
// without starting one. A daemon that isn't running, or doesn't answer, reads the
// change from the server when it next starts.
func (a *app) tellRunningDaemon(ctx context.Context, req delivery.Request) {
	p, err := a.paths()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := control.Dial(ctx, p.socket())
	if err != nil {
		return
	}
	defer func() { _ = c.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	req.V = delivery.ProtocolVersion
	if delivery.WriteFrame(c, req) != nil {
		return
	}
	var resp delivery.Response
	_ = delivery.ReadFrame(bufio.NewReader(c), &resp)
}

// deliveryMode reads an agent's delivery mode from the daemon's journal, without
// starting the daemon: what this machine kept for it, set here or last read from its
// server. An agent without its own mode has the machine's default, kept under the empty
// AgentRef, else focused. A mode saved as auto reads as all.
func (a *app) deliveryMode(ctx context.Context, agent delivery.AgentRef) (delivery.Mode, error) {
	modes, err := a.journalModes(ctx)
	if err != nil {
		return "", err
	}
	for _, ref := range []delivery.AgentRef{agent, {}} {
		if m, ok := journalMode(modes, ref); ok {
			if parsed, ok := delivery.ParseMode(string(m)); ok {
				return parsed, nil
			}
		}
	}
	return delivery.ModeFocused, nil
}

func journalMode(modes map[delivery.AgentRef]delivery.Mode, agent delivery.AgentRef) (delivery.Mode, bool) {
	for ref, mode := range modes {
		if ref.Key() == agent.Key() {
			return mode, true
		}
	}
	return "", false
}

// journalModes reads every delivery mode the daemon's journal keeps.
func (a *app) journalModes(ctx context.Context) (map[delivery.AgentRef]delivery.Mode, error) {
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	modes, err := sqlitejournal.ReadModes(ctx, p.deliveryDB())
	if err != nil {
		return nil, fmt.Errorf("read delivery modes: %w", err)
	}
	return modes, nil
}

// seatDelivery is an agent's delivery mode and its rule, as the commands that seat an
// agent show them (DeliveryRule in spec/cli.yaml).
type seatDelivery struct {
	Mode delivery.Mode `json:"delivery"`
	Rule string        `json:"delivery_rule"`
}

// deliveryFor is the agent's delivery mode for a command that seats it: held, the mode
// its server holds as the command read it ("" from a server that doesn't hold modes),
// else this machine's journal. A journal that can't be read gives focused, the default:
// the command has seated the agent by then, and the line is advice.
func (a *app) deliveryFor(ctx context.Context, agent delivery.AgentRef, held string) seatDelivery {
	mode := delivery.ModeFocused
	if m, ok := delivery.ParseMode(held); ok {
		mode = m
	} else if m, err := a.deliveryMode(ctx, agent); err == nil {
		mode = m
	}
	return seatDelivery{Mode: mode, Rule: deliverytext.ModeRule(string(mode))}
}

// line is the text line naming the mode and its rule.
func (d seatDelivery) line() string { return deliverytext.ModeLine(string(d.Mode)) + "\n" }

// revisionOf is a mode's revision as the API gives it, 0 when it gives none.
func revisionOf(rev *int) int64 {
	if rev == nil {
		return 0
	}
	return int64(*rev)
}

// heldModeOf is the delivery mode a member's server holds for it, or "" from a server
// that doesn't hold modes.
func heldModeOf(m api.Member) string {
	if m.DeliveryMode == nil {
		return ""
	}
	return string(*m.DeliveryMode)
}
