package registry

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// Every profile in adapters/ matches the schema and is in the registry, and every
// harness in the registry has one. A profile's hooks each say what they do, and a hook
// that came with a version names the events older versions use instead.
func TestEveryProfileMatchesTheSchemaAndIsRegistered(t *testing.T) {
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
	files, err := fs.Glob(adapters.Profiles, "*/profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var folders []string
	for _, f := range files {
		folders = append(folders, filepath.Dir(f))
	}
	if got := Harnesses().Names(); !slices.Equal(got, folders) {
		t.Fatalf("the registry lists %v; adapters/ has profiles for %v", got, folders)
	}
	for _, h := range Harnesses() {
		name := h.Profile().Harness
		t.Run(name, func(t *testing.T) {
			data, err := adapters.Profiles.ReadFile(name + "/profile.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err := yaml.Unmarshal(data, &v); err != nil {
				t.Fatal(err)
			}
			// Round-trip through JSON so the validator sees JSON types.
			j, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(inst); err != nil {
				t.Fatalf("profile doesn't match the schema: %v", err)
			}
			for _, s := range h.Profile().Delivery.Hooks {
				if s.Op == "" {
					t.Errorf("hook %s runs %s and doesn't say what it does (op)", s.Event, s.Run)
				}
				if s.Since != "" && len(s.Fallback) == 0 {
					t.Errorf("hook %s came with version %s but names no events for older versions", s.Event, s.Since)
				}
				if c, ok := h.HookCall(s.Run, harness.HookInput{}); !ok || c.Op != s.Op {
					t.Errorf("aboard hook %s %s does %v, the profile says %s", name, s.Run, c.Op, s.Op)
				}
			}
			if _, ok := harness.Find(h.Items(harness.Env{Getenv: func(string) string { return "/h" }}, harness.ScopeGlobal), harness.ItemHooks); !ok && len(h.Profile().Delivery.Hooks) > 0 {
				t.Errorf("the profile lists hooks but no install item of kind hooks")
			}
		})
	}
}

// Global setup follows a harness's config variable when it is an absolute path, and its
// default folder under the home directory otherwise; a project setup goes under the
// project.
func TestInstallPathsFollowTheConfigFolder(t *testing.T) {
	claude, _ := Harnesses().Get("claude-code")
	codex, _ := Harnesses().Get("codex")
	omp, _ := Harnesses().Get("omp")
	env := func(vars map[string]string) harness.Env {
		return harness.Env{Dir: "/p", Getenv: func(k string) string { return vars[k] }}
	}
	paths := func(h harness.Harness, e harness.Env, scope string) []string {
		var out []string
		for _, it := range h.Items(e, scope) {
			out = append(out, string(it.Kind)+" "+it.Path)
		}
		return out
	}
	tests := []struct {
		h     harness.Harness
		vars  map[string]string
		scope string
		want  []string
	}{
		{claude, map[string]string{"HOME": "/h"}, harness.ScopeGlobal, []string{
			"skill /h/.claude/skills/aboard/SKILL.md", "hooks /h/.claude/settings.json", "allow-rule ", "consent ",
		}},
		{claude, map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": "/c"}, harness.ScopeGlobal, []string{
			"skill /c/skills/aboard/SKILL.md", "hooks /c/settings.json", "allow-rule ", "consent ",
		}},
		{claude, map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": "relative"}, harness.ScopeGlobal, []string{
			"skill /h/.claude/skills/aboard/SKILL.md", "hooks /h/.claude/settings.json", "allow-rule ", "consent ",
		}},
		{claude, map[string]string{"HOME": "/h", "CLAUDE_CONFIG_DIR": "/c"}, harness.ScopeProject, []string{
			"skill /p/.claude/skills/aboard/SKILL.md", "hooks /p/.claude/settings.local.json", "allow-rule ", "consent ",
		}},
		{codex, map[string]string{"HOME": "/h", "CODEX_HOME": "/x"}, harness.ScopeGlobal, []string{
			"skill /h/.agents/skills/aboard/SKILL.md", "hooks /x/hooks.json", "allow-rule /x/rules/aboard.rules", "consent ",
		}},
		{codex, map[string]string{"HOME": "/h"}, harness.ScopeProject, []string{
			"skill /p/.agents/skills/aboard/SKILL.md", "hooks /p/.codex/hooks.json", "allow-rule /p/.codex/rules/aboard.rules", "consent ",
		}},
		{omp, map[string]string{"HOME": "/h"}, harness.ScopeGlobal, []string{
			"skill /h/.omp/agent/skills/aboard/SKILL.md", "file /h/.omp/agent/extensions/aboard.ts",
		}},
		{omp, map[string]string{"HOME": "/h", "PI_CODING_AGENT_DIR": "/a"}, harness.ScopeGlobal, []string{
			"skill /a/skills/aboard/SKILL.md", "file /a/extensions/aboard.ts",
		}},
		{omp, map[string]string{"HOME": "/h", "PI_CODING_AGENT_DIR": "/a"}, harness.ScopeProject, []string{
			"skill /p/.omp/skills/aboard/SKILL.md", "file /p/.omp/extensions/aboard.ts",
		}},
	}
	for _, tt := range tests {
		if got := paths(tt.h, env(tt.vars), tt.scope); !slices.Equal(got, tt.want) {
			t.Errorf("%s with %v, %s: %q, want %q", tt.h.Profile().Harness, tt.vars, tt.scope, got, tt.want)
		}
	}
}

