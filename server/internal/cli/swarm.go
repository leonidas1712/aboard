package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
	"github.com/leonidas1712/aboard/server/internal/harness"
	"github.com/leonidas1712/aboard/server/internal/launcher"
	"github.com/leonidas1712/aboard/server/internal/launcher/external"
	"github.com/leonidas1712/aboard/server/internal/launcher/headless"
	"github.com/leonidas1712/aboard/server/internal/launcher/tmux"
)

// swarmUsage is the usage of "aboard swarm".
var swarmUsage = usageOf("swarm")

// defaultLauncher is the launcher of agents whose board file names none.
const defaultLauncher = tmux.Name

// defaultSwarmWait is how long swarm up waits for every agent to take its seat.
const defaultSwarmWait = 2 * time.Minute

// runSwarm runs "aboard swarm up|ps|down|runner".
func runSwarm(ctx context.Context, a *app, args []string) error {
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		// --help, -h or --json before the subcommand: help, or a usage error.
		if _, err := a.parse(a.flags("swarm"), args, swarmUsage, 0, 0); err != nil {
			return err
		}
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return usageError("Name what to do: aboard swarm up, ps or down.", swarmUsage)
	}
	switch args[0] {
	case "up":
		return runSwarmUp(ctx, a, args[1:])
	case "ps":
		return runSwarmPs(ctx, a, args[1:])
	case "down":
		return runSwarmDown(ctx, a, args[1:])
	case "runner":
		return runSwarmRunner(ctx, a, args[1:])
	}
	return usageError(fmt.Sprintf("%q is not a swarm command.", args[0]), swarmUsage)
}

// swarmRecord is what this machine keeps about a swarm it started: which launcher holds
// each agent's session, and the launcher's handle for it. It holds no token.
type swarmRecord struct {
	Server string                       `json:"server"`
	Board  string                       `json:"board"`
	File   string                       `json:"file"`
	Agents map[string]*swarmAgentRecord `json:"agents"`
}

