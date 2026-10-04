package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/server/internal/rules"
	"github.com/leonidas1712/aboard/spec"
)

// defaultBoardFile is the board file swarm up reads when --file isn't given.
const defaultBoardFile = "aboard.yaml"

// swarmFile is the part of a board file swarm up reads (spec/aboard.schema.json).
type swarmFile struct {
	Board    string         `json:"board"`
	Title    string         `json:"title"`
	Template string         `json:"template"`
	Charter  string         `json:"charter"`
	Roles    map[string]any `json:"roles"`
	Policy   map[string]any `json:"policy"`
	Launcher string         `json:"launcher"`
	Agents   []swarmSpec    `json:"agents"`
	// path is the file's absolute path; dir its folder.
	path, dir string
}

// swarmSpec is one agent in the board file's agents section.
type swarmSpec struct {
	Name     string   `json:"name"`
	Harness  string   `json:"harness"`
	Role     string   `json:"role"`
	Launcher string   `json:"launcher"`
	Model    string   `json:"model"`
	Args     []string `json:"args"`
	Dir      string   `json:"dir"`
	// Prompt is nil when the file leaves it out, and points to "" for no first prompt.
	Prompt *string `json:"prompt"`
}

// role is the agent's role: the file's, else member.
func (s swarmSpec) role() string {
	if s.Role == "" {
		return rules.MemberRole
	}
	return s.Role
}

// readSwarmFile reads a board file and checks it against the board file's schema and
// against what swarm up can do with it.
func (a *app) readSwarmFile(path string) (swarmFile, error) {
	if path == "" {
		path = defaultBoardFile
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(a.env.Dir, path)
	}
	raw, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return swarmFile{}, newError("board_file_not_found", "There is no board file at "+path+".",
			"Write an aboard.yaml with a board name and an agents section (see aboard help swarm), or pass --file.")
	}
	if err != nil {
		return swarmFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	invalid := func(problems ...string) error {
		e := newError("board_file_invalid", fmt.Sprintf("%s isn't a board file swarm up can use: %s", path, strings.Join(problems, "; ")),
			"Fix the file; spec/aboard.schema.json lists every key and the values each takes.")
		e.Details = map[string]any{"file": path, "errors": problems}
		return e
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return swarmFile{}, invalid("it isn't YAML: " + err.Error())
	}
	asJSON, err := json.Marshal(doc)
	if err != nil {
		return swarmFile{}, invalid("it has values JSON can't hold: " + err.Error())
	}
	if problems := schemaProblems(asJSON); len(problems) > 0 {
		return swarmFile{}, invalid(problems...)
	}
	var f swarmFile
	if err := json.Unmarshal(asJSON, &f); err != nil {
		return swarmFile{}, invalid(err.Error())
	}
	f.path, f.dir = path, filepath.Dir(path)
	var problems []string
	if f.Board == "" {
		problems = append(problems, "/board: swarm up needs the board's name")
	}
	if len(f.Agents) == 0 {
		problems = append(problems, "/agents: list at least one agent to start")
	}
	if len(f.Roles) > 0 {
		problems = append(problems, "/roles: swarm up creates boards from a template and can't set roles; use a template's roles and remove this section")
	}
	for k := range f.Policy {
		if k != "preset" {
			problems = append(problems, "/policy/"+k+": swarm up sets only policy.preset; change other keys with aboard board policy")
		}
	}
	seen := map[string]bool{}
	for i, ag := range f.Agents {
		if seen[ag.Name] {
			problems = append(problems, fmt.Sprintf("/agents/%d/name: %s is named twice", i, ag.Name))
		}
		seen[ag.Name] = true
		if _, ok := a.registry().Get(ag.Harness); !ok {
			problems = append(problems, fmt.Sprintf("/agents/%d/harness: %q has no harness profile; use %s", i, ag.Harness, strings.Join(a.registry().Names(), ", ")))
		}
	}
	if len(problems) > 0 {
		return swarmFile{}, invalid(problems...)
	}
	return f, nil
}

// boardFileSchema is the compiled board file schema.
var boardFileSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(spec.BoardFileSchema))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("aboard.schema.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("aboard.schema.json")
})

// schemaProblems checks a board file, as JSON, against its schema, and returns each
// problem as "<where>: <what>".
func schemaProblems(asJSON []byte) []string {
	schema, err := boardFileSchema()
	if err != nil {
		return []string{"the board file schema built into aboard doesn't compile: " + err.Error()}
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(asJSON))
	if err != nil {
		return []string{err.Error()}
	}
	err = schema.Validate(inst)
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, "/"+strings.Join(e.InstanceLocation, "/")+": "+leafText(e))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	slices.Sort(out)
	return slices.Compact(out)
}

// leafText is what one schema check says, without the location the caller shows.
func leafText(e *jsonschema.ValidationError) string {
	text := e.Error()
	if rest, ok := strings.CutPrefix(text, "at '"); ok {
		if _, msg, found := strings.Cut(rest, "': "); found {
			return msg
		}
	}
	return text
}