// A command finds its session from ABOARD_SESSION, then from a harness's own variable.
// omp sets CLAUDECODE as well as OMPCODE in every command, so with OMPCODE set nothing
// is taken for Claude Code: the command runs in an omp session, whose id Aboard's
// extension puts in ABOARD_SESSION. A Codex started inside a Claude Code or omp session
// inherits its ABOARD_SESSION and is taken for Codex.
func TestSessionDetectionChecksTheMostSpecificMarkerFirst(t *testing.T) {
	tests := []struct {
		name        string
		vars        map[string]string
		wantSession string
		wantTitle   string
		wantIn      bool
	}{
		{"plain terminal", nil, "", "", false},
		{"claude code", map[string]string{"CLAUDECODE": "1", "ABOARD_SESSION": "claude-code:5f1c"}, "claude-code:5f1c", "Claude Code", true},
		{"claude code before its hook ran", map[string]string{"CLAUDECODE": "1"}, "", "Claude Code", true},
		{"codex", map[string]string{"CODEX_THREAD_ID": " 019a "}, "codex:019a", "Codex", true},
		{"aboard's session first", map[string]string{"ABOARD_SESSION": "codex:019b", "CODEX_THREAD_ID": "019a"}, "codex:019b", "Codex", true},
		{"codex started from a claude code session", map[string]string{"CLAUDECODE": "1", "ABOARD_SESSION": "claude-code:5f1c", "CODEX_THREAD_ID": "019a"}, "codex:019a", "Codex", true},
		{"omp before its extension ran", map[string]string{"OMPCODE": "1", "CLAUDECODE": "1"}, "", "omp", true},
		{"omp started from a claude code session", map[string]string{"OMPCODE": "1", "CLAUDECODE": "1", "ABOARD_SESSION": "claude-code:5f1c"}, "", "omp", true},
		{"omp", map[string]string{"OMPCODE": "1", "CLAUDECODE": "1", "ABOARD_SESSION": "omp:0199"}, "omp:0199", "omp", true},
		{"codex started from an omp session", map[string]string{"OMPCODE": "1", "CLAUDECODE": "1", "ABOARD_SESSION": "omp:0199", "CODEX_THREAD_ID": "019a"}, "codex:019a", "Codex", true},
		{"claude code's sandbox", map[string]string{"SANDBOX_RUNTIME": "1"}, "", "Claude Code", true},
		{"codex's sandbox", map[string]string{"CODEX_SANDBOX": "seatbelt"}, "", "Codex", true},
	}
	for _, tt := range tests {
		e := harness.Env{Getenv: func(k string) string { return tt.vars[k] }}
		got := ""
		if key, ok := Harnesses().Session(e); ok {
			got = key.String()
		}
		if got != tt.wantSession {
			t.Errorf("%s: session %q, want %q", tt.name, got, tt.wantSession)
		}
		if title, in := Harnesses().InSession(e); title != tt.wantTitle || in != tt.wantIn {
			t.Errorf("%s: in session of %q (%v), want %q (%v)", tt.name, title, in, tt.wantTitle, tt.wantIn)
		}
	}
}

