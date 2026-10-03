package harness

import (
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/leonidas1712/aboard/adapters"
)

// Profile is the part of a harness profile (adapters/<harness>/profile.yaml, schema
// spec/harness-profile.schema.json) that Aboard's code reads. The rest of the file
// describes the harness for launchers, the conformance kit and people.
type Profile struct {
	Harness   string `yaml:"harness"`
	Name      string `yaml:"name"`
	Command   string `yaml:"command"`
	CheckName string `yaml:"check_name"`
	Checks    struct {
		Installed  Check  `yaml:"installed"`
		LoggedIn   Check  `yaml:"logged_in"`
		MinVersion string `yaml:"min_version"`
	} `yaml:"checks"`
	SandboxEnv        []string `yaml:"sandbox_env"`
	SandboxNetworkEnv []string `yaml:"sandbox_network_env"`
	SandboxFix        string   `yaml:"sandbox_fix"`
	SessionEnv        []string `yaml:"session_env"`
	ConfigDir         struct {
		Env     string `yaml:"env"`
		Default string `yaml:"default"`
	} `yaml:"config_dir"`
	Install  []InstallSpec `yaml:"install"`
	Identity struct {
		Kind       string   `yaml:"kind"`
		Env        string   `yaml:"env"`
		RootEnv    string   `yaml:"root_env"`
		EnvFile    string   `yaml:"env_file"`
		Precedence int      `yaml:"precedence"`
		YieldsTo   []string `yaml:"yields_to"`
	} `yaml:"identity"`
	// SubagentIdentity is whether a subagent's commands can be told from its parent's:
	// none, marked or seats.
	SubagentIdentity string `yaml:"subagent_identity"`
	Lifecycle        struct {
		Liveness         string `yaml:"liveness"`
		OutlivesTerminal bool   `yaml:"outlives_terminal"`
		ResumeKeepsID    bool   `yaml:"resume_keeps_id"`
		ResumeStart      string `yaml:"resume_start"`
		SubagentField    string `yaml:"subagent_field"`
	} `yaml:"lifecycle"`
	Delivery struct {
		Method       string     `yaml:"method"`
		Capabilities []string   `yaml:"capabilities"`
		Hooks        []HookSpec `yaml:"hooks"`
		MidTurn      string     `yaml:"mid_turn"`
	} `yaml:"delivery"`
}

// Check is a command that checks something about the harness.
type Check struct {
	Run      []string `yaml:"run"`
	JSONTrue string   `yaml:"json_true"`
}

// InstallSpec is one item of a profile's install list, with its paths unresolved.
type InstallSpec struct {
	Kind        ItemKind       `yaml:"kind"`
	Global      string         `yaml:"global"`
	Project     string         `yaml:"project"`
	Format      string         `yaml:"format"`
	Source      string         `yaml:"source"`
	Rule        string         `yaml:"rule"`
	Accepts     []string       `yaml:"accepts"`
	Note        string         `yaml:"note"`
	Required    *AllowRequired `yaml:"required"`
	Missing     *HooksMissing  `yaml:"missing"`
	Text        string         `yaml:"text"`
	ProjectText string         `yaml:"project_text"`
}

// AllowRequired holds what Aboard says about an allow rule the harness can't reach
// Aboard without, such as Codex's, whose sandbox blocks network access.
type AllowRequired struct {
	// Why is init's reason for asking.
	Why string `yaml:"why"`
	// Advice is the line init adds when it sets the harness up without the rule.
	Advice string `yaml:"advice"`
	// Missing is what init's overview says without the rule.
	Missing string `yaml:"missing"`
	// Allowed and NotAllowed are what doctor says with and without it.
	Allowed    string `yaml:"allowed"`
	NotAllowed string `yaml:"not_allowed"`
}

// HooksMissing is what doctor reports when the harness's hooks aren't installed.
type HooksMissing struct {
	Level  string `yaml:"level"`
	Effect string `yaml:"effect"`
}

// HookSpec is one hook a profile lists.
type HookSpec struct {
	Event    string         `yaml:"event"`
	Run      string         `yaml:"run"`
	Op       Op             `yaml:"op"`
	Matcher  string         `yaml:"matcher"`
	Timeout  int            `yaml:"timeout"`
	Options  map[string]any `yaml:"options"`
	Since    string         `yaml:"since"`
	Fallback []string       `yaml:"fallback"`
}

// LoadProfile reads the profile built into aboard for a harness.
func LoadProfile(name string) (*Profile, error) {
	raw, err := adapters.Profiles.ReadFile(name + "/profile.yaml")
	if err != nil {
		return nil, fmt.Errorf("read the %s profile: %w", name, err)
	}
	var p Profile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("read the %s profile: %w", name, err)
	}
	if p.Harness != name {
		return nil, fmt.Errorf("the profile in adapters/%s names harness %q", name, p.Harness)
	}
	if p.CheckName == "" {
		p.CheckName = strings.ReplaceAll(p.Harness, "-", "_")
	}
	return &p, nil
}

// HasCapability reports whether the harness's delivery has a capability, such as
// "idle-hook".
func (p *Profile) HasCapability(c string) bool {
	return slices.Contains(p.Delivery.Capabilities, c)
}
