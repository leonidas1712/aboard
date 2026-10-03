// Package support is what the harness conformance kits and the README's harness table
// share: the harness profiles as a client reads them, the capabilities a harness is
// measured on, the live suite's recorded results, and the support matrix built from
// them. It reads only the public contracts (the profiles in adapters/ and their schema),
// so a kit outside this repository could use it the same way.
package support

import (
	"fmt"
	"io/fs"
	"path"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
)

// Profile is the part of a harness profile (spec/harness-profile.schema.json) the kits
// and the table read.
type Profile struct {
	Harness   string `yaml:"harness"`
	Name      string `yaml:"name"`
	Command   string `yaml:"command"`
	CheckName string `yaml:"check_name"`
	Checks    struct {
		MinVersion string `yaml:"min_version"`
	} `yaml:"checks"`
	SandboxEnv        []string `yaml:"sandbox_env"`
	SandboxNetworkEnv []string `yaml:"sandbox_network_env"`
	SessionEnv        []string `yaml:"session_env"`
	ConfigDir         struct {
		Env     string `yaml:"env"`
		Default string `yaml:"default"`
	} `yaml:"config_dir"`
	Install     []Item `yaml:"install"`
	Interactive struct {
		Start  []string `yaml:"start"`
		Resume []string `yaml:"resume"`
	} `yaml:"interactive"`
	Headless struct {
		Via string `yaml:"via"`
	} `yaml:"headless"`
	Identity struct {
		Kind     string   `yaml:"kind"`
		Env      string   `yaml:"env"`
		RootEnv  string   `yaml:"root_env"`
		EnvFile  string   `yaml:"env_file"`
		YieldsTo []string `yaml:"yields_to"`
	} `yaml:"identity"`
	SubagentIdentity string `yaml:"subagent_identity"`
	Lifecycle        struct {
		Liveness         string `yaml:"liveness"`
		OutlivesTerminal bool   `yaml:"outlives_terminal"`
		ResumeKeepsID    bool   `yaml:"resume_keeps_id"`
		ResumeStart      string `yaml:"resume_start"`
		SubagentField    string `yaml:"subagent_field"`
	} `yaml:"lifecycle"`
	Delivery struct {
		Method        string   `yaml:"method"`
		Capabilities  []string `yaml:"capabilities"`
		Hooks         []Hook   `yaml:"hooks"`
		MidTurn       string   `yaml:"mid_turn"`
		WaitingNotice bool     `yaml:"waiting_notice"`
	} `yaml:"delivery"`
}

// Item is one thing aboard init installs.
type Item struct {
	Kind    string `yaml:"kind"`
	Global  string `yaml:"global"`
	Project string `yaml:"project"`
	Format  string `yaml:"format"`
	Rule    string `yaml:"rule"`
	// Required is set for an allow rule the harness can't reach Aboard without.
	Required map[string]string `yaml:"required"`
	Missing  *struct {
		Level string `yaml:"level"`
	} `yaml:"missing"`
	Text string `yaml:"text"`
}

// Hook is one hook the profile lists.
type Hook struct {
	Event   string         `yaml:"event"`
	Run     string         `yaml:"run"`
	Op      string         `yaml:"op"`
	Matcher string         `yaml:"matcher"`
	Timeout int            `yaml:"timeout"`
	Options map[string]any `yaml:"options"`
}

// Profiles reads every profile built into aboard, in the order of their folders.
func Profiles() ([]Profile, error) {
	files, err := fs.Glob(adapters.Profiles, "*/profile.yaml")
	if err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(files))
	for _, f := range files {
		raw, err := adapters.Profiles.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var p Profile
		if err := yaml.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("read adapters/%s: %w", f, err)
		}
		if want := path.Dir(f); p.Harness != want {
			return nil, fmt.Errorf("adapters/%s names harness %q", f, p.Harness)
		}
		if p.CheckName == "" {
			p.CheckName = underscores(p.Harness)
		}
		out = append(out, p)
	}
	return out, nil
}

func underscores(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c == '-' {
			b[i] = '_'
		}
	}
	return string(b)
}

// Has reports whether the profile declares a delivery capability.
func (p Profile) Has(capability string) bool {
	return slices.Contains(p.Delivery.Capabilities, capability)
}

// Hook returns the profile's hook for an operation, such as "wait".
func (p Profile) Hook(op string) (Hook, bool) {
	for _, h := range p.Delivery.Hooks {
		if h.Op == op {
			return h, true
		}
	}
	return Hook{}, false
}

// Item returns the profile's first install item of a kind.
func (p Profile) Item(kind string) (Item, bool) {
	for _, it := range p.Install {
		if it.Kind == kind {
			return it, true
		}
	}
	return Item{}, false
}

// WaitsForIdle reports whether bundles go only to a hook that waits while the session is
// idle, so a handed bundle is confirmed later, by the session's next event.
func (p Profile) WaitsForIdle() bool { return p.Has("idle-hook") }

// Delivers reports whether the harness has automatic delivery of any kind.
func (p Profile) Delivers() bool {
	return p.Has("idle-hook") || p.Has("queue") || p.Has("extension")
}
