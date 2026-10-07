// Package rules holds the rules a board enforces on every write: role permissions, policy
// presets, who a message is addressed to, and who may read it. It is pure logic with no
// storage, so every rule can be tested directly.
package rules

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Permissions a role can grant.
const (
	Post        = "post"
	Broadcast   = "broadcast"
	Urgent      = "urgent"
	CreateTasks = "create_tasks"
	ClaimTasks  = "claim_tasks"
	WriteNotes  = "write_notes"
	UploadFiles = "upload_files"
	Invite      = "invite"
	AddPeople   = "add_people"
	EditCharter = "edit_charter"
)

// Permissions lists every permission, in the order the board file schema gives them.
var Permissions = []string{Post, Broadcast, Urgent, CreateTasks, ClaimTasks, WriteNotes, UploadFiles, Invite, AddPeople, EditCharter}

// Grant is one entry in a role's `can` list: a permission, or claim_tasks limited to
// some task types.
type Grant struct {
	Permission string
	TaskTypes  []string
}

// MarshalJSON writes a plain permission as a string and a limited one as an object.
func (g Grant) MarshalJSON() ([]byte, error) {
	if g.TaskTypes == nil {
		return json.Marshal(g.Permission)
	}
	return json.Marshal(map[string][]string{g.Permission: g.TaskTypes})
}

// UnmarshalJSON reads either form.
func (g *Grant) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		return g.set(s, nil)
	}
	var m map[string][]string
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("permission must be a name or {claim_tasks: [types]}: %w", err)
	}
	return g.setLimited(m)
}

// UnmarshalYAML reads either form from a board file.
func (g *Grant) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		return g.set(n.Value, nil)
	}
	var m map[string][]string
	if err := n.Decode(&m); err != nil {
		return fmt.Errorf("line %d: permission must be a name or {claim_tasks: [types]}", n.Line)
	}
	return g.setLimited(m)
}

func (g *Grant) setLimited(m map[string][]string) error {
	types, ok := m[ClaimTasks]
	if len(m) != 1 || !ok || len(types) == 0 {
		return fmt.Errorf("only claim_tasks can be limited, as {claim_tasks: [types]}")
	}
	return g.set(ClaimTasks, types)
}

func (g *Grant) set(p string, types []string) error {
	if !slices.Contains(Permissions, p) {
		return fmt.Errorf("unknown permission %q; allowed: %s", p, strings.Join(Permissions, ", "))
	}
	g.Permission, g.TaskTypes = p, types
	return nil
}

// Role is a named set of permissions with its own charter text.
type Role struct {
	Charter string  `json:"charter,omitempty" yaml:"charter"`
	Can     []Grant `json:"can" yaml:"can"`
}

// Has reports whether the role grants permission p in any form.
func (r Role) Has(p string) bool {
	return slices.ContainsFunc(r.Can, func(g Grant) bool { return g.Permission == p })
}

// MemberRole is the role every board has.
const MemberRole = "member"

// DefaultMemberRole is the `member` role when a board doesn't define it.
func DefaultMemberRole() Role {
	return Role{Can: []Grant{{Permission: Post}, {Permission: CreateTasks}, {Permission: ClaimTasks}, {Permission: WriteNotes}, {Permission: UploadFiles}, {Permission: Invite}, {Permission: AddPeople}}}
}

// Policy values.
const (
	Starter     = "starter"
	Recommended = "recommended"

	VisibilityOpen      = "open"
	VisibilityAddressed = "addressed"

	Everyone = "everyone"
	Granted  = "granted"
)

// Policy is what the server enforces on a board.
type Policy struct {
	Nudges     string `json:"nudges"`
	Preset     string `json:"preset"`
	Visibility string `json:"visibility"`
	Broadcast  string `json:"broadcast"`
	Urgent     string `json:"urgent"`
	// ShowHarness lets agents see which harness other agents run. When false, new
	// agents without a chosen name get neutral names.
	ShowHarness bool     `json:"show_harness"`
	Overrides   []string `json:"overrides"`
}

