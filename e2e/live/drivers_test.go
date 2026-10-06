//go:build live

package live

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// driver is how the live kit runs one harness in a tmux pane: what its profile can't
// say, such as how its screen shows a prompt and which start-up questions it asks. The
// scenarios in scenarios_test.go are written once against drivers; a new harness adds
// its driver to drivers and passes them with make live HARNESS=<name>.
type driver struct {
	p support.Profile
	// require skips the test when the harness can't run here, or fails it when it can't
	// run without touching the person's own config.
	require func(t *testing.T)
	// setUp prepares the lab before any harness starts in it. Codex's: the daemon must
	// start outside Codex's sandbox, with the test's CODEX_HOME.
	setUp func(l *lab)
	// start starts the harness in dir, set up as a project with l.project, and waits
	// for its prompt.
	start func(l *lab, name, dir string) *pane
	// startPlain starts the harness in a folder with no Aboard setup.
	startPlain func(l *lab, name, dir string) *pane
	// argv is how the suite runs the harness, followed by args.
	argv func(l *lab, args ...string) []string
	// env is the environment the harness runs with in a lab.
	env func(l *lab) []string
	// ready waits until the pane shows the harness's prompt, answering the questions it
	// asks on the way.
	ready func(p *pane)
	// idle reports whether the pane shows the prompt with no turn running.
	idle func(p *pane) bool
	// taken reports whether the prompt box no longer holds text typed into it.
	taken func(p *pane, prefix string) bool
	// afterBind waits for what the harness does once its first turn ends.
	afterBind func(p *pane)
	// startsAtFirstTurn is true when the harness runs its session-start hook only once
	// the first prompt is sent, not when it opens.
	startsAtFirstTurn bool
	// stopBackground stops what the harness leaves running after its terminal quits,
	// for a harness whose sessions outlive the terminal.
	stopBackground func(l *lab)
	// approve answers a question the harness asks before running a command, and
	// reports whether it answered one.
	approve func(p *pane) bool
	// subagentPrompt asks the session to have one subagent run %s, the commands, and
	// then run aboard say "SUBAGENT-DONE" itself.
	subagentPrompt string
	// subagentSetup records what the harness sends inside a subagent, before it starts,
	// and returns what to log afterwards; it may be nil.
	subagentSetup func(l *lab, project string) func()
	// transcripts lists the files where the harness keeps its sessions' transcripts.
	transcripts func(l *lab) []string
	// model is the model the suite runs the harness with, on every start and resume.
	model func() string
}

// The models the suite runs each harness with unless a variable names another. The suite
// proves Aboard's wiring to a harness, not what a model can do, so each default is a
// cheap model of the provider the suite logs the harness in to.
const (
	defaultClaudeModel = "claude-sonnet-5-5"
	defaultCodexModel  = "gpt-6.1-sol"
	defaultOmpModel    = "anthropic/claude-sonnet-5-5"
)

// liveModel is the model variable names, else def.
func liveModel(variable, def string) string {
	if m := os.Getenv(variable); m != "" {
		return m
	}
	return def
}

// claudeModel is the model the suite runs Claude Code with: LIVE_CLAUDE_MODEL, else Haiku.
func claudeModel() string { return liveModel("LIVE_CLAUDE_MODEL", defaultClaudeModel) }

// codexModel is the model the suite runs Codex with: LIVE_CODEX_MODEL, else a cheap GPT.
func codexModel() string { return liveModel("LIVE_CODEX_MODEL", defaultCodexModel) }

// ompModel is the model the suite runs omp with: LIVE_OMP_MODEL, else Haiku through the
// Anthropic login, so omp never picks a local or another provider's model that happens
// to be set up.
func ompModel() string { return liveModel("LIVE_OMP_MODEL", defaultOmpModel) }

// resumeArgv is how the suite resumes the session id in the harness: the profile's
// interactive.resume, with the suite's own options.
func (d *driver) resumeArgv(l *lab, id string) []string {
	if len(d.p.Interactive.Resume) == 0 {
		l.t.Fatalf("%s's profile has no interactive.resume", d.p.Harness)
	}
	var args []string
	for _, a := range d.p.Interactive.Resume[1:] {
		if a == "{prompt}" {
			continue // the suite resumes with no first prompt
		}
		args = append(args, strings.ReplaceAll(a, "{session}", id))
	}
	return d.argv(l, args...)
}

