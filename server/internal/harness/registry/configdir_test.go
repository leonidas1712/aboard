package registry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
)

// A profile that names no config folder fails the conformance kit's schema check, so no
// harness can be added that a development sandbox (scripts/sandbox) couldn't point at a
// folder of its own.
func TestProfileWithoutConfigDirFails(t *testing.T) {
	schema := profileSchema(t)
	data, err := adapters.Profiles.ReadFile("codex/profile.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	delete(v, "config_dir")
	j, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(inst); err == nil || !strings.Contains(err.Error(), "config_dir") {
		t.Fatalf("a profile without config_dir passed the schema: %v", err)
	}
}
