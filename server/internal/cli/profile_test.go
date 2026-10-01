package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
)

// Each harness profile matches the schema, and lists exactly the hooks aboard init
// installs for that harness.
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
				Harness  string `yaml:"harness"`
				Delivery struct {
					Hooks []struct {
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
		})
	}
}