// waitsForIdle is true when the harness's bundles go only to a hook that waits while
// it is idle, so a bundle handed is confirmed by the session's next event.
func (d *driver) waitsForIdle() bool { return d.p.WaitsForIdle() }

var (
	driversOnce sync.Once
	allDrivers  []*driver
	driversErr  error
)

// drivers returns the driver of every harness with a profile, in the profiles' order.
func drivers(t *testing.T) []*driver {
	t.Helper()
	driversOnce.Do(func() {
		profiles, err := support.Profiles()
		if err != nil {
			driversErr = err
			return
		}
		for _, p := range profiles {
			if d := newDriver(p); d != nil {
				allDrivers = append(allDrivers, d)
			}
		}
	})
	if driversErr != nil {
		t.Fatal(driversErr)
	}
	return allDrivers
}

// newDriver returns the driver for a profile, or nil for a harness without one.
func newDriver(p support.Profile) *driver {
	switch p.Harness {
	case "claude-code":
		return claudeDriver(p)
	case "codex":
		return codexDriver(p)
	case "omp":
		return ompDriver(p)
	}
	return nil
}

// selected reports whether make live runs a harness: every one, or those HARNESS names.
func selected(harness string) bool {
	only := os.Getenv("HARNESS")
	return only == "" || slices.Contains(strings.Split(only, ","), harness)
}

// only skips a test written for one harness when HARNESS leaves that harness out.
func only(t *testing.T, harness string) {
	t.Helper()
	if !selected(harness) {
		t.Skipf("HARNESS=%s", os.Getenv("HARNESS"))
	}
}

// eachHarness runs a scenario as a subtest for each harness make live runs. The
// scenario runs in parallel with every other test, not only with its own subtests: a
// top-level test that isn't parallel holds up the whole run until its subtests end.
func eachHarness(t *testing.T, scenario string, f func(t *testing.T, d *driver, rec *recorder)) {
	t.Parallel()
	for _, d := range drivers(t) {
		if !selected(d.p.Harness) {
			continue
		}
		t.Run(d.p.Harness, func(t *testing.T) {
			d.require(t)
			parallel(t)
			f(t, d, record(t, d, scenario))
		})
	}
}

func claudeDriver(p support.Profile) *driver {
	return &driver{
		p:       p,
		require: requireClaude,
		setUp:   func(*lab) {},
		start:   func(l *lab, name, dir string) *pane { return l.startClaude(name, dir) },
		startPlain: func(l *lab, name, dir string) *pane {
			return l.startClaude(name, dir)
		},
		argv:  func(_ *lab, args ...string) []string { return claudeArgv(args...) },
		env:   func(l *lab) []string { return slices.Clone(l.vars) },
		ready: func(p *pane) { p.waitClaudeReady() },
		idle:  func(p *pane) bool { s := p.screen(); return claudeReady(s) && !claudeBusy(s) },
		taken: func(p *pane, prefix string) bool {
			in, ok := inputLine(p.screen())
			return !ok || !strings.Contains(in, prefix)
		},
		afterBind: func(*pane) {},
		approve: func(p *pane) bool {
			if strings.Contains(p.screen(), "Do you want to proceed?") {
				p.keys("Enter") // the default answer, Yes
				return true
			}
			return false
		},
		subagentPrompt: "Use your Agent tool to start one general-purpose subagent with this task: \"Run %s, and report the exact " +
			"output of each.\" Don't run those commands yourself. When the subagent has reported, run: aboard say \"SUBAGENT-DONE\".",
		subagentSetup: claudeSubagentSetup,
		transcripts:   func(l *lab) []string { return l.claudeTranscripts() },
		model:         claudeModel,
	}
}

