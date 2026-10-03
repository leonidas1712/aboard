//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

var (
	cliSchemaOnce sync.Once
	cliSchemas    *jsonschema.Compiler
	cliSchemaErr  error
)

// loadSpec reads a YAML spec file as JSON values. OpenAPI's `nullable: true` becomes a
// "null" in the node's type, which is what it means in JSON Schema.
func loadSpec(path string) (any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(j))
	if err != nil {
		return nil, err
	}
	var fix func(any)
	fix = func(n any) {
		switch n := n.(type) {
		case map[string]any:
			if n["nullable"] == true {
				if t, ok := n["type"].(string); ok {
					n["type"] = []any{t, "null"}
				}
				if e, ok := n["enum"].([]any); ok {
					n["enum"] = append(e, nil)
				}
				if _, ok := n["$ref"]; ok {
					n["anyOf"] = []any{map[string]any{"$ref": n["$ref"]}, map[string]any{"type": "null"}}
					delete(n, "$ref")
				}
			}
			for _, c := range n {
				fix(c)
			}
		case []any:
			for _, c := range n {
				fix(c)
			}
		}
	}
	fix(doc)
	return doc, nil
}

// matchesCLISpec fails unless v matches the named definition in spec/cli.yaml.
func matchesCLISpec(t *testing.T, def string, v any) {
	t.Helper()
	cliSchemaOnce.Do(func() {
		c := jsonschema.NewCompiler()
		for _, f := range []string{"cli.yaml", "openapi.yaml"} {
			doc, err := loadSpec("../spec/" + f)
			if err != nil {
				cliSchemaErr = err
				return
			}
			if err := c.AddResource("https://aboard.example/spec/"+f, doc); err != nil {
				cliSchemaErr = err
				return
			}
		}
		cliSchemas = c
	})
	if cliSchemaErr != nil {
		t.Fatal(cliSchemaErr)
	}
	s, err := cliSchemas.Compile("https://aboard.example/spec/cli.yaml#/$defs/" + def)
	if err != nil {
		t.Fatalf("compile %s: %v", def, err)
	}
	if err := s.Validate(v); err != nil {
		raw, _ := json.MarshalIndent(v, "", "  ")
		t.Fatalf("output doesn't match %s in spec/cli.yaml: %v\n%s", def, err, raw)
	}
}