// swarmAgentRecord is one agent of a swarm record.
type swarmAgentRecord struct {
	Harness  string `json:"harness"`
	Role     string `json:"role"`
	Launcher string `json:"launcher"`
	Mode     string `json:"mode"`
	Dir      string `json:"dir"`
	Handle   string `json:"handle,omitempty"`
	Attach   string `json:"attach,omitempty"`
	// Start is fresh or resumed: how its session last started.
	Start string `json:"start,omitempty"`
	// StartNote says why an agent that had a session started fresh.
	StartNote string `json:"start_note,omitempty"`
	// Ticket is the launch ticket the session was started with, removed by down if no
	// session took it.
	Ticket    string    `json:"ticket,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
}

// swarmName names a board's swarm on this machine: what launchers call its tmux server
// or herdr session. The hash keeps two servers' boards of one name, or two Aboard homes
// on one machine, apart.
func swarmName(serverURL, stateDir, board string) string {
	sum := sha256.Sum256([]byte(serverURL + "\n" + stateDir))
	return "aboard-" + board + "-" + hex.EncodeToString(sum[:3])
}

func (a *app) swarmPath(name string) (string, error) {
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	return filepath.Join(p.swarms(), name+".json"), nil
}

func (a *app) readSwarm(name string) (*swarmRecord, bool, error) {
	path, err := a.swarmPath(name)
	if err != nil {
		return nil, false, err
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return &swarmRecord{Agents: map[string]*swarmAgentRecord{}}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	var r swarmRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	if r.Agents == nil {
		r.Agents = map[string]*swarmAgentRecord{}
	}
	return &r, true, nil
}

func (a *app) saveSwarm(name string, r *swarmRecord) error {
	path, err := a.swarmPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the swarm record: %w", err)
	}
	return writeFileAtomic(path, append(raw, '\n'), 0o600)
}

// launcherFor finds a launcher by name: a built-in one, else aboard-launcher-<name> on
// the PATH.
func (a *app) launcherFor(name string) (launcher.Launcher, error) {
	switch name {
	case tmux.Name:
		return tmux.Launcher{}, nil
	case headless.Name:
		p, err := a.paths()
		if err != nil {
			return nil, err
		}
		return headless.Launcher{Dir: filepath.Join(p.swarms(), "headless")}, nil
	}
	path, err := exec.LookPath(external.Prefix + name)
	if err != nil {
		e := newError("launcher_not_found",
			fmt.Sprintf("There is no launcher called %s: it isn't built in, and %s%s isn't on the PATH.", name, external.Prefix, name),
			"Use tmux or headless, which are built in, or install "+external.Prefix+name+" on your PATH.")
		if name == "herdr" {
			e.Hint = "Install the herdr launcher from Aboard's repository: go build -o ~/.local/bin/aboard-launcher-herdr ./launchers/herdr (and herdr itself), or use tmux."
		}
		return nil, e
	}
	return external.Launcher{Name: name, Path: path}, nil
}

// launcherError turns a launcher's error into the CLI's.
func launcherError(name, what string, err error) error {
	var le *launcher.Error
	if !errors.As(err, &le) {
		return err
	}
	if le.Code == "launcher_broken" {
		return &Error{Code: le.Code, Message: le.Message, Hint: le.Hint, Details: map[string]any{"launcher": name, "stderr": le.Stderr}}
	}
	return &Error{
		Code: "launcher_failed", Message: fmt.Sprintf("The %s launcher couldn't %s: %s", name, what, le.Message), Hint: le.Hint,
		Details: map[string]any{"launcher": name, "code": le.Code, "stderr": le.Stderr},
	}
}

// swarmAgent is the SwarmAgent JSON shape.
type swarmAgent struct {
	Name     string  `json:"name"`
	Harness  string  `json:"harness"`
	Role     string  `json:"role"`
	Launcher string  `json:"launcher"`
	Mode     string  `json:"mode"`
	Dir      string  `json:"dir"`
	Handle   *string `json:"handle"`
	Attach   *string `json:"attach"`
	State    string  `json:"state"`
	Start    *string `json:"start"`
	// StartNote says why an agent that had a session started fresh, or is null.
	StartNote *string `json:"start_note"`
	Session   *string `json:"session"`
	Seated    bool    `json:"seated"`
	Presence  *string `json:"presence"`
	Delivery  *string `json:"delivery"`
}

// swarmUpAgent is one agent in swarm up's output.
type swarmUpAgent struct {
	swarmAgent
	Action      string `json:"action"`
	SeatCreated bool   `json:"seat_created"`
}

// Actions swarm up takes on an agent.
const (
	actionStarted        = "started"
	actionResumed        = "resumed"
	actionAlreadyRunning = "already_running"
)

// runSwarmUp starts the agents of a board file.
func runSwarmUp(ctx context.Context, a *app, args []string) error {
	flags := a.flags("swarm")
	file := flags.String("file", "", "the board file")
	launcherFlag := flags.String("launcher", "", "the launcher of agents that don't name their own")
	fresh := flags.Bool("fresh", false, "start new sessions instead of resuming")
	wait := flags.Duration("wait", defaultSwarmWait, "how long to wait for every agent to take its seat")
	if _, err := a.parse(flags, args, swarmUsage, 0, 0); err != nil {
		return err
	}
	if *wait < 0 {
		return usageError("--wait can't be negative.", swarmUsage)
	}
	command := "aboard swarm up"
	if *file != "" {
		command += " --file " + shellWord(*file)
	}
	if err := a.refuseInSession("Starting agents", command); err != nil {
		return err
	}
	f, err := a.readSwarmFile(*file)
	if err != nil {
		return err
	}
	srv, started, err := a.swarmServer(ctx)
	if err != nil {
		return err
	}
	c, err := a.humanClient(ctx, target{server: srv})
	if err != nil {
		return err
	}
	board, created, err := ensureSwarmBoard(ctx, c, f)
	if err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	name := swarmName(srv.URL, p.state, board.Name)
	rec, _, err := a.readSwarm(name)
	if err != nil {
		return err
	}
	rec.Server, rec.Board, rec.File = srv.URL, board.Name, f.path
	members, err := boardMembers(ctx, c, board.Name)
	if err != nil {
		return err
	}
	bindings := a.daemonBindings(ctx)
	creds, err := a.readCredentials()
	if err != nil {
		return err
	}
	tickets := launchtickets.Dir(p.launches())
	var out []swarmUpAgent
	var inputs []upInput
	for _, spec := range f.Agents {
		in := upInput{
			c: c, srv: srv, board: board.Name, swarm: name, file: f, spec: spec, rec: rec,
			members: members, bindings: bindings, creds: creds, tickets: tickets,
			launcherFlag: *launcherFlag, fresh: *fresh,
		}
		inputs = append(inputs, in)
		ag, err := a.upAgent(ctx, in)
		if saveErr := a.saveSwarm(name, rec); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return err
		}
		out = append(out, ag)
	}
	if *wait > 0 {
		// A resumed session that ends at once couldn't be resumed (its harness lost the
		// conversation): such an agent starts once more, fresh.
		retry := func(i int) error {
			in := inputs[i]
			in.fresh, in.note = true, "resuming the last session failed: it ended as it started, so a new one started"
			ag, err := a.upAgent(ctx, in)
			if saveErr := a.saveSwarm(name, rec); saveErr != nil && err == nil {
				err = saveErr
			}
			if err != nil {
				return err
			}
			ag.SeatCreated = out[i].SeatCreated
			out[i] = ag
			return nil
		}
		if err := a.waitSeated(ctx, c, board.Name, name, out, *wait, retry); err != nil {
			return err
		}
	} else {
		a.fillSeats(ctx, c, board.Name, func(i int) *swarmAgent { return &out[i].swarmAgent }, len(out))
	}
	notice := noticeFor(board.Policy)
	a.emit(struct {
		Server        serverRef      `json:"server"`
		ServerStarted bool           `json:"server_started"`
		File          string         `json:"file"`
		Board         api.Board      `json:"board"`
		BoardCreated  bool           `json:"board_created"`
		Swarm         string         `json:"swarm"`
		PolicyNotice  *policyNotice  `json:"policy_notice"`
		Agents        []swarmUpAgent `json:"agents"`
	}{srv, started, f.path, *board, created, name, notice, out}, a.swarmUpText(srv, started, f, board, created, name, notice, out))
	return nil
}

// swarmServer is the server a swarm's board is on: the one this directory's .aboard
// names, else the local server, which it starts if needed.
func (a *app) swarmServer(ctx context.Context) (serverRef, bool, error) {
	srv := a.localServer()
	if proj, ok, err := a.readProject(); err != nil {
		return serverRef{}, false, err
	} else if ok && proj.Server.URL != "" {
		srv = proj.Server
	}
	if srv.URL != a.localServer().URL {
		return srv, false, nil
	}
	started, err := a.ensureLocal(ctx)
	return srv, started, err
}

// ensureSwarmBoard returns the board file's board, creating it when it doesn't exist.
func ensureSwarmBoard(ctx context.Context, c *client, f swarmFile) (*api.Board, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	b, err := c.board(ctx, f.Board)
	if err == nil {
		return b, false, nil
	}
	var e *Error
	if !errors.As(err, &e) || e.Code != "board_not_found" {
		return nil, false, err
	}
	req := api.CreateBoardRequest{Name: &f.Board, Title: optional(strings.TrimSpace(f.Title)), Template: optional(f.Template), Charter: optional(f.Charter)}
	if preset, ok := f.Policy["preset"].(string); ok {
		pp := api.PolicyPreset(preset)
		req.Preset = &pp
	}
	r, err := c.api.CreateBoardWithResponse(ctx, &api.CreateBoardParams{}, req)
	if err != nil {
		return nil, false, c.unreachable(err)
	}
	if r.JSON201 == nil {
		return nil, false, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON201, true, nil
}

// boardMembers lists a board's members by name.
func boardMembers(ctx context.Context, c *client, board string) (map[string]api.Member, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListMembersWithResponse(ctx, board)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	out := map[string]api.Member{}
	for _, m := range r.JSON200.Members {
		out[m.Name] = m
	}
	return out, nil
}

// daemonBindings asks the delivery daemon which session holds each agent, starting the
// daemon if it isn't running. It returns none when the daemon can't be reached.
func (a *app) daemonBindings(ctx context.Context) map[delivery.AgentRef]delivery.BindingStatus {
	out, _ := a.readBindings(ctx)
	return out
}

// readBindings is daemonBindings, and whether the daemon answered.
func (a *app) readBindings(ctx context.Context) (map[delivery.AgentRef]delivery.BindingStatus, bool) {
	out := map[delivery.AgentRef]delivery.BindingStatus{}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpStatus})
	if err != nil || resp.Status == nil {
		return out, false
	}
	for _, b := range resp.Status.Bindings {
		out[b.Agent] = b
	}
	return out, true
}

// upInput is what swarm up knows when it starts one agent.
type upInput struct {
	c            *client
	srv          serverRef
	board, swarm string
	file         swarmFile
	spec         swarmSpec
	rec          *swarmRecord
	members      map[string]api.Member
	bindings     map[delivery.AgentRef]delivery.BindingStatus
	creds        credentials
	tickets      launchtickets.Dir
	launcherFlag string
	fresh        bool
	// note says why the agent starts fresh, when swarm up already knows.
	note string
}

// upAgent gives an agent its seat if it has none, and starts its session unless one runs.
func (a *app) upAgent(ctx context.Context, in upInput) (swarmUpAgent, error) {
	spec := in.spec
	h, _ := a.registry().Get(spec.Harness)
	prof := h.Profile()
	launcherName := spec.Launcher
	if launcherName == "" {
		launcherName = in.launcherFlag
	}
	if launcherName == "" {
		launcherName = in.file.Launcher
	}
	if launcherName == "" {
		launcherName = defaultLauncher
	}
	l, err := a.launcherFor(launcherName)
	if err != nil {
		return swarmUpAgent{}, err
	}
	info, err := l.Info(ctx)
	if err != nil {
		return swarmUpAgent{}, launcherError(launcherName, "say what it is", err)
	}
	mode := launcher.ModeInteractive
	if !info.Supports(mode) {
		mode = launcher.ModeHeadless
	}
	if mode == launcher.ModeHeadless && prof.Headless.Via != "native" {
		return swarmUpAgent{}, newError("headless_unsupported",
			fmt.Sprintf("%s can't run headless turns: its profile runs them through %s, which the headless launcher doesn't drive.", prof.Name, viaText(prof.Headless.Via)),
			"Start "+spec.Name+" with an interactive launcher, such as tmux or herdr.")
	}
	if _, err := exec.LookPath(prof.Command); err != nil {
		return swarmUpAgent{}, newError("harness_not_installed",
			fmt.Sprintf("%s isn't installed: %s isn't on the PATH, so %s can't start.", prof.Name, prof.Command, spec.Name),
			"Install "+prof.Name+", run aboard init, then aboard swarm up again.")
	}
	dir := in.file.dir
	if spec.Dir != "" {
		dir = spec.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(in.file.dir, dir)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // a working folder the person named
		return swarmUpAgent{}, fmt.Errorf("make the folder %s for %s: %w", dir, spec.Name, err)
	}

	// The seat: created once, with the person's login, and kept across runs.
	seatCreated := false
	if _, ok := in.creds.find(in.srv.URL, in.board, spec.Name); !ok {
		if m, exists := in.members[spec.Name]; exists && m.Status == api.Active {
			e := newError("agent_seat_elsewhere",
				fmt.Sprintf("Board %s already has an agent called %s, and this machine holds no credential for it.", in.board, spec.Name),
				"Give the agent another name in the board file, or start it on the machine that joined it.")
			e.Details = map[string]any{"agent": spec.Name, "board": in.board}
			return swarmUpAgent{}, e
		}
		role := spec.role()
		jctx, cancel := context.WithTimeout(ctx, requestTimeout)
		joined, err := in.c.join(jctx, api.JoinRequest{Board: &in.board, Role: &role, Name: &spec.Name, Harness: &spec.Harness})
		cancel()
		if err != nil {
			return swarmUpAgent{}, err
		}
		cred := agentCredential{Server: in.srv.URL, Board: in.board, Name: joined.Agent.Name, Token: joined.Token}
		if err := a.saveCredential(cred); err != nil {
			return swarmUpAgent{}, err
		}
		in.members[spec.Name] = joined.Agent
		seatCreated = true
	}

	ref := delivery.AgentRef{Server: in.srv.URL, Board: in.board, Name: spec.Name}
	ar := in.rec.Agents[spec.Name]
	if ar != nil && ar.Handle != "" {
		if prev, err := a.launcherFor(ar.Launcher); err == nil {
			st, err := prev.Status(ctx, launcher.Ref{Swarm: in.swarm, Agent: spec.Name, Handle: ar.Handle})
			if err == nil && st != launcher.Exited {
				return swarmUpAgent{swarmAgent: a.swarmRow(spec.Name, ar, string(st), in.bindings[ref], in.members), Action: actionAlreadyRunning, SeatCreated: seatCreated}, nil
			}
		}
		_ = in.tickets.Remove(ar.Ticket)
		// Its process is gone, so its session has ended, whether or not its end hook ran:
		// a resumed session must report in again before it counts as seated.
		if b, ok := in.bindings[ref]; ok && b.Open {
			a.endSession(ctx, b.Session)
			b.Open = false
			in.bindings[ref] = b
		}
	}

	req := launcher.StartRequest{
		Swarm: in.swarm, Agent: spec.Name, Harness: spec.Harness, Mode: mode, Dir: dir,
		Env: a.sessionEnv(spec.Name),
	}
	start, ticket, note := "fresh", "", in.note
	if mode == launcher.ModeHeadless {
		exe, err := a.env.Executable()
		if err != nil {
			return swarmUpAgent{}, fmt.Errorf("find the aboard binary: %w", err)
		}
		if !in.fresh && headlessSessionSaved(a, in.swarm, spec.Name) {
			start = "resumed"
		}
		req.Argv = runnerArgv(exe, in.swarm, spec, in.fresh)
	} else {
		session := ""
		if b, bound := in.bindings[ref]; bound && !in.fresh && len(prof.Interactive.Resume) > 0 {
			key, ok := delivery.ParseSessionKey(b.Session)
			switch {
			case !ok || key.Harness != spec.Harness:
			case !b.Turned:
				// A harness saves a conversation only from its first turn (Claude Code), so a
				// session that ran none can't be resumed, and has nothing to keep.
				note = "the last session never ran a turn, so there was nothing to resume"
			default:
				session = key.ID
			}
		}
		prompt := firstPrompt(spec, in.board, session != "")
		if session != "" {
			start = "resumed"
			req.Argv = harnessArgv(prof.Interactive.Resume, prof.Interactive.Model, spec, session, prompt)
		} else {
			if ticket, err = in.tickets.Write(a.env.Rand, ref); err != nil {
				return swarmUpAgent{}, err
			}
			req.Env[launchtickets.Env] = ticket
			req.Argv = harnessArgv(prof.Interactive.Start, prof.Interactive.Model, spec, "", prompt)
		}
	}
	got, err := l.Start(ctx, req)
	if err != nil {
		_ = in.tickets.Remove(ticket)
		return swarmUpAgent{}, launcherError(launcherName, "start "+spec.Name, err)
	}
	ar = &swarmAgentRecord{
		Harness: spec.Harness, Role: spec.role(), Launcher: launcherName, Mode: mode, Dir: dir,
		Handle: got.Handle, Attach: got.Attach, Start: start, StartNote: note, Ticket: ticket, StartedAt: time.Now().UTC(),
	}
	in.rec.Agents[spec.Name] = ar
	action := actionStarted
	if start == "resumed" {
		action = actionResumed
	}
	return swarmUpAgent{swarmAgent: a.swarmRow(spec.Name, ar, string(launcher.Running), delivery.BindingStatus{}, in.members), Action: action, SeatCreated: seatCreated}, nil
}

// endSession tells the delivery daemon a session has ended, for one whose process a
// launcher stopped or saw gone.
func (a *app) endSession(ctx context.Context, session string) {
	if key, ok := delivery.ParseSessionKey(session); ok {
		_, _ = a.callDaemon(ctx, delivery.Request{Op: delivery.OpEnd, Harness: key.Harness, Session: key.ID})
	}
}

func viaText(via string) string {
	switch via {
	case "acp":
		return "its Agent Client Protocol agent"
	case "", "none":
		return "nothing: it has no headless mode"
	}
	return via
}

// sessionEnv is what a launched session gets in its environment: its agent, and the
// Aboard home and harness config folders this command runs with, so the session uses
// the same ones.
func (a *app) sessionEnv(agent string) map[string]string {
	env := map[string]string{"ABOARD_AGENT": agent}
	names := []string{"ABOARD_HOME", "ABOARD_LOCAL_ADDR"}
	for _, h := range a.registry() {
		if v := h.Profile().ConfigDir.Env; v != "" {
			names = append(names, v)
		}
	}
	for _, n := range names {
		if v := a.env.Getenv(n); v != "" {
			env[n] = v
		}
	}
	return env
}

// firstPrompt is the session's first prompt: the board file's, else a line saying who it
// is. A resumed session is told it was resumed, so its first turn ends and the messages
// that waited for it arrive.
func firstPrompt(spec swarmSpec, board string, resumed bool) string {
	if resumed {
		// Never the first prompt again: the conversation already has it, and repeating an
		// instruction such as "do only this" would make the agent turn down what waited.
		// A short turn of its own lets every harness take what waited: added to this turn
		// as it starts, or handed when it ends.
		return resumePrompt(spec.Name, board)
	}
	if spec.Prompt != nil {
		return *spec.Prompt
	}
	return fmt.Sprintf("You are %s on the Aboard board %s, started by aboard swarm up. Run aboard status now, "+
		"then wait: messages from the board arrive in this session.", spec.Name, board)
}

// resumePrompt is the prompt a resumed session gets.
func resumePrompt(agent, board string) string {
	return fmt.Sprintf("Aboard: aboard swarm up restarted this session; you are still %s on the board %s. "+
		"Messages that waited for you come with this prompt or right after it: act on them as you would have.", agent, board)
}

// harnessArgv fills a profile's command line: the model option and the file's args go
// right after the program, {session} is the session, and an element that is exactly
// {prompt} is the prompt, left out when there is none.
func harnessArgv(base, model []string, spec swarmSpec, session, prompt string) []string {
	argv := []string{base[0]}
	if spec.Model != "" {
		for _, m := range model {
			argv = append(argv, strings.ReplaceAll(m, "{model}", spec.Model))
		}
	}
	argv = append(argv, spec.Args...)
	for _, arg := range base[1:] {
		if arg != "{prompt}" {
			argv = append(argv, strings.ReplaceAll(arg, "{session}", session))
		} else if prompt != "" {
			argv = append(argv, prompt)
		}
	}
	return argv
}

// swarmRow is an agent's row in swarm output, before its seat is checked.
func (a *app) swarmRow(name string, ar *swarmAgentRecord, state string, b delivery.BindingStatus, members map[string]api.Member) swarmAgent {
	row := swarmAgent{
		Name: name, Harness: ar.Harness, Role: ar.Role, Launcher: ar.Launcher, Mode: ar.Mode, Dir: ar.Dir,
		Handle: optional(ar.Handle), Attach: optional(ar.Attach), State: state, Start: optional(ar.Start),
		StartNote: optional(ar.StartNote),
	}
	if b.Session != "" {
		row.Session = optional(b.Session)
		row.Seated = b.Open && ar.Mode == launcher.ModeInteractive
	}
	if m, ok := members[name]; ok {
		if m.Presence != nil {
			row.Presence = optional(string(*m.Presence))
		}
		if m.Delivery != nil {
			row.Delivery = optional(string(*m.Delivery))
		}
		// A headless agent's runner reports its presence itself, and holds no session.
		if ar.Mode == launcher.ModeHeadless && present(row.Presence) && state == string(launcher.Running) {
			row.Seated = true
		}
	}
	return row
}

// present reports whether a presence says a session holds the agent.
func present(p *string) bool {
	if p == nil {
		return false
	}
	switch api.MemberPresence(*p) {
	case api.MemberPresenceIdle, api.MemberPresenceWorking, api.MemberPresenceWaiting:
		return true
	case api.MemberPresenceNoSession, api.MemberPresenceLessThannil:
		return false
	}
	return false
}

// fillSeats refreshes each row's session, seat, presence and delivery mode.
func (a *app) fillSeats(ctx context.Context, c *client, board string, row func(int) *swarmAgent, n int) bool {
	bindings, asked := a.readBindings(ctx)
	members, _ := boardMembers(ctx, c, board)
	srv := c.server
	all := true
	for i := range n {
		r := row(i)
		ar := &swarmAgentRecord{Harness: r.Harness, Role: r.Role, Launcher: r.Launcher, Mode: r.Mode, Dir: r.Dir, Handle: deref(r.Handle), Attach: deref(r.Attach), Start: deref(r.Start), StartNote: deref(r.StartNote)}
		*r = a.swarmRow(r.Name, ar, r.State, bindings[delivery.AgentRef{Server: srv.URL, Board: board, Name: r.Name}], members)
		// The board shows a presence only while a session holds the agent: the daemon
		// reports it from the moment a session takes the seat, before any turn. It
		// witnesses the seat when this command couldn't read the daemon's bindings.
		if !asked && present(r.Presence) && r.State == string(launcher.Running) {
			r.Seated = true
		}
		all = all && r.Seated
	}
	return all
}

// waitSeated waits until every agent has taken its seat, for at most d.
func (a *app) waitSeated(ctx context.Context, c *client, board, swarm string, out []swarmUpAgent, d time.Duration, retry func(int) error) error {
	deadline := time.Now().Add(d)
	row := func(i int) *swarmAgent { return &out[i].swarmAgent }
	for !a.fillSeats(ctx, c, board, row, len(out)) {
		if err := a.exitedUnseated(ctx, swarm, out, retry); err != nil {
			return err
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			var waiting, watch []string
			for _, ag := range out {
				if !ag.Seated {
					waiting = append(waiting, ag.Name)
					if ag.Attach != nil {
						watch = append(watch, ag.Name+": "+*ag.Attach)
					}
				}
			}
			hint := "A harness asking a question as it starts, such as whether to trust the folder, waits for an answer in its session. "
			if len(watch) > 0 {
				hint += "Watch it with " + strings.Join(watch, "; ") + ". "
			}
			hint += "aboard swarm ps shows where each agent is."
			e := newError("swarm_not_ready",
				fmt.Sprintf("%s didn't take %s seat within %s.", harness.AndList(waiting), pluralSeat(len(waiting)), d), hint)
			e.Details = map[string]any{"agents": waiting}
			return e
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil
}

// exitedUnseated fails when a session swarm up started has already ended without its
// agent taking the seat: waiting longer can't help, and its window, where the launcher
// keeps one, says why.
func (a *app) exitedUnseated(ctx context.Context, swarm string, out []swarmUpAgent, retry func(int) error) error {
	for i := range out {
		ag := &out[i]
		if ag.Seated || ag.Handle == nil {
			continue
		}
		l, err := a.launcherFor(ag.Launcher)
		if err != nil {
			continue
		}
		st, err := l.Status(ctx, launcher.Ref{Swarm: swarm, Agent: ag.Name, Handle: *ag.Handle})
		if err != nil || st != launcher.Exited {
			continue
		}
		ag.State = string(launcher.Exited)
		if ag.Action == actionResumed && retry != nil {
			if err := retry(i); err != nil {
				return err
			}
			continue
		}
		hint := "Start it again with aboard swarm up once the cause is fixed; aboard swarm ps shows the swarm."
		if ag.Attach != nil {
			hint = "See why with " + *ag.Attach + ", which shows its last output. " + hint
		}
		e := newError("swarm_not_ready", fmt.Sprintf("%s's %s session ended before it took its seat.", ag.Name, ag.Harness), hint)
		e.Details = map[string]any{"agents": []string{ag.Name}, "exited": []string{ag.Name}}
		return e
	}
	return nil
}

func pluralSeat(n int) string {
	if n == 1 {
		return "its"
	}
	return "their"
}

// swarmUpText is swarm up's text output.
func (a *app) swarmUpText(srv serverRef, started bool, f swarmFile, board *api.Board, created bool, name string, notice *policyNotice, out []swarmUpAgent) string {
	st := a.out()
	var b strings.Builder
	if started {
		b.WriteString("Started local Aboard at " + st.code(srv.URL) + "\n")
	} else {
		b.WriteString("Using Aboard at " + st.code(srv.URL) + "\n")
	}
	if created {
		fmt.Fprintf(&b, "Created board %s from %s\n", st.name(board.Name), f.path)
	}
	if notice != nil {
		b.WriteString(st.warn(notice.Message) + "\n")
	}
	fmt.Fprintf(&b, "Swarm %s on %s:\n", name, st.name(board.Name))
	b.WriteString(swarmTable(st, out, func(r swarmUpAgent) []string {
		state := strings.ReplaceAll(r.Action, "already_running", "running")
		return []string{r.Name, r.Harness, r.Launcher, state, deref(r.Attach)}
	}))
	for _, r := range out {
		if r.StartNote != nil && r.Action == actionStarted {
			fmt.Fprintf(&b, "%s started fresh: %s.\n", r.Name, *r.StartNote)
		}
	}
	seated := 0
	for _, r := range out {
		if r.Seated {
			seated++
		}
	}
	if seated == len(out) {
		fmt.Fprintf(&b, "All %d agents are seated. Watch and message them with: aboard open --board %s\n", len(out), board.Name)
	} else {
		fmt.Fprintf(&b, "%d of %d agents are seated; aboard swarm ps shows the rest.\n", seated, len(out))
	}
	return b.String()
}

// swarmTable lays rows out in columns, two spaces apart, indented by two.
func swarmTable[T any](_ styles, rows []T, cells func(T) []string) string {
	var table [][]string
	widths := []int{}
	for _, r := range rows {
		c := cells(r)
		for i, s := range c {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], len(s))
		}
		table = append(table, c)
	}
	var b strings.Builder
	for _, c := range table {
		b.WriteString(" ")
		for i, s := range c {
			b.WriteString(" ")
			if i < len(c)-1 {
				s += strings.Repeat(" ", widths[i]-len(s)+1)
			}
			b.WriteString(s)
		}
		b.WriteString("\n")
	}
	return strings.ReplaceAll(b.String(), " \n", "\n")
}

// swarmTarget finds a swarm for ps and down: --board's, else the board file's, else the
// only swarm on this machine.
func (a *app) swarmTarget(ctx context.Context, boardFlag, file string) (string, *swarmRecord, *swarmFile, error) {
	p, err := a.paths()
	if err != nil {
		return "", nil, nil, err
	}
	srv := a.localServer()
	if proj, ok, err := a.readProject(); err == nil && ok && proj.Server.URL != "" {
		srv = proj.Server
	}
	board := boardFlag
	var f *swarmFile
	if board == "" {
		if sf, err := a.readSwarmFile(file); err == nil {
			f, board = &sf, sf.Board
		} else if file != "" {
			return "", nil, nil, err
		}
	}
	if board == "" {
		entries, _ := os.ReadDir(p.swarms())
		var names []string
		for _, e := range entries {
			if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
				names = append(names, n)
			}
		}
		if len(names) != 1 {
			e := newError("swarm_not_found", "There's no board file here, and this machine has "+fmt.Sprint(len(names))+" swarms.",
				"Pass --board with the swarm's board, or --file with its board file.")
			return "", nil, nil, e
		}
		rec, _, err := a.readSwarm(names[0])
		return names[0], rec, nil, err
	}
	name := swarmName(srv.URL, p.state, board)
	rec, found, err := a.readSwarm(name)
	if err != nil {
		return "", nil, nil, err
	}
	if !found {
		rec.Server, rec.Board = srv.URL, board
		if f == nil {
			return "", nil, nil, newError("swarm_not_found", "This machine hasn't started a swarm on board "+board+".",
				"Start one with aboard swarm up, from the board file's folder.")
		}
	}
	_ = ctx
	return name, rec, f, nil
}

// runSwarmPs lists a swarm's agents.
func runSwarmPs(ctx context.Context, a *app, args []string) error {
	flags := a.flags("swarm")
	file := flags.String("file", "", "the board file")
	boardFlag := flags.String("board", "", "the swarm's board")
	if _, err := a.parse(flags, args, swarmUsage, 0, 0); err != nil {
		return err
	}
	if err := a.refuseInSession("Listing a swarm's sessions", "aboard swarm ps"+boardArg(*boardFlag)); err != nil {
		return err
	}
	name, rec, f, err := a.swarmTarget(ctx, *boardFlag, *file)
	if err != nil {
		return err
	}
	srv := a.serverRefFor(rec.Server)
	c, err := a.humanClient(ctx, target{server: srv})
	if err != nil {
		return err
	}
	rows := a.swarmRows(ctx, name, rec, f)
	a.fillSeats(ctx, c, rec.Board, func(i int) *swarmAgent { return &rows[i] }, len(rows))
	st := a.out()
	text := fmt.Sprintf("%s · %d agents · swarm %s\n", st.name(rec.Board), len(rows), name)
	text += swarmTable(st, rows, func(r swarmAgent) []string {
		seated := "-"
		if r.Seated {
			seated = "seated"
		}
		return []string{r.Name, r.Harness, r.Launcher, r.State, orDash(r.Start), seated, orDash(r.Presence), orDash(r.Delivery)}
	})
	a.emit(struct {
		Server serverRef    `json:"server"`
		Board  string       `json:"board"`
		Swarm  string       `json:"swarm"`
		Agents []swarmAgent `json:"agents"`
	}{srv, rec.Board, name, rows}, text)
	return nil
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// swarmRows lists a swarm's agents: the ones it started, then the board file's that it
// never started, with what their launchers say now.
func (a *app) swarmRows(ctx context.Context, name string, rec *swarmRecord, f *swarmFile) []swarmAgent {
	var names []string
	for n := range rec.Agents {
		names = append(names, n)
	}
	slices.Sort(names)
	var rows []swarmAgent
	for _, n := range names {
		ar := rec.Agents[n]
		state := "not_started"
		if ar.Handle != "" {
			state = string(launcher.Unknown)
			if l, err := a.launcherFor(ar.Launcher); err == nil {
				if st, err := l.Status(ctx, launcher.Ref{Swarm: name, Agent: n, Handle: ar.Handle}); err == nil {
					state = string(st)
				}
			}
		}
		rows = append(rows, a.swarmRow(n, ar, state, delivery.BindingStatus{}, nil))
	}
	if f != nil {
		for _, spec := range f.Agents {
			if _, ok := rec.Agents[spec.Name]; ok {
				continue
			}
			l := spec.Launcher
			if l == "" {
				l = f.Launcher
			}
			if l == "" {
				l = defaultLauncher
			}
			dir := f.dir
			if spec.Dir != "" {
				dir = spec.Dir
				if !filepath.IsAbs(dir) {
					dir = filepath.Join(f.dir, dir)
				}
			}
			mode := launcher.ModeInteractive
			if l == headless.Name {
				mode = launcher.ModeHeadless
			}
			rows = append(rows, a.swarmRow(spec.Name, &swarmAgentRecord{Harness: spec.Harness, Role: spec.role(), Launcher: l, Mode: mode, Dir: dir}, "not_started", delivery.BindingStatus{}, nil))
		}
	}
	return rows
}

// runSwarmDown stops a swarm's sessions, or the named agents'.
func runSwarmDown(ctx context.Context, a *app, args []string) error {
	flags := a.flags("swarm")
	file := flags.String("file", "", "the board file")
	boardFlag := flags.String("board", "", "the swarm's board")
	pos, err := a.parse(flags, args, swarmUsage, 0, -1)
	if err != nil {
		return err
	}
	command := "aboard swarm down" + boardArg(*boardFlag)
	for _, n := range pos {
		command += " " + shellWord(n)
	}
	if err := a.refuseInSession("Stopping agents", command); err != nil {
		return err
	}
	name, rec, _, err := a.swarmTarget(ctx, *boardFlag, *file)
	if err != nil {
		return err
	}
	names := pos
	if len(names) == 0 {
		for n := range rec.Agents {
			names = append(names, n)
		}
		slices.Sort(names)
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	tickets := launchtickets.Dir(p.launches())
	bindings := a.daemonBindings(ctx)
	stopped, notRunning := []string{}, []string{}
	for _, n := range names {
		ar, ok := rec.Agents[n]
		if !ok || ar.Handle == "" {
			notRunning = append(notRunning, n)
			continue
		}
		l, err := a.launcherFor(ar.Launcher)
		if err != nil {
			return err
		}
		ref := launcher.Ref{Swarm: name, Agent: n, Handle: ar.Handle}
		before, err := l.Status(ctx, ref)
		if err != nil {
			return launcherError(ar.Launcher, "check "+n, err)
		}
		if _, err := l.Stop(ctx, ref); err != nil {
			return launcherError(ar.Launcher, "stop "+n, err)
		}
		_ = tickets.Remove(ar.Ticket)
		if b, ok := bindings[delivery.AgentRef{Server: rec.Server, Board: rec.Board, Name: n}]; ok && b.Open {
			a.endSession(ctx, b.Session)
		}
		ar.Ticket = ""
		if before == launcher.Exited {
			notRunning = append(notRunning, n)
		} else {
			stopped = append(stopped, n)
		}
	}
	if err := a.saveSwarm(name, rec); err != nil {
		return err
	}
	text := "Nothing was running on " + rec.Board + ".\n"
	if len(stopped) > 0 {
		text = fmt.Sprintf("Stopped %s on %s. The board, the seats and the record stay; aboard swarm up starts them again.\n",
			harness.AndList(stopped), rec.Board)
	}
	a.emit(struct {
		Server     serverRef `json:"server"`
		Board      string    `json:"board"`
		Swarm      string    `json:"swarm"`
		Stopped    []string  `json:"stopped"`
		NotRunning []string  `json:"not_running"`
	}{a.serverRefFor(rec.Server), rec.Board, name, stopped, notRunning}, text)
	return nil
}