// UnmarshalJSON defaults missing show_harness to true and nudges to on.
func (p *Policy) UnmarshalJSON(b []byte) error {
	type plain Policy
	out := plain{ShowHarness: true, Nudges: "on"}
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*p = Policy(out)
	return nil
}

// PolicyChange is a request to change a board's policy: an optional preset, then
// optional individual keys on top of it.
type PolicyChange struct {
	Nudges     string `json:"nudges,omitempty" yaml:"nudges"`
	Preset     string `json:"preset,omitempty" yaml:"preset"`
	Visibility string `json:"visibility,omitempty" yaml:"visibility"`
	Broadcast  string `json:"broadcast,omitempty" yaml:"broadcast"`
	Urgent     string `json:"urgent,omitempty" yaml:"urgent"`
	// ShowHarness is nil when the change leaves it alone.
	ShowHarness *bool `json:"show_harness,omitempty" yaml:"show_harness"`
}

// Preset returns the policy a preset stands for.
func Preset(name string) (Policy, error) {
	switch name {
	case Starter:
		return Policy{Preset: Starter, Visibility: VisibilityOpen, Broadcast: Everyone, Urgent: Everyone, ShowHarness: true, Nudges: "on", Overrides: []string{}}, nil
	case Recommended:
		return Policy{Preset: Recommended, Visibility: VisibilityAddressed, Broadcast: Granted, Urgent: Granted, ShowHarness: true, Nudges: "on", Overrides: []string{}}, nil
	}
	return Policy{}, fmt.Errorf("unknown preset %q; allowed: starter, recommended", name)
}

// Apply returns the policy after a change. A preset in the change replaces every key;
// individual keys then override it and are listed in Overrides when they differ from
// the preset.
func (p Policy) Apply(c PolicyChange) (Policy, error) {
	base := p.Preset
	if c.Preset != "" {
		base = c.Preset
	}
	out, err := Preset(base)
	if err != nil {
		return Policy{}, err
	}
	if c.Preset == "" {
		out.Visibility, out.Broadcast, out.Urgent, out.ShowHarness = p.Visibility, p.Broadcast, p.Urgent, p.ShowHarness
		if p.Nudges != "" {
			out.Nudges = p.Nudges
		}
	}
	if c.ShowHarness != nil {
		out.ShowHarness = *c.ShowHarness
	}
	set := func(key, val string, allowed []string, dst *string) error {
		if val == "" {
			return nil
		}
		if !slices.Contains(allowed, val) {
			return fmt.Errorf("%s must be one of %s, not %q", key, strings.Join(allowed, ", "), val)
		}
		*dst = val
		return nil
	}
	if err := set("visibility", c.Visibility, []string{VisibilityOpen, VisibilityAddressed}, &out.Visibility); err != nil {
		return Policy{}, err
	}
	if err := set("broadcast", c.Broadcast, []string{Everyone, Granted}, &out.Broadcast); err != nil {
		return Policy{}, err
	}
	if err := set("urgent", c.Urgent, []string{Everyone, Granted}, &out.Urgent); err != nil {
		return Policy{}, err
	}
	if err := set("nudges", c.Nudges, []string{"on", "off"}, &out.Nudges); err != nil {
		return Policy{}, err
	}
	preset, _ := Preset(out.Preset)
	out.Overrides = []string{}
	for _, k := range []struct{ key, got, want string }{
		{"visibility", out.Visibility, preset.Visibility},
		{"broadcast", out.Broadcast, preset.Broadcast},
		{"urgent", out.Urgent, preset.Urgent},
		{"nudges", out.Nudges, preset.Nudges},
	} {
		if k.got != k.want {
			out.Overrides = append(out.Overrides, k.key)
		}
	}
	if out.ShowHarness != preset.ShowHarness {
		out.Overrides = append(out.Overrides, "show_harness")
	}
	return out, nil
}

// Access levels of a person on a board. An admin may change the board's charter, roles,
// policy and monitor settings; a member may act only on their own agents. Agents have
// no access level.
const (
	AccessAdmin  = "admin"
	AccessMember = "member"
)

