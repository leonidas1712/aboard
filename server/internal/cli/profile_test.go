package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

	installed := map[string][]hookSpec{"claude-code": claudeHooks("aboard"), "codex": codexHooks("aboard")}
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
				SkillsDir  string   `yaml:"skills_dir"`
				Project    struct {
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
						Event string `yaml:"event"`
						Run   string `yaml:"run"`
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

			home, dir := filepath.FromSlash("/h"), filepath.FromSlash("/p")
			a := &app{env: Env{Dir: dir, Getenv: func(k string) string { return map[string]string{"HOME": home}[k] }}}
			global, project := a.setupFiles(harness, scopeGlobal), a.setupFiles(harness, scopeProject)
			allow, rule := global.hooks, claudeAllowRule
			if harness == "codex" {
				allow, rule = filepath.Join(home, ".codex", p.AllowCommands.File), codexAllowRule
			}
			got := []string{global.skill, global.hooks, project.skill, project.hooks, global.allow, rule}
			want = []string{
				filepath.Join(home, p.SkillsDir, "aboard", "SKILL.md"), filepath.Join(home, p.Delivery.HooksFile),
				filepath.Join(dir, p.Project.SkillsDir, "aboard", "SKILL.md"), filepath.Join(dir, p.Project.HooksFile),
				allow, p.AllowCommands.Rule,
			}
			if !slices.Equal(got, want) {
				t.Fatalf("aboard init writes %v, the profile says %v", got, want)
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
