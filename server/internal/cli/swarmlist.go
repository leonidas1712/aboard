package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/launcher"
)

// recordedSwarm is a swarm this machine started, by its name, with its record.
type recordedSwarm struct {
	id  string
	rec *swarmRecord
}

// recordedSwarms lists every swarm whose record is in the state folder, by name.
func (a *app) recordedSwarms() ([]recordedSwarm, error) {
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	entries, _ := os.ReadDir(p.swarms())
	var out []recordedSwarm
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() {
			continue
		}
		rec, found, err := a.readSwarm(id)
		if err != nil || !found {
			continue
		}
		out = append(out, recordedSwarm{id: id, rec: rec})
	}
	return out, nil
}

// findSwarm finds a swarm this machine started by its name (aboard-trio-3f9a0c) or by its
// board's name (trio).
func (a *app) findSwarm(name string) (string, *swarmRecord, error) {
	if !strings.ContainsAny(name, `/\`) && name != "." && name != ".." {
		rec, found, err := a.readSwarm(name)
		if err != nil {
			return "", nil, err
		}
		if found {
			return name, rec, nil
		}
	}
	all, err := a.recordedSwarms()
	if err != nil {
		return "", nil, err
	}
	var onBoard []recordedSwarm
	for _, s := range all {
		if s.rec.Board == name {
			onBoard = append(onBoard, s)
		}
	}
	switch len(onBoard) {
	case 1:
		return onBoard[0].id, onBoard[0].rec, nil
	case 0:
		return "", nil, newError("swarm_not_found",
			fmt.Sprintf("This machine hasn't started a swarm called %s, or one on a board called %s.", name, name),
			"aboard swarm list shows the swarms this machine started.")
	}
	e := newError("swarm_not_selected",
		fmt.Sprintf("This machine has %d swarms on a board called %s: %s.", len(onBoard), name, swarmIDs(onBoard)),
		"Pass the swarm's own name to --swarm, such as --swarm "+onBoard[0].id+".")
	e.Details = swarmChoices(onBoard)
	return "", nil, e
}

// chooseSwarm is the error for a command that found no board file here and can't tell
// which of this machine's swarms to use.
func chooseSwarm(all []recordedSwarm) error {
	if len(all) == 0 {
		return newError("swarm_not_found", "There's no board file here, and this machine hasn't started a swarm.",
			"Start one with aboard swarm up in the folder of a board file, or pass --file.")
	}
	e := newError("swarm_not_selected",
		fmt.Sprintf("There's no board file here, and this machine has %d swarms: %s.", len(all), swarmIDs(all)),
		"Name one with --swarm and the swarm's name or its board's, such as --swarm "+all[0].rec.Board+"; aboard swarm list shows them.")
	e.Details = swarmChoices(all)
	return e
}

func swarmIDs(ss []recordedSwarm) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.id+" ("+s.rec.Board+")")
	}
	return strings.Join(out, ", ")
}

func swarmChoices(ss []recordedSwarm) map[string]any {
	var ids, boards []string
	for _, s := range ss {
		ids, boards = append(ids, s.id), append(boards, s.rec.Board)
	}
	return map[string]any{"choices": ids, "boards": boards}
}

// recordedFile reads a recorded swarm's board file, or returns nil when it is gone or no
// longer names the swarm's board.
func (a *app) recordedFile(rec *swarmRecord) *swarmFile {
	if rec.File == "" {
		return nil
	}
	if sf, err := a.readSwarmFile(rec.File); err == nil && sf.Board == rec.Board {
		return &sf
	}
	return nil
}

// recordedSwarmFile is swarm up --swarm's board file, and the server the swarm is on.
func (a *app) recordedSwarmFile(ctx context.Context, name, file string) (swarmFile, serverRef, bool, error) {
	id, rec, err := a.findSwarm(name)
	if err != nil {
		return swarmFile{}, serverRef{}, false, err
	}
	path := file
	if path == "" {
		path = rec.File
		if !fileExists(path) {
			e := newError("board_file_not_found", swarmFileGone(id, path), "")
			e.Hint = "Pass where it is now: aboard swarm up --swarm " + id + " --file <path>."
			e.Details = map[string]any{"swarm": id, "file": path}
			return swarmFile{}, serverRef{}, false, e
		}
	}
	f, err := a.readSwarmFile(path)
	if err != nil {
		return swarmFile{}, serverRef{}, false, err
	}
	if f.Board != rec.Board {
		return swarmFile{}, serverRef{}, false, boardMismatch(f, id, rec)
	}
	srv := a.serverRefFor(rec.Server)
	if srv.URL != a.localServer().URL {
		return f, srv, false, nil
	}
	started, err := a.ensureLocal(ctx)
	return f, srv, started, err
}

// boardMismatch is the error for a board file that isn't the named swarm's.
func boardMismatch(f swarmFile, id string, rec *swarmRecord) error {
	e := newError("board_file_invalid",
		fmt.Sprintf("%s is the board file of %s, but the swarm %s is on %s.", f.path, f.Board, id, rec.Board),
		"Pass the swarm's own board file with --file, or leave out --swarm to start the file's board.")
	e.Details = map[string]any{"file": f.path, "errors": []string{"/board: " + f.Board + " isn't " + rec.Board}}
	return e
}

// noBoardFileHere adds to swarm up's missing-file error that a swarm this machine started
// can be started from any folder.
func (a *app) noBoardFileHere(err error) error {
	var e *Error
	if !errors.As(err, &e) || e.Code != "board_file_not_found" {
		return err
	}
	if all, _ := a.recordedSwarms(); len(all) > 0 {
		e.Hint += " To start a swarm this machine started before, from any folder: aboard swarm up --swarm " + all[0].rec.Board + " (aboard swarm list shows them)."
	}
	return e
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// swarmFileGone says a recorded swarm's board file isn't where swarm up last read it.
func swarmFileGone(id, path string) string {
	return fmt.Sprintf("The board file of the swarm %s, %s, isn't there any more: it moved or was deleted.", id, path)
}

func swarmArg(name string) string {
	if name == "" {
		return ""
	}
	return " --swarm " + shellWord(name)
}

// swarmSummary is one swarm in swarm list and swarm show (the SwarmSummary JSON shape).
type swarmSummary struct {
	Swarm         string     `json:"swarm"`
	Server        serverRef  `json:"server"`
	Board         string     `json:"board"`
	Title         *string    `json:"title"`
	File          string     `json:"file"`
	Folder        string     `json:"folder"`
	FileExists    bool       `json:"file_exists"`
	Launchers     []string   `json:"launchers"`
	AgentsRunning int        `json:"agents_running"`
	AgentsTotal   int        `json:"agents_total"`
	LastUp        *time.Time `json:"last_up"`
}

// summarize sums up a swarm from its agents' rows.
func (a *app) summarize(id string, rec *swarmRecord, rows []swarmAgent) swarmSummary {
	s := swarmSummary{
		Swarm: id, Server: a.serverRefFor(rec.Server), Board: rec.Board, Title: optional(rec.Title),
		File: rec.File, Folder: filepath.Dir(rec.File), FileExists: rec.File != "" && fileExists(rec.File),
		Launchers: []string{}, AgentsTotal: len(rows),
	}
	for _, r := range rows {
		if r.State == string(launcher.Running) {
			s.AgentsRunning++
		}
		if !slices.Contains(s.Launchers, r.Launcher) {
			s.Launchers = append(s.Launchers, r.Launcher)
		}
	}
	slices.Sort(s.Launchers)
	// A record written before swarm up kept the time it ran has each agent's start.
	last := rec.UpAt
	if last.IsZero() {
		for _, ar := range rec.Agents {
			if ar.StartedAt.After(last) {
				last = ar.StartedAt
			}
		}
	}
	if !last.IsZero() {
		s.LastUp = &last
	}
	return s
}

// upText says when swarm up last ran, after prefix, or "-" when this machine doesn't know.
func (s swarmSummary) upText(prefix string, now time.Time) string {
	if s.LastUp == nil {
		return "-"
	}
	return prefix + agoText(*s.LastUp, now)
}

// runSwarmList lists every swarm this machine started.
func runSwarmList(ctx context.Context, a *app, args []string) error {
	flags := a.flags("swarm")
	if _, err := a.parse(flags, args, swarmUsage, 0, 0); err != nil {
		return err
	}
	if err := a.refuseInSession("Listing this machine's swarms", "aboard swarm list"); err != nil {
		return err
	}
	all, err := a.recordedSwarms()
	if err != nil {
		return err
	}
	out := []swarmSummary{}
	for _, s := range all {
		out = append(out, a.summarize(s.id, s.rec, a.swarmRows(ctx, s.id, s.rec, a.recordedFile(s.rec))))
	}
	slices.SortStableFunc(out, func(x, y swarmSummary) int {
		switch {
		case x.LastUp == nil && y.LastUp == nil:
			return strings.Compare(x.Swarm, y.Swarm)
		case x.LastUp == nil:
			return 1
		case y.LastUp == nil:
			return -1
		}
		return y.LastUp.Compare(*x.LastUp)
	})
	st := a.out()
	now := time.Now()
	var b strings.Builder
	switch len(out) {
	case 0:
		b.WriteString("This machine hasn't started a swarm. Start one with aboard swarm up in the folder of a board file.\n")
	default:
		fmt.Fprintf(&b, "%d %s on this machine:\n", len(out), plural(len(out), "swarm", "swarms"))
		b.WriteString(swarmTable(st, out, func(s swarmSummary) []string {
			where := s.Folder
			if filepath.Base(s.File) != defaultBoardFile {
				where = s.File
			}
			if !s.FileExists {
				where += " (" + filepath.Base(s.File) + " is gone)"
			}
			return []string{
				s.Swarm, s.Board, orDash(s.Title), fmt.Sprintf("%d of %d running", s.AgentsRunning, s.AgentsTotal),
				strings.Join(s.Launchers, ","), s.upText("up ", now), where,
			}
		}))
		b.WriteString("From any folder: aboard swarm show <swarm>, or --swarm <swarm> on swarm up, ps or down.\n")
	}
	a.emit(struct {
		Swarms []swarmSummary `json:"swarms"`
	}{out}, b.String())
	return nil
}

// swarmShowAgent is one agent in swarm show's output.
type swarmShowAgent struct {
	swarmAgent
	Commands agentCommands `json:"commands"`
}

// agentCommands are what a person runs for one agent of a swarm.
type agentCommands struct {
	// Watch is the launcher's line to watch the session, or nil before it ever started.
	Watch *string `json:"watch"`
	Stop  string  `json:"stop"`
	Start string  `json:"start"`
}

// swarmCommands are what a person runs for a whole swarm.
type swarmCommands struct {
	Open string `json:"open"`
	Ps   string `json:"ps"`
	Up   string `json:"up"`
	Down string `json:"down"`
}

// runSwarmShow shows one swarm: where its file is, each agent, and the commands to
// watch, stop and start them.
func runSwarmShow(ctx context.Context, a *app, args []string) error {
	flags := a.flags("swarm")
	file := flags.String("file", "", "the board file")
	swarmFlag := flags.String("swarm", "", "a swarm this machine started, by its name or its board's")
	pos, err := a.parse(flags, args, swarmUsage, 0, 1)
	if err != nil {
		return err
	}
	if len(pos) == 1 {
		if *swarmFlag != "" && *swarmFlag != pos[0] {
			return usageError("Name the swarm once: as the argument or with --swarm.", swarmUsage)
		}
		*swarmFlag = pos[0]
	}
	command := "aboard swarm show"
	if *swarmFlag != "" {
		command += " " + shellWord(*swarmFlag)
	}
	if err := a.refuseInSession("Showing a swarm", command); err != nil {
		return err
	}
	id, rec, f, err := a.swarmTarget("", *swarmFlag, *file)
	if err != nil {
		return err
	}
	srv := a.serverRefFor(rec.Server)
	c, err := a.humanClient(ctx, target{server: srv})
	if err != nil {
		return err
	}
	rows := a.swarmRows(ctx, id, rec, f)
	a.fillSeats(ctx, c, rec.Board, func(i int) *swarmAgent { return &rows[i] }, len(rows))
	a.checkSeats(ctx, srv, rec.Board, rows)
	sum := a.summarize(id, rec, rows)
	up := "aboard swarm up --swarm " + id
	cmds := swarmCommands{
		Open: "aboard open --board " + rec.Board, Ps: "aboard swarm ps --swarm " + id,
		Up: up, Down: "aboard swarm down --swarm " + id,
	}
	agents := make([]swarmShowAgent, 0, len(rows))
	for _, r := range rows {
		agents = append(agents, swarmShowAgent{swarmAgent: r, Commands: agentCommands{
			Watch: r.Attach, Stop: "aboard swarm down " + shellWord(r.Name) + " --swarm " + id, Start: up,
		}})
	}
	a.emit(struct {
		swarmSummary
		Agents   []swarmShowAgent `json:"agents"`
		Commands swarmCommands    `json:"commands"`
	}{sum, agents, cmds}, a.swarmShowText(sum, agents, cmds))
	return nil
}

// swarmShowText is swarm show's text output.
func (a *app) swarmShowText(s swarmSummary, agents []swarmShowAgent, cmds swarmCommands) string {
	st := a.out()
	var b strings.Builder
	head := []string{st.name(s.Board)}
	if s.Title != nil {
		head = append(head, *s.Title)
	}
	head = append(head, "swarm "+s.Swarm)
	b.WriteString(strings.Join(head, " · ") + "\n")
	field := func(label, value string) {
		fmt.Fprintf(&b, "  %s %s\n", st.dim(fmt.Sprintf("%-9s", label)), value)
	}
	file := s.File
	if !s.FileExists {
		file = st.warn(file + " (moved or deleted; swarm up --swarm needs --file with where it is now)")
	}
	field("file", file)
	field("server", s.Server.URL)
	field("launcher", strings.Join(s.Launchers, ", "))
	field("last up", s.upText("", time.Now()))
	field("running", fmt.Sprintf("%d of %d agents", s.AgentsRunning, s.AgentsTotal))
	for _, ag := range agents {
		b.WriteString("\n")
		seated := "not seated"
		if ag.Seated {
			seated = "seated"
		}
		line := []string{st.name(ag.Name), ag.Harness, ag.Launcher, ag.State, seated}
		if ag.Presence != nil && *ag.Presence != "" {
			line = append(line, presenceText(*ag.Presence))
		}
		if ag.Start != nil {
			line = append(line, *ag.Start)
		}
		b.WriteString(strings.Join(line, " · ") + "\n")
		row := func(label, value string) {
			fmt.Fprintf(&b, "  %s %s\n", st.dim(fmt.Sprintf("%-8s", label)), value)
		}
		if ag.StartNote != nil {
			row("note", "started fresh: "+*ag.StartNote)
		}
		if ag.Session != nil {
			row("session", *ag.Session)
		}
		if deref(ag.SeatCredential) == seatEnded {
			row("seat", st.warn("ended: the access key it came from was revoked or has expired"))
		}
		if ag.Commands.Watch != nil {
			row("watch", st.code(*ag.Commands.Watch))
		}
		if ag.State == string(launcher.Running) || ag.State == string(launcher.Unknown) {
			row("stop", st.code(ag.Commands.Stop))
		} else {
			row("start", st.code(ag.Commands.Start))
		}
	}
	b.WriteString("\n")
	rows := make([]swarmAgent, 0, len(agents))
	for _, ag := range agents {
		rows = append(rows, ag.swarmAgent)
	}
	b.WriteString(endedSeatsText(st, s.Board, rows))
	fmt.Fprintf(&b, "Watch and message them: %s\n", st.code(cmds.Open))
	fmt.Fprintf(&b, "Stop them all: %s\n", st.code(cmds.Down))
	return b.String()
}
