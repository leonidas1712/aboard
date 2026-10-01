// Package boardfile reads aboard.yaml board files and the built-in templates. A board file
// sets a board's charter, roles and policy; unknown keys are rejected so mistakes surface
// instead of being ignored.
package boardfile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/server/internal/rules"
	"github.com/leonidas1712/aboard/skills/templates"
)

// File is the part of aboard.yaml the server and `aboard pair` read.
type File struct {
	Board    string                `yaml:"board"`
	Template string                `yaml:"template"`
	Charter  string                `yaml:"charter"`
	Roles    map[string]rules.Role `yaml:"roles"`
	Policy   rules.PolicyChange    `yaml:"policy"`
	Pair     []string              `yaml:"pair"`
}

// Parse reads a board file, rejecting unknown keys and invalid values.
func Parse(data []byte) (File, error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return File{}, fmt.Errorf("read board file: %w", err)
	}
	if len(f.Pair) != 0 && len(f.Pair) != 2 {
		return File{}, errors.New("read board file: pair must list exactly two roles")
	}
	for _, r := range f.Pair {
		if _, ok := f.Roles[r]; !ok && r != rules.MemberRole {
			return File{}, fmt.Errorf("read board file: pair role %q is not defined in roles", r)
		}
	}
	return f, nil
}

// ErrNoTemplate is returned for a template name that isn't built in.
var ErrNoTemplate = errors.New("no such template")

// Template returns a built-in template by name.
func Template(name string) (File, error) {
	data, err := templates.FS.ReadFile(name + ".yaml")
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, fmt.Errorf("%w: %q (available: %s)", ErrNoTemplate, name, strings.Join(TemplateNames(), ", "))
	}
	if err != nil {
		return File{}, fmt.Errorf("read template %s: %w", name, err)
	}
	return Parse(data)
}

// TemplateNames lists the built-in templates, sorted.
func TemplateNames() []string {
	entries, _ := templates.FS.ReadDir(".")
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}
