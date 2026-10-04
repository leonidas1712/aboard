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
		Installed Check `yaml:"installed"`
		LoggedIn  Check `yaml:"logged_in"`
		// MinVersion is the oldest version Aboard works with. A harness whose version
		// can't be read gets the hooks this version runs.
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
	Install     []InstallSpec `yaml:"install"`
	Interactive Interactive   `yaml:"interactive"`
	Headless    Headless      `yaml:"headless"`
	Identity    struct {
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
		Queue        []string   `yaml:"queue"`
		Hooks        []HookSpec `yaml:"hooks"`
		MidTurn      string     `yaml:"mid_turn"`
		// WaitingNotice is true when the tool hook also names the other waiting messages.
		WaitingNotice bool `yaml:"waiting_notice"`
	} `yaml:"delivery"`
}

// Interactive is how a launcher starts and resumes the harness in a terminal. Start and
// Resume hold {prompt} and {session}; Model holds {model}.
type Interactive struct {
	Start  []string `yaml:"start"`
	Resume []string `yaml:"resume"`
	Model  []string `yaml:"model"`
}

// Headless is how the headless launcher runs one turn of the harness without a
// terminal: Via is native (Run and Resume below), acp or none.
type Headless struct {
	Via          string   `yaml:"via"`
	Run          []string `yaml:"run"`
	Resume       []string `yaml:"resume"`
	SessionField string   `yaml:"session_field"`
	Model        []string `yaml:"model"`
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
	Event   string         `yaml:"event"`
	Run     string         `yaml:"run"`
	Op      Op             `yaml:"op"`
	Matcher string         `yaml:"matcher"`
	Timeout int            `yaml:"timeout"`
	Options map[string]any `yaml:"options"`
	// Since is the first version of the harness that runs the hook as written: it knows
	// the event, and honors its options and the output the hook gives.
	Since string `yaml:"since"`
	// Fallback are the events that run the same command on a version older than Since.
	Fallback []FallbackSpec `yaml:"fallback"`
	// Without is what the person loses on a version that runs neither the hook nor a
	// fallback, such as "idle sessions don't wake for new messages".
	Without string `yaml:"without"`
}

// FallbackSpec is one event a hook falls back to on an older version, with the first
// version that knows it. A profile may name the event alone, which any version knows.
type FallbackSpec struct {
	Event string `yaml:"event"`
	Since string `yaml:"since"`
}

// UnmarshalYAML reads a fallback written as an event name or as {event, since}.
func (f *FallbackSpec) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		f.Event = n.Value
		return nil
	}
	type plain FallbackSpec
	return n.Decode((*plain)(f))
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
	for _, it := range p.Install {
		if it.Kind != ItemFile {
			continue
		}
		if _, err := adapters.Files.ReadFile(it.Source); err != nil {
			return nil, fmt.Errorf("the %s profile installs %q, which isn't built into aboard: %w", name, it.Source, err)
		}
	}
	return &p, nil
}

// HasCapability reports whether the harness's delivery has a capability, such as
// "idle-hook".
func (p *Profile) HasCapability(c string) bool {
	return slices.Contains(p.Delivery.Capabilities, c)
}
