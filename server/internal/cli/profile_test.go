package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
)

// Each harness profile matches the schema, lists exactly the hooks aboard init installs
// for that harness and the files it writes in each scope, and lists the variables the CLI checks for its sandbox and sessions.
func TestProfilesMatchTheSchemaAndTheInstalledHooks(t *testing.T) {
	raw, err := os.ReadFile("../../../spec/harness-profile.schema.json")
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

	installed := map[string][]hookSpec{"claude-code": claudeHooks("aboard", true), "codex": codexHooks("aboard")}
	for harness, specs := range installed {
		t.Run(harness, func(t *testing.T) {
			data, err := adapters.Profiles.ReadFile(harness + "/profile.yaml")
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

			var p struct {
				Harness    string   `yaml:"harness"`
				Name       string   `yaml:"name"`
				SandboxEnv []string `yaml:"sandbox_env"`
				SessionEnv []string `yaml:"session_env"`
				ConfigDir  struct {
					Env     string `yaml:"env"`
					Default string `yaml:"default"`
				} `yaml:"config_dir"`
				SkillsDir string `yaml:"skills_dir"`
				Project   struct {
					SkillsDir string `yaml:"skills_dir"`
					HooksFile string `yaml:"hooks_file"`
				} `yaml:"project"`
				AllowCommands struct {
					Rule string `yaml:"rule"`
					File string `yaml:"file"`
				} `yaml:"allow_commands"`
				Delivery struct {
					HooksFile string `yaml:"hooks_file"`
					Hooks     []struct {
						Event    string   `yaml:"event"`
						Run      string   `yaml:"run"`
						Since    string   `yaml:"since"`
						Fallback []string `yaml:"fallback"`
					} `yaml:"hooks"`
				} `yaml:"delivery"`
			}
			if err := yaml.Unmarshal(data, &p); err != nil {
				t.Fatal(err)
			}
			if p.Harness != harness {
				t.Fatalf("profile in %s/ names harness %q", harness, p.Harness)
			}
			var listed, want []string
			for _, h := range p.Delivery.Hooks {
				listed = append(listed, h.Event+" "+h.Run)
			}
			for _, s := range specs {
				want = append(want, s.event+" "+s.arg)
			}
			slices.Sort(listed)
			slices.Sort(want)
			if !slices.Equal(listed, want) {
				t.Fatalf("profile hooks %v, aboard init installs %v", listed, want)
			}
			for _, h := range p.Delivery.Hooks {
				if h.Since == "" {
					continue
				}
				// The fallback events are what aboard init installs for an older version.
				var older []string
				for _, s := range claudeHooks("aboard", false) {
					if s.arg == h.Run {
						older = append(older, s.event)
					}
				}
				if harness != "claude-code" || h.Since != claudeBatchSince || !slices.Equal(h.Fallback, older) {
					t.Fatalf("profile hook %s since %s falls back to %v; aboard init uses %s and %v", h.Event, h.Since, h.Fallback, claudeBatchSince, older)
				}
			}

			// Global setup follows the harness's config variable, and its default without it.
			home, dir, set := filepath.FromSlash("/h"), filepath.FromSlash("/p"), filepath.FromSlash("/c")
			for _, vars := range []map[string]string{{"HOME": home}, {"HOME": home, p.ConfigDir.Env: set}} {
				a := &app{env: Env{Dir: dir, Getenv: func(k string) string { return vars[k] }}}
				config := filepath.Join(home, p.ConfigDir.Default)
				if v, ok := vars[p.ConfigDir.Env]; ok {
					config = v
				}
				global := func(path string) string {
					if rest, ok := strings.CutPrefix(path, "{config_dir}/"); ok {
						return filepath.Join(config, rest)
					}
					return filepath.Join(home, path)
				}
				g, project := a.setupFiles(harness, scopeGlobal), a.setupFiles(harness, scopeProject)
				allow, rule := g.hooks, claudeAllowRule
				if harness == "codex" {
					allow, rule = filepath.Join(config, p.AllowCommands.File), codexAllowRule
				}
				got := []string{g.skill, g.hooks, project.skill, project.hooks, g.allow, rule}
				want := []string{
					global(p.SkillsDir + "/aboard/SKILL.md"), global(p.Delivery.HooksFile),
					filepath.Join(dir, p.Project.SkillsDir, "aboard", "SKILL.md"), filepath.Join(dir, p.Project.HooksFile),
					allow, p.AllowCommands.Rule,
				}
				if !slices.Equal(got, want) {
					t.Fatalf("with %v, aboard init writes %v, the profile says %v", vars, got, want)
				}
				if d := configDirs[harness]; d.env != p.ConfigDir.Env || d.home != p.ConfigDir.Default {
					t.Fatalf("profile config_dir %+v, the CLI uses %+v", p.ConfigDir, d)
				}
			}

			var markers []string
			for _, m := range sandboxMarkers {
				if m.harness == p.Name {
					markers = append(markers, m.env)
				}
			}
			if !slices.Equal(markers, p.SandboxEnv) {
				t.Fatalf("profile sandbox_env %v, the CLI checks %v", p.SandboxEnv, markers)
			}
			markers = nil
			for _, m := range sessionMarkers {
				if m.harness == p.Name {
					markers = append(markers, m.env)
				}
			}
			if !slices.Equal(markers, p.SessionEnv) {
				t.Fatalf("profile session_env %v, the CLI checks %v", p.SessionEnv, markers)
			}
		})
	}
}
