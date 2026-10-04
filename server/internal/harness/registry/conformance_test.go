package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// This file is the in-process half of the harness conformance kit; e2e/conformance_test.go
// is the half that drives the real binary. Both run for every harness in the registry
// with make conformance, or for one with make conformance HARNESS=<name>.

// fakeBin holds stand-ins for harness commands that delivery adapters run: the fake
// codex from e2e/fakecodex. codexQueueLog is where it records what it queued.
var fakeBin, codexQueueLog string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "aboard-conformance-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	fakeBin = filepath.Join(dir, "bin")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", filepath.Join(fakeBin, "codex"), "github.com/leonidas1712/aboard/e2e/fakecodex") //nolint:gosec // builds this repository's fake
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build the fake codex:", err)
		return 1
	}
	// The adapters run their harness's command from the PATH, in this process.
	_ = os.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	codexQueueLog = filepath.Join(dir, "codex-queue.jsonl")
	_ = os.Setenv("FAKE_CODEX_LOG", codexQueueLog)
	_ = os.Setenv("FAKE_CODEX_THREADS", filepath.Join(dir, "codex-threads.json"))
	threads := `{"019a0000-0000-7000-8000-00000000000b":{"parent":"019a0000-0000-7000-8000-00000000000a"},` +
		`"019a0000-0000-7000-8000-0000000000dd":{"missing":true}}`
	if err := os.WriteFile(filepath.Join(dir, "codex-threads.json"), []byte(threads), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// selected reports whether the kit runs for a harness: every harness, or those HARNESS
// names, separated by commas.
func selected(name string) bool {
	only := os.Getenv("HARNESS")
	return only == "" || slices.Contains(strings.Split(only, ","), name)
}

// adapterFixtures give the adapter contract what a harness's own delivery adapter needs
// beyond a session id: a fake of the harness's command and what it received. A harness
// whose adapter waits for an idle hook needs none. A new harness with an adapter of
// another kind adds its fixture here, with a fake of its command in e2e/.
var adapterFixtures = map[string]func(t *testing.T, h harness.Harness) deliverytest.AdapterFixture{
	"codex": func(_ *testing.T, h harness.Harness) deliverytest.AdapterFixture {
		var n atomic.Int64
		return deliverytest.AdapterFixture{
			New: func(*testing.T) delivery.Adapter { return h.Adapter("conformance") },
			Root: func(*testing.T) string {
				return fmt.Sprintf("019a0000-0000-7000-8000-%012d", 1000+n.Add(1))
			},
			SubAgent: func(*testing.T) string { return "019a0000-0000-7000-8000-00000000000b" },
			Absent:   func(*testing.T) string { return "019a0000-0000-7000-8000-0000000000dd" },
			Received: fakeCodexQueued,
		}
	},
}

// fakeCodexQueued returns what the fake codex queued for a thread, in order.
func fakeCodexQueued(t *testing.T, thread string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(codexQueueLog))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c map[string]string
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if c["thread"] == thread {
			out = append(out, c["message"])
		}
	}
	return out
}

// TestHarnessConformance is the in-process part of the harness conformance kit. For
// every harness in the registry: its profile matches the schema, every capability it
// declares has code behind it, its delivery adapter passes the delivery port's contract,
// its markers find its sessions, giving way where the profile says, and aboard init
// never writes a hook event the harness's version doesn't run.
func TestHarnessConformance(t *testing.T) {
	schema := profileSchema(t)
	for _, h := range Harnesses() {
		name := h.Profile().Harness
		if !selected(name) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Run("Profile", func(t *testing.T) { checkProfile(t, schema, h) })
			t.Run("Capabilities", func(t *testing.T) { checkCapabilities(t, h) })
			t.Run("AdapterContract", func(t *testing.T) { checkAdapter(t, h) })
			t.Run("Identity", func(t *testing.T) { checkIdentity(t, h) })
			t.Run("Versions", func(t *testing.T) { checkVersions(t, h) })
		})
	}
}

func profileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("../../../../spec/harness-profile.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("harness-profile.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := c.Compile("harness-profile.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// checkProfile validates the profile against the schema, and checks each hook says
// what it does and that aboard hook does that.
func checkProfile(t *testing.T, schema *jsonschema.Schema, h harness.Harness) {
	p := h.Profile()
	data, err := adapters.Profiles.ReadFile(p.Harness + "/profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	j, err := json.Marshal(v) // the validator wants JSON types
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(inst); err != nil {
		t.Fatalf("adapters/%s/profile.yaml doesn't match spec/harness-profile.schema.json: %v", p.Harness, err)
	}
	if versioned(p) && p.Checks.MinVersion == "" {
		t.Errorf("its hooks depend on the version, so checks.min_version names what a version that can't be read gets")
	}
	for _, s := range p.Delivery.Hooks {
		if s.Op == "" {
			t.Errorf("hook %s runs %s and doesn't say what it does (op)", s.Event, s.Run)
		}
		if versioned(p) && s.Since == "" {
			t.Errorf("hook %s has no since: in a profile where one hook depends on the version, every hook names the first version that runs it", s.Event)
		}
		for _, f := range s.Fallback {
			if f.Since == "" {
				t.Errorf("hook %s falls back to %s without naming the first version that knows it (since)", s.Event, f.Event)
			}
		}
		if s.Since != "" && len(s.Fallback) == 0 && s.Without == "" {
			t.Errorf("hook %s needs version %s and has no fallback, so it says what a person loses without it (without)", s.Event, s.Since)
		}
		if c, ok := h.HookCall(s.Run, harness.HookInput{}); !ok || c.Op != s.Op {
			t.Errorf("aboard hook %s %s does %q; the profile says %q", p.Harness, s.Run, c.Op, s.Op)
		}
	}
}

// versioned reports whether one of a profile's hooks depends on the harness's version.
func versioned(p *harness.Profile) bool {
	return slices.ContainsFunc(p.Delivery.Hooks, func(s harness.HookSpec) bool { return s.Since != "" })
}

// checkVersions checks that aboard init writes only hook events the harness's version
// runs, since an unknown one can make it ignore the whole settings file: just below the
// version each hook came in, that hook is left out; an old version and one that can't
// be read get only what checks.min_version runs; the newest gets every hook.
func checkVersions(t *testing.T, h harness.Harness) {
	p := h.Profile()
	if !versioned(p) {
		t.Skip("no hook depends on the harness's version")
	}
	versions := []string{"0.0.1", p.Checks.MinVersion, below(p.Checks.MinVersion)}
	for _, s := range p.Delivery.Hooks {
		versions = append(versions, s.Since, below(s.Since))
		for _, f := range s.Fallback {
			versions = append(versions, f.Since, below(f.Since))
		}
	}
	for _, v := range versions {
		hooks := h.Hooks("aboard", v)
		for _, hk := range hooks {
			first, ok := firstVersion(p, hk)
			if !ok {
				t.Errorf("at %s, aboard init writes %s for %s, which the profile doesn't list", v, hk.Event, hk.Arg)
			} else if !harness.VersionAtLeast(v, first) {
				t.Errorf("at %s, aboard init writes %s, which needs %s", v, hk.Event, first)
			}
		}
		for _, s := range h.Unsupported(v) {
			if slices.ContainsFunc(hooks, func(hk harness.Hook) bool { return hk.Arg == s.Run }) {
				t.Errorf("at %s, %s is reported unsupported but aboard init writes it", v, s.Event)
			}
		}
		for _, s := range p.Delivery.Hooks {
			if harness.VersionAtLeast(v, s.Since) && !slices.ContainsFunc(hooks, func(hk harness.Hook) bool { return hk.Event == s.Event && hk.Arg == s.Run }) {
				t.Errorf("at %s, aboard init leaves out %s, which that version runs", v, s.Event)
			}
		}
	}
	if got := h.Hooks("aboard", "0.0.1"); len(got) != 0 {
		t.Errorf("a version older than every hook gets %d hooks", len(got))
	}
	oldest := h.Hooks("aboard", p.Checks.MinVersion)
	for _, unknown := range []string{"", "unknown (" + p.Name + ")"} {
		if got := h.Hooks("aboard", unknown); !slices.EqualFunc(got, oldest, sameHook) {
			t.Errorf("a version that can't be read (%q) gets %v; want what %s gets, %v", unknown, events(got), p.Checks.MinVersion, events(oldest))
		}
	}
	newest := h.Hooks("aboard", harness.Newest)
	if len(newest) != len(p.Delivery.Hooks) || len(h.Unsupported(harness.Newest)) != 0 {
		t.Errorf("the newest version gets %v; want every hook the profile lists", events(newest))
	}
	for i, hk := range newest {
		if s := p.Delivery.Hooks[i]; hk.Event != s.Event || hk.Arg != s.Run {
			t.Errorf("the newest version gets %s for %s; the profile lists %s", hk.Event, hk.Arg, s.Event)
		}
	}
}

// firstVersion returns the first version that runs a hook entry, from the profile hook or
// fallback it came from.
func firstVersion(p *harness.Profile, hk harness.Hook) (string, bool) {
	for _, s := range p.Delivery.Hooks {
		if s.Run != hk.Arg {
			continue
		}
		if s.Event == hk.Event {
			return s.Since, true
		}
		for _, f := range s.Fallback {
			if f.Event == hk.Event {
				return f.Since, true
			}
		}
	}
	return "", false
}

// below returns a version just older than v: 2.1.117 for 2.1.118, 0.113.999 for 0.114.0.
func below(v string) string {
	parts := strings.Split(v, ".")
	for i := len(parts) - 1; i >= 0; i-- {
		n := 0
		_, _ = fmt.Sscan(parts[i], &n)
		if n > 0 {
			parts[i] = fmt.Sprint(n - 1)
			for j := i + 1; j < len(parts); j++ {
				parts[j] = "999"
			}
			return strings.Join(parts, ".")
		}
	}
	return v
}

func sameHook(a, b harness.Hook) bool { return a.Event == b.Event && a.Arg == b.Arg }

func events(hooks []harness.Hook) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.Event
	}
	return out
}

// hasOp reports whether the profile has a hook of an operation.
func hasOp(p *harness.Profile, op harness.Op) bool {
	return slices.ContainsFunc(p.Delivery.Hooks, func(s harness.HookSpec) bool { return s.Op == op })
}

// checkCapabilities fails for every capability the profile declares that no code in
// this aboard backs, naming what is missing.
func checkCapabilities(t *testing.T, h harness.Harness) {
	p := h.Profile()
	ad := h.Adapter("conformance")
	items := h.Items(harness.Env{Getenv: func(string) string { return "/home" }, Dir: "/project"}, harness.ScopeGlobal)

	// Baseline: a skill, and a way for a command to find its session.
	if _, ok := harness.Find(items, harness.ItemSkill); !ok {
		t.Errorf("install has no item of kind skill: every harness gets the Aboard skill")
	}
	switch p.Identity.Kind {
	case "env":
		if p.Identity.Env == "" {
			t.Errorf("identity kind env names no variable (identity.env)")
		}
	case "hook":
		if p.Identity.EnvFile == "" || !hasOp(p, harness.OpSessionStart) {
			t.Errorf("identity kind hook needs identity.env_file and a hook of op session-start that writes it")
		}
	case "extension":
		if !p.HasCapability("extension") {
			t.Errorf("identity kind extension needs the delivery capability extension: the extension's hello registers the session")
		}
		if _, ok := harness.Find(items, harness.ItemFile); !ok {
			t.Errorf("identity kind extension needs an install item of kind file: the extension aboard init installs")
		}
	default:
		t.Errorf("identity.kind %q: say where a session's id comes from (env, hook or extension)", p.Identity.Kind)
	}
	if len(p.SessionEnv) == 0 {
		t.Errorf("session_env lists no markers, so commands in a session would be taken for a person's")
	}
	if p.Lifecycle.Liveness == "connection" && !p.HasCapability("extension") {
		t.Errorf("lifecycle.liveness connection needs the delivery capability extension, whose connection keeps the session alive")
	}

	// Delivery: the method and each capability.
	caps := p.Delivery.Capabilities
	method := map[string]string{"stop-hook": "idle-hook", "queue": "queue", "extension": "extension", "none": "none"}[p.Delivery.Method]
	declared := slices.Contains(caps, method) || method == "none" && len(caps) == 0
	if method == "" || !declared {
		t.Errorf("delivery.method %q needs the capability %q", p.Delivery.Method, method)
	}
	for _, c := range caps {
		switch c {
		case "idle-hook":
			if ad == nil || !ad.WaitsForIdle() {
				t.Errorf("capability idle-hook: the harness's delivery adapter must wait for an idle hook")
			}
			if !hasOp(p, harness.OpWait) {
				t.Errorf("capability idle-hook: no hook of op wait")
			}
		case "queue":
			if ad == nil || ad.WaitsForIdle() {
				t.Errorf("capability queue: the harness needs a delivery adapter that queues (Adapter in its Go package)")
			}
			if len(p.Delivery.Queue) == 0 {
				t.Errorf("capability queue: delivery.queue names no command")
			}
		case "tool-boundary":
			if !hasOp(p, harness.OpTool) {
				t.Errorf("capability tool-boundary: no hook of op tool")
			}
			if p.Delivery.MidTurn != "tool-hook" {
				t.Errorf("capability tool-boundary: delivery.mid_turn is %q, want tool-hook", p.Delivery.MidTurn)
			}
		case "turn-start":
			t.Errorf("capability turn-start: no code in this aboard adds messages as a turn starts")
		case "extension":
			if ad == nil || !ad.WaitsForIdle() {
				t.Errorf("capability extension: the harness's delivery adapter must hand bundles to the extension's connection while the session is idle")
			}
			if p.Identity.Kind != "extension" {
				t.Errorf("capability extension: identity.kind is %q, want extension: the connection is opened for the session the extension names", p.Identity.Kind)
			}
		case "none":
			if ad != nil {
				t.Errorf("capability none, but the harness has a delivery adapter")
			}
		}
	}
	if p.Delivery.MidTurn == "tool-hook" && !slices.Contains(caps, "tool-boundary") && !slices.Contains(caps, "extension") {
		t.Errorf("delivery.mid_turn tool-hook needs the capability tool-boundary")
	}
	if p.Delivery.WaitingNotice && !slices.Contains(caps, "tool-boundary") && !slices.Contains(caps, "extension") {
		t.Errorf("delivery.waiting_notice needs the capability tool-boundary or extension, where the notice is added")
	}
	if p.Delivery.Method == "stop-hook" || p.Delivery.Method == "queue" {
		if !hasOp(p, harness.OpSessionStart) || !hasOp(p, harness.OpEnd) {
			t.Errorf("delivery %s needs hooks of op session-start and end, which open and close the session", p.Delivery.Method)
		}
	}
	if len(p.Delivery.Hooks) > 0 {
		if _, ok := harness.Find(items, harness.ItemHooks); !ok {
			t.Errorf("the profile lists hooks but no install item of kind hooks")
		}
	}

	// Install items.
	for _, it := range p.Install {
		switch it.Kind {
		case harness.ItemFile:
			if f, _ := harness.Find(items, harness.ItemFile); len(f.Data) == 0 {
				t.Errorf("install item of kind file (%s): aboard has no such file to install", it.Source)
			}
			if it.Global == "" && it.Project == "" {
				t.Errorf("install item of kind file (%s) names no path to install it at", it.Source)
			}
		case harness.ItemHooks:
			if it.Format != "json" {
				t.Errorf("install item hooks has format %q: aboard merges only json hooks files", it.Format)
			}
		case harness.ItemAllowRule:
			if it.Rule == "" {
				t.Errorf("install item allow-rule has no rule")
			}
		case harness.ItemSkill, harness.ItemConsent:
		}
	}

	// Subagents.
	switch p.SubagentIdentity {
	case "", "none":
	case "marked":
		if p.Identity.RootEnv == "" && !hasOp(p, harness.OpMarkSubagent) && p.Identity.Kind != "extension" {
			t.Errorf("subagent_identity marked needs a way to mark: a hook of op mark-subagent, identity.root_env, or Aboard's extension")
		}
	case "seats":
		t.Errorf("subagent_identity seats: no code in this aboard gives a subagent a seat")
	}
	if p.SubagentIdentity == "none" && (p.Identity.RootEnv != "" || hasOp(p, harness.OpMarkSubagent)) {
		t.Errorf("subagent_identity none, but the profile can mark subagents; declare marked")
	}
	if hasOp(p, harness.OpMarkSubagent) && p.Lifecycle.SubagentField == "" {
		t.Errorf("a hook of op mark-subagent needs lifecycle.subagent_field, the hook-input field that names the subagent")
	}
	if f := p.Lifecycle.SubagentField; f != "" && f != "agent_id" {
		t.Errorf("lifecycle.subagent_field %q: the hooks read only agent_id", f)
	}
}

// checkAdapter runs the delivery port's adapter contract against the harness's adapter.
func checkAdapter(t *testing.T, h harness.Harness) {
	ad := h.Adapter("conformance")
	if ad == nil {
		t.Skip("no automatic delivery: the skill has the agent run aboard inbox --wait")
	}
	if ad.Harness() != h.Profile().Harness {
		t.Fatalf("the adapter calls itself %q; the profile's harness is %q", ad.Harness(), h.Profile().Harness)
	}
	if fixture, ok := adapterFixtures[h.Profile().Harness]; ok {
		deliverytest.RunAdapter(t, fixture(t, h))
		return
	}
	if !ad.WaitsForIdle() {
		t.Fatalf("add a fixture for %s to adapterFixtures in this file: its adapter hands bundles to the harness itself, so the contract needs a fake of the harness's command", h.Profile().Harness)
	}
	var n atomic.Int64
	deliverytest.RunAdapter(t, deliverytest.AdapterFixture{
		New:      func(*testing.T) delivery.Adapter { return h.Adapter("conformance") },
		Root:     func(*testing.T) string { return fmt.Sprintf("5f1c2d3e-0000-4000-8000-%012d", n.Add(1)) },
		SubAgent: func(*testing.T) string { return "" },
		Absent:   func(*testing.T) string { return "" },
		// An extension answers received for each bundle it adds, which confirms it.
		ConfirmsOnHand: h.Profile().HasCapability("extension"),
	})
}

// checkIdentity checks the harness's markers find its sessions, and give way to a more
// specific marker as the profile says, among all the harnesses in the registry.
func checkIdentity(t *testing.T, h harness.Harness) {
	all := Harnesses()
	p := h.Profile()
	env := func(vars map[string]string) harness.Env {
		return harness.Env{Getenv: func(k string) string { return vars[k] }}
	}
	// own is what a command in one of the harness's sessions sees.
	own := func(id string) map[string]string {
		vars := map[string]string{}
		for _, m := range p.SessionEnv {
			vars[m] = "1"
		}
		switch p.Identity.Kind {
		case "env":
			vars[p.Identity.Env] = id
			if p.Identity.RootEnv != "" {
				vars[p.Identity.RootEnv] = id
			}
		case "hook", "extension":
			vars["ABOARD_SESSION"] = p.Harness + ":" + id
		}
		return vars
	}
	want := p.Harness + ":019a0000-0000-7000-8000-0000000000e1"
	vars := own("019a0000-0000-7000-8000-0000000000e1")
	if k, ok := all.Session(env(vars)); !ok || k.String() != want {
		t.Errorf("a command in a %s session is taken for session %q, want %q", p.Name, k.String(), want)
	}
	if title, in := all.InSession(env(vars)); !in || title != p.Name {
		t.Errorf("a command in a %s session is taken for one of %q (%v)", p.Name, title, in)
	}
	if id, ok := all.Subagent(env(vars)); ok {
		t.Errorf("a command in the session's own conversation is taken for subagent %q", id)
	}

	// A more specific marker wins over this harness's: a nested harness, or one that sets
	// this harness's markers too.
	for _, v := range p.Identity.YieldsTo {
		nested := own("019a0000-0000-7000-8000-0000000000e1")
		nested[v] = "019a0000-0000-7000-8000-0000000000e2"
		k, ok := all.Session(env(nested))
		if ok && k.Harness == p.Harness {
			t.Errorf("with %s set, a command is still taken for %s's session %s", v, p.Name, k)
		}
		for _, o := range all {
			if id := o.Profile().Identity; id.Kind == "env" && id.Env == v {
				if !ok || k.Harness != o.Profile().Harness {
					t.Errorf("a %s started inside a %s session is taken for %q, want its own session", o.Profile().Name, p.Name, k.String())
				}
			}
		}
	}

	// A harness started inside one of this harness's sessions inherits the session's
	// environment: ABOARD_SESSION when this harness's identity comes through it, or its
	// own session variable. The nested harness's variable must win.
	for _, o := range all {
		op := o.Profile()
		if op.Harness == p.Harness || op.Identity.Kind != "env" || op.Identity.Env == "" {
			continue
		}
		yields := slices.Contains(p.Identity.YieldsTo, op.Identity.Env)
		switch {
		case p.Identity.Kind != "env" && !yields:
			t.Errorf("a %s started inside a %s session inherits ABOARD_SESSION; list %s in identity.yields_to so its own session wins",
				op.Name, p.Name, op.Identity.Env)
		case p.Identity.Kind == "env" && p.Identity.Precedence == op.Identity.Precedence && !yields &&
			!slices.Contains(op.Identity.YieldsTo, p.Identity.Env):
			t.Errorf("%s and %s both give commands a session variable; give one a higher identity.precedence, or list the other's in identity.yields_to",
				p.Harness, op.Harness)
		}
	}

	// Two harnesses that set the same marker need a way to tell them apart.
	for _, o := range all {
		op := o.Profile()
		if op.Harness == p.Harness || p.Identity.Precedence != op.Identity.Precedence {
			continue
		}
		for _, m := range p.SessionEnv {
			if m == "ABOARD_SESSION" || !slices.Contains(op.SessionEnv, m) {
				continue
			}
			told := len(p.Identity.YieldsTo) > 0 && slices.ContainsFunc(op.SessionEnv, func(v string) bool { return slices.Contains(p.Identity.YieldsTo, v) }) ||
				len(op.Identity.YieldsTo) > 0 && slices.ContainsFunc(p.SessionEnv, func(v string) bool { return slices.Contains(op.Identity.YieldsTo, v) })
			if !told {
				t.Errorf("%s and %s both set %s: give one a higher identity.precedence, or list the other's own marker in identity.yields_to", p.Harness, op.Harness, m)
			}
		}
	}

	// A marked subagent's command is told from its parent's.
	if p.SubagentIdentity == "marked" {
		sub := own("019a0000-0000-7000-8000-0000000000e1")
		wantSub := "a1b2c3d4"
		if p.Identity.RootEnv != "" {
			wantSub = "019a0000-0000-7000-8000-0000000000e3"
			sub[p.Identity.Env] = wantSub
		} else {
			sub[harness.SubagentEnv] = wantSub
		}
		if id, ok := all.Subagent(env(sub)); !ok || id != wantSub {
			t.Errorf("a subagent's command in a %s session is taken for subagent %q (%v), want %q", p.Name, id, ok, wantSub)
		}
	}
}
