package harness

import (
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Set is the harnesses Aboard knows, in the order they are listed and set up.
type Set []Harness

// Get returns the harness with a name.
func (s Set) Get(name string) (Harness, bool) {
	for _, h := range s {
		if h.Profile().Harness == name {
			return h, true
		}
	}
	return nil, false
}

// Names returns the harnesses' names, in order.
func (s Set) Names() []string {
	names := make([]string, len(s))
	for i, h := range s {
		names[i] = h.Profile().Harness
	}
	return names
}

// Titles returns the names people know the harnesses by, in order.
func (s Set) Titles() []string {
	titles := make([]string, len(s))
	for i, h := range s {
		titles[i] = h.Profile().Name
	}
	return titles
}

// OrList writes words as "a, b or c".
func OrList(words []string) string { return joinList(words, " or ") }

// AndList writes words as "a, b and c".
func AndList(words []string) string { return joinList(words, " and ") }

func joinList(words []string, last string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + last + words[len(words)-1]
}

// byPrecedence returns the harnesses with the highest identity precedence first, in
// their order otherwise. Detection checks the most specific markers first.
func (s Set) byPrecedence() Set {
	out := slices.Clone(s)
	slices.SortStableFunc(out, func(a, b Harness) int {
		return b.Profile().Identity.Precedence - a.Profile().Identity.Precedence
	})
	return out
}

// yields reports whether a command runs in another harness that sets this harness's
// markers too.
func yields(h Harness, e Env) bool {
	return slices.ContainsFunc(h.Profile().Identity.YieldsTo, func(v string) bool { return e.Getenv(v) != "" })
}

// Session returns the harness session a command runs in: ABOARD_SESSION, which
// Aboard's hooks or extension write, then the variable of a harness that gives every
// command its session id. A session of a harness that gives way to another one whose
// marker is set is not taken.
func (s Set) Session(e Env) (delivery.SessionKey, bool) {
	if k, ok := delivery.ParseSessionKey(e.Getenv("ABOARD_SESSION")); ok {
		if h, known := s.Get(k.Harness); !known || !yields(h, e) {
			return k, true
		}
	}
	for _, h := range s.byPrecedence() {
		id := h.Profile().Identity
		if id.Kind != "env" || id.Env == "" || yields(h, e) {
			continue
		}
		if v := strings.TrimSpace(e.Getenv(id.Env)); v != "" {
			return delivery.SessionKey{Harness: h.Profile().Harness, ID: v}, true
		}
	}
	return delivery.SessionKey{}, false
}

// RootSession returns the session of the conversation a command runs under: the one
// Session finds, except in a sub-agent of a harness that gives every command both its
// own thread's id and its root session's (Codex), where it is the root's. A subagent
// thus finds the same session, and so the same agent, in every harness.
func (s Set) RootSession(e Env) (delivery.SessionKey, bool) {
	k, ok := s.Session(e)
	if !ok {
		return k, false
	}
	if h, known := s.Get(k.Harness); known {
		id := h.Profile().Identity
		if id.RootEnv != "" && strings.TrimSpace(e.Getenv(id.Env)) == k.ID {
			if root := strings.TrimSpace(e.Getenv(id.RootEnv)); root != "" {
				k.ID = root
			}
		}
	}
	return k, true
}

// Subagent returns the id of the subagent a command runs in: from the mark Aboard's hook
// or extension adds to a subagent's aboard commands, or, for a harness that gives every
// command both its own thread's id and its root session's (Codex), from the two being
// different. A mark inherited by a harness started inside that subagent doesn't count:
// a more specific marker says the command runs in another harness, as for
// ABOARD_SESSION.
func (s Set) Subagent(e Env) (string, bool) {
	if id := strings.TrimSpace(e.Getenv(SubagentEnv)); id != "" {
		k, ok := delivery.ParseSessionKey(e.Getenv("ABOARD_SESSION"))
		if h, known := s.Get(k.Harness); !ok || !known || !yields(h, e) {
			return id, true
		}
	}
	k, ok := s.Session(e)
	if !ok {
		return "", false
	}
	h, known := s.Get(k.Harness)
	if !known {
		return "", false
	}
	id := h.Profile().Identity
	if id.RootEnv == "" || strings.TrimSpace(e.Getenv(id.Env)) != k.ID {
		return "", false
	}
	if root := strings.TrimSpace(e.Getenv(id.RootEnv)); root != "" && root != k.ID {
		return k.ID, true
	}
	return "", false
}

// InSession reports the harness whose session a command runs in, from its markers or
// its sandbox's. title is "" when a marker is set but its harness gives way to one
// Aboard doesn't know: the command still runs in a session.
func (s Set) InSession(e Env) (title string, ok bool) {
	unknown := false
	for _, h := range s.byPrecedence() {
		if !slices.ContainsFunc(h.Profile().SessionEnv, func(v string) bool { return e.Getenv(v) != "" }) {
			continue
		}
		if yields(h, e) {
			unknown = true
			continue
		}
		return h.Profile().Name, true
	}
	if unknown {
		return "", true
	}
	if title, _, ok := s.Sandboxed(e); ok {
		return title, true
	}
	return "", false
}

// Sandboxed reports the harness whose sandbox a command runs in, and how to get the
// delivery daemon started outside it. A daemon started there would inherit the
// sandbox and couldn't reach the harness.
func (s Set) Sandboxed(e Env) (title, fix string, ok bool) {
	for _, h := range s.byPrecedence() {
		p := h.Profile()
		if slices.ContainsFunc(p.SandboxEnv, func(v string) bool { return e.Getenv(v) != "" }) {
			return p.Name, p.SandboxFix, true
		}
	}
	return "", "", false
}

// NetworkBlocked reports the harness whose sandbox a command runs in when that sandbox
// blocks network access, as Codex's does by default: then neither the server's
// address nor the daemon's socket can be reached from there, running or not.
func (s Set) NetworkBlocked(e Env) (title string, ok bool) {
	for _, h := range s.byPrecedence() {
		p := h.Profile()
		if slices.ContainsFunc(p.SandboxNetworkEnv, func(v string) bool { return e.Getenv(v) != "" }) {
			return p.Name, true
		}
	}
	return "", false
}