func codexDriver(p support.Profile) *driver {
	return &driver{
		p:       p,
		require: func(t *testing.T) { t.Helper(); requireCodex(t) },
		setUp:   func(l *lab) { l.codexHome(requireCodex(l.t)) },
		start:   func(l *lab, name, dir string) *pane { return l.startCodex(name, dir) },
		startPlain: func(l *lab, name, dir string) *pane {
			home := l.codexHome(requireCodex(l.t))
			appendFile(l.t, filepath.Join(home, "config.toml"), fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir))
			p := l.start(name, dir, slices.Clone(l.vars), l.codexArgv())
			p.waitCodexReady()
			return p
		},
		argv:  func(l *lab, args ...string) []string { return l.codexArgv(args...) },
		env:   func(l *lab) []string { return slices.Clone(l.vars) },
		ready: func(p *pane) { p.waitCodexReady() },
		idle:  codexIdle,
		taken: func(p *pane, prefix string) bool { return !strings.Contains(codexInput(p.screen()), prefix) },
		afterBind: func(p *pane) {
			// Without its hooks, Codex still gets ordinary messages through its queue, but
			// the daemon never sees its turns; the suite proves the hooks as Codex runs them.
			// Codex 0.160 can show no "Working" line for a moment between steps, so the
			// screen alone can look idle mid-turn; the stop hook is what ends the turn.
			p.l.waitFor(3*time.Minute, p.name+": Codex to run its session start, prompt and stop hooks", func() bool {
				return p.l.codexHooksRan("session-start", "prompt", "stop")
			})
			p.waitIdle(time.Minute)
		},
		startsAtFirstTurn: true,
		stopBackground:    func(l *lab) { l.stopProcesses(filepath.Join(l.dir, "codex-home")) },
		approve:           func(*pane) bool { return false },
		subagentPrompt: "Spawn one sub-agent with this task: \"Run %s, and report the exact output of each.\" Don't run those " +
			"commands yourself. When the sub-agent has reported, run: aboard say \"SUBAGENT-DONE\".",
		transcripts: func(l *lab) []string {
			var out []string
			_ = filepath.WalkDir(filepath.Join(l.dir, "codex-home", "sessions"), func(path string, e os.DirEntry, err error) error {
				if err == nil && !e.IsDir() && strings.HasSuffix(path, ".jsonl") {
					out = append(out, path)
				}
				return nil
			})
			return out
		},
		model: codexModel,
	}
}

// ompDriver runs omp with a scratch home folder of the lab's own, logged in from
// CLAUDE_CODE_OAUTH_TOKEN (omp_test.go). Aboard's extension in the project connects as
// omp opens, so the session starts at open, and omp asks nothing before running a command.
func ompDriver(p support.Profile) *driver {
	return &driver{
		p:       p,
		require: func(t *testing.T) { t.Helper(); requireOmp(t, p) },
		setUp:   func(l *lab) { l.ompHome() },
		start:   func(l *lab, name, dir string) *pane { return l.startOmp(name, dir) },
		// Without the project's setup there is no extension: omp's agent folder in the
		// scratch home has none.
		startPlain: func(l *lab, name, dir string) *pane { return l.startOmp(name, dir) },
		argv:       func(_ *lab, args ...string) []string { return ompArgv(args...) },
		env:        func(l *lab) []string { return l.ompEnv() },
		ready:      func(p *pane) { p.waitOmpReady() },
		idle:       ompIdle,
		taken:      func(p *pane, prefix string) bool { return !strings.Contains(ompInput(p.screen()), prefix) },
		afterBind:  func(*pane) {},
		approve:    func(*pane) bool { return false },
		subagentPrompt: "Use your task tool to start one subagent with this task: \"Run %s, and report the exact output of each.\" " +
			"Don't run those commands yourself. When the subagent has reported, run: aboard say \"SUBAGENT-DONE\".",
		transcripts: func(l *lab) []string { return l.ompTranscripts() },
		model:       ompModel,
	}
}

// codexIdle reports whether Codex shows its prompt with no turn running, answering a
// hooks question if Codex asks one mid-session.
func codexIdle(p *pane) bool {
	s := p.screen()
	running := strings.Contains(s, "Working") || strings.Contains(s, "esc to interrupt")
	turnOpen := p.codexTurnOpen()
	// An announcement can open while a turn runs, even before Codex shows the turn
	// running, and Esc then would interrupt the turn too. So it waits until the pane's
	// session has no turn open.
	if !running && !turnOpen && p.dismissCodexAnnouncement(s) {
		return false
	}
	if strings.Contains(s, "Hooks need review") && strings.Contains(s, "Trust all and continue") {
		// Trusting records the hooks in the test's own CODEX_HOME, never the person's.
		p.keys("2")
		p.keys("Enter")
		return false
	}
	// Older Codex shows how much context is left under its prompt; newer shows "? for
	// shortcuts" there instead.
	return (strings.Contains(s, "context left") || strings.Contains(s, "? for shortcuts")) && !running && !turnOpen
}