// Member is what the rules need to know about a board member.
type Member struct {
	ID      string
	Name    string
	Kind    string // "agent" or "human"
	Role    string // empty for humans
	HumanID string // the person, or the agent's owner
	Access  string // AccessAdmin or AccessMember for a person, empty for an agent
}

// IsHuman reports whether the member is a person.
func (m Member) IsHuman() bool { return m.Kind == "human" }

// IsAdmin reports whether the member is a person who may change the board's rules.
func (m Member) IsAdmin() bool { return m.IsHuman() && m.Access == AccessAdmin }

// CanManage reports whether person may pause or remove agent: only the agent's owner or
// an admin of its board may.
func CanManage(person, agent Member) bool {
	return person.IsHuman() && (person.IsAdmin() || person.HumanID == agent.HumanID)
}

// Target kinds in a message's `to` list.
const (
	TargetAll  = "all"
	TargetName = "name"
	TargetRole = "role"
)

// ParseTarget splits "all", "@name" or "role:R" into its kind and value.
func ParseTarget(t string) (kind, value string, ok bool) {
	switch {
	case t == "all":
		return TargetAll, "", true
	case strings.HasPrefix(t, "@") && len(t) > 1:
		return TargetName, t[1:], true
	case strings.HasPrefix(t, "role:") && len(t) > 5:
		return TargetRole, t[5:], true
	}
	return "", "", false
}

// AddressedTo reports whether a message sent to `to` is addressed to m.
func AddressedTo(to []string, m Member) bool {
	for _, t := range to {
		kind, v, _ := ParseTarget(t)
		switch {
		case kind == TargetAll,
			kind == TargetName && v == m.Name,
			kind == TargetRole && v == m.Role && m.Role != "":
			return true
		}
	}
	return false
}

// CanRead reports whether reader may read a message from senderID to `to` on a board
// with policy p. Humans read everything; under addressed visibility, agents read only
// what they sent or what was addressed to them.
func CanRead(p Policy, to []string, senderID string, reader Member) bool {
	return p.Visibility == VisibilityOpen || reader.IsHuman() || reader.ID == senderID || AddressedTo(to, reader)
}

// Refusal says which permission a post lacks.
type Refusal string

// Reasons a post is refused.
const (
	NeedsPost      Refusal = "post"
	NeedsBroadcast Refusal = "broadcast"
	NeedsUrgent    Refusal = "urgent"
)

// CheckPost decides whether sender may post to `to` with the urgent flag. Humans may
// always post. It returns "" when the post is allowed.
func CheckPost(p Policy, role Role, sender Member, to []string, urgent bool) Refusal {
	if sender.IsHuman() {
		return ""
	}
	if !role.Has(Post) {
		return NeedsPost
	}
	if slices.Contains(to, TargetAll) && p.Broadcast != Everyone && !role.Has(Broadcast) {
		return NeedsBroadcast
	}
	if urgent && p.Urgent != Everyone && !role.Has(Urgent) {
		return NeedsUrgent
	}
	return ""
}

// harnessNames are the agent names for harnesses whose own name is longer than people
// call them.
var harnessNames = map[string]string{"claude-code": "claude"}

// AgentNameBase is the name a new agent gets before a number is added: its harness's
// short name when the harness is known, otherwise its role.
func AgentNameBase(harness, role string) string {
	if !strings.ContainsFunc(harness, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' }) {
		return role
	}
	h := NormalizeName(harness)
	if short, ok := harnessNames[h]; ok {
		return short
	}
	return h
}

// AllocateNumberedName returns the first free base-1, base-2, and so on: neutral names
// that say nothing about the agent.
func AllocateNumberedName(base string, taken func(string) bool) string {
	for i := 1; ; i++ {
		if name := fmt.Sprintf("%s-%d", base, i); !taken(name) {
			return name
		}
	}
}

// AllocateName returns base if it is free, otherwise base-2, base-3, and so on.
func AllocateName(base string, taken func(string) bool) string {
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s-%d", base, i); !taken(name) {
			return name
		}
	}
}

// NormalizeName turns a login or display name into a valid member name: lowercase
// letters, digits and dashes, at most 40 characters.
func NormalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "-")
	}
	if out == "" {
		return "human"
	}
	return out
}