// Codex's sandbox blocks network access when it says so; Claude Code's never does here.
func TestNetworkBlockedComesFromTheProfile(t *testing.T) {
	e := func(vars map[string]string) harness.Env {
		return harness.Env{Getenv: func(k string) string { return vars[k] }}
	}
	if title, ok := Harnesses().NetworkBlocked(e(map[string]string{"CODEX_SANDBOX_NETWORK_DISABLED": "1"})); !ok || title != "Codex" {
		t.Fatalf("got %q %v", title, ok)
	}
	if _, ok := Harnesses().NetworkBlocked(e(map[string]string{"CODEX_SANDBOX": "seatbelt", "SANDBOX_RUNTIME": "1"})); ok {
		t.Fatal("a sandbox that doesn't block the network was taken for one that does")
	}
}

// A subagent's mark counts unless a more specific marker says the command runs in a
// harness started inside that subagent. A Codex sub-agent's thread id differs from its
// root session's.
func TestSubagentMarkFollowsTheSession(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"main conversation", map[string]string{"ABOARD_SESSION": "claude-code:5f1c"}, ""},
		{"claude code subagent", map[string]string{"ABOARD_SESSION": "claude-code:5f1c", "ABOARD_SUBAGENT": "a1b2"}, "a1b2"},
		{"codex started from a subagent", map[string]string{"ABOARD_SESSION": "claude-code:5f1c", "ABOARD_SUBAGENT": "a1b2", "CODEX_THREAD_ID": "019a"}, ""},
		{"no session", map[string]string{"ABOARD_SUBAGENT": "a1b2"}, "a1b2"},
		{"codex root", map[string]string{"CODEX_THREAD_ID": "019a", "CODEX_SESSION_ID": "019a"}, ""},
		{"codex sub-agent", map[string]string{"CODEX_THREAD_ID": "019c", "CODEX_SESSION_ID": "019a"}, "019c"},
		{"codex before CODEX_SESSION_ID", map[string]string{"CODEX_THREAD_ID": "019c"}, ""},
		{"omp subagent", map[string]string{"OMPCODE": "1", "ABOARD_SESSION": "omp:0199", "ABOARD_SUBAGENT": "0-Explore"}, "0-Explore"},
	}
	for _, tt := range tests {
		e := harness.Env{Getenv: func(k string) string { return tt.vars[k] }}
		got, _ := Harnesses().Subagent(e)
		if got != tt.want {
			t.Errorf("%s: subagent %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A profile that marks subagents says so, and one that says so has a way to mark them:
// identity.root_env, a hook of op mark-subagent, or Aboard's extension, which marks them.
func TestSubagentIdentityMatchesTheHooks(t *testing.T) {
	for _, h := range Harnesses() {
		p := h.Profile()
		marks := p.Identity.RootEnv != "" || p.Identity.Kind == "extension" ||
			slices.ContainsFunc(p.Delivery.Hooks, func(s harness.HookSpec) bool { return s.Op == harness.OpMarkSubagent })
		declared := p.SubagentIdentity == "marked" || p.SubagentIdentity == "seats"
		if marks != declared {
			t.Errorf("%s: subagent_identity %q, but a mark-subagent hook is %v", p.Harness, p.SubagentIdentity, marks)
		}
	}
}

// The file aboard init installs inside a harness names this machine's aboard binary and
// ABOARD_HOME, quoted so the file still reads as code, and leaves no placeholder.
func TestInstalledFilesNameThisAboard(t *testing.T) {
	omp, _ := Harnesses().Get("omp")
	e := harness.Env{
		Dir: "/p", Getenv: func(k string) string { return map[string]string{"HOME": "/h"}[k] },
		Aboard: `/opt/my "tools"/aboard`, AboardHome: "/h/aboard",
	}
	it, _ := harness.Find(omp.Items(e, harness.ScopeGlobal), harness.ItemFile)
	data := string(it.Data)
	for _, want := range []string{`const INSTALLED_ABOARD = "/opt/my \"tools\"/aboard";`, `const INSTALLED_HOME = "/h/aboard";`} {
		if !strings.Contains(data, want) {
			t.Errorf("the installed extension lacks %s", want)
		}
	}
	if strings.Contains(data, "{aboard_") {
		t.Error("the installed extension still has a placeholder")
	}
}