// recorder records one scenario's outcome for one harness in the live kit's results,
// which make harness-table turns into the README's support matrix.
type recorder struct {
	t        *testing.T
	d        *driver
	scenario string
	na       bool
	// expected are scenarios the test proves on its way, each true once proved.
	expected map[string]bool
}

// liveResults are the outcomes of this run, by harness and scenario, saved when the
// run ends.
var liveResults = struct {
	sync.Mutex
	r        *support.Results
	versions map[string]string
	// handovers are the deliveries measured, by harness (handover_test.go).
	handovers map[string][]handSample
}{
	r:        &support.Results{Harnesses: map[string]*support.HarnessResults{}},
	versions: map[string]string{}, handovers: map[string][]handSample{},
}

// record starts recording a scenario for a harness: pass or fail when the test ends, or
// n/a when the scenario doesn't apply. A test skipped because the harness can't run
// here records nothing.
func record(t *testing.T, d *driver, scenario string) *recorder {
	rec := &recorder{t: t, d: d, scenario: scenario, expected: map[string]bool{}}
	t.Cleanup(func() {
		result := "pass"
		switch {
		case rec.na:
			result = "n/a"
		case t.Failed():
			result = "fail"
		case t.Skipped():
			return
		}
		rec.save(scenario, result)
		for s, proved := range rec.expected {
			if !proved && t.Failed() {
				rec.save(s, "fail")
			}
		}
	})
	return rec
}

// expect names a scenario the test proves on its way, such as a session that joins and
// talks before the rest of the test: it fails with the test unless milestone recorded it.
func (rec *recorder) expect(scenario string) { rec.expected[scenario] = false }

// milestone records that the test proved an expected scenario.
func (rec *recorder) milestone(scenario string) {
	rec.expected[scenario] = true
	rec.save(scenario, "pass")
}

func (rec *recorder) save(scenario, result string) {
	version := harnessVersion(rec.d)
	liveResults.Lock()
	defer liveResults.Unlock()
	liveResults.r.Record(rec.d.p.Harness, scenario, support.Result{
		Result: result, Date: time.Now().Format(time.DateOnly), Version: version, Test: rec.t.Name(),
	})
	if version != "" {
		liveResults.versions[rec.d.p.Harness] = version
	}
}

// notApplicable records that the scenario doesn't apply to the harness, and why, and
// skips the test.
func (rec *recorder) notApplicable(reason string) {
	rec.t.Helper()
	rec.na = true
	rec.t.Skip(reason)
}

var versionCache sync.Map

// harnessVersion is what the harness's command prints for --version, first line.
func harnessVersion(d *driver) string {
	if v, ok := versionCache.Load(d.p.Harness); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := command(ctx, d.p.Command, "--version")
	cmd.Env = append(cleanEnv(), "PATH="+os.Getenv("PATH"))
	out, _ := cmd.Output()
	v := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	versionCache.Store(d.p.Harness, v)
	return v
}

// saveResults merges this run's results into the record in the repository, so make
// harness-table shows them. A run that recorded nothing leaves the record as it is.
func saveResults() error {
	liveResults.Lock()
	defer liveResults.Unlock()
	if len(liveResults.r.Harnesses) == 0 && len(liveResults.handovers) == 0 {
		return nil
	}
	path := filepath.Join(repoRoot, support.ResultsFile)
	all, err := support.LoadResults(path)
	if err != nil {
		return err
	}
	for harness, got := range liveResults.r.Harnesses {
		for scenario, res := range got.Scenarios {
			all.Record(harness, scenario, res)
		}
		if v := liveResults.versions[harness]; v != "" {
			all.Harnesses[harness].Version = v
		}
	}
	for harness, samples := range liveResults.handovers {
		if all.Harnesses[harness] == nil {
			all.Harnesses[harness] = &support.HarnessResults{Scenarios: map[string]support.Result{}}
		}
		all.Harnesses[harness].Handover = typicalHandover(samples)
	}
	return all.Save(path)
}
