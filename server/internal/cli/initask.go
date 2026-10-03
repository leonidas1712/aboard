package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

// askInit is the guided part of aboard init in a terminal: it shows what is already set
// up, then asks the questions no flag answered. set holds the flags that were given.
// It returns false when there is nothing to ask about or the person stopped, having
// said so.
func (a *app) askInit(ctx context.Context, c *initChoices, known []harnessSetup, set map[string]bool, current delivery.Mode) (bool, error) {
	st := a.out()
	overview, allCurrent, scope := a.setupOverview(ctx, known, current, st)
	_, _ = io.WriteString(a.env.Stdout, overview)
	var found []string
	for _, h := range known {
		if h.Detected {
			found = append(found, h.Name)
		}
	}
	if len(found) == 0 {
		none := "neither"
		if len(a.registry()) > 2 {
			none = "none of them"
		}
		_, _ = io.WriteString(a.env.Stdout, "\naboard init sets up "+harness.AndList(a.registry().Titles())+", and found "+none+" on this machine. "+
			"Install one, then run aboard init again.\n")
		return false, nil
	}
	stop := func(err error) (bool, error) {
		if errors.Is(err, errAborted) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return false, nil
		}
		return false, err
	}
	k := a.asker()
	_, _ = io.WriteString(a.env.Stdout, "\n")
	if allCurrent && len(set) == 0 {
		_, _ = io.WriteString(a.env.Stdout, st.ok("Everything is set up and current.")+"\n\n")
		next, err := k.pickOne("What would you like to do?", "", []choice{
			{"exit", "Leave it as it is"},
			{"change", "Change the setup"},
		}, "exit")
		if err != nil {
			return stop(err)
		}
		if next == "exit" {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return false, nil
		}
	}

	if !set["harness"] {
		var choices []choice
		for _, name := range found {
			if h, ok := a.registry().Get(name); ok {
				choices = append(choices, choice{name, h.Profile().Name})
			}
		}
		picked, err := k.pickMany("Set up which harnesses?", "", choices, found)
		if err != nil {
			return stop(err)
		}
		c.harnesses = nil
		if len(picked) < len(found) {
			c.harnesses = picked
		}
	}
	if !set["scope"] && a.env.Dir != a.env.Getenv("HOME") {
		picked, err := k.pickOne("Install where?", "", []choice{
			{scopeGlobal, "Everywhere: every project you open"},
			{scopeProject, "Only in this project: " + shortPath(a.env.Dir, a.env.Getenv("HOME"))},
		}, scope)
		if err != nil {
			return stop(err)
		}
		c.scope = picked
	}
	if !set["delivery"] {
		picked, err := k.pickOne("Delivery for agents on this machine",
			"When a message wakes an agent's session. aboard delivery sets one agent's own mode.",
			[]choice{
				{string(delivery.ModeAuto), "auto: " + modeText[delivery.ModeAuto]},
				{string(delivery.ModeHumans), "humans: " + modeText[delivery.ModeHumans]},
				{string(delivery.ModeOff), "off: " + modeText[delivery.ModeOff]},
			}, string(current))
		if err != nil {
			return stop(err)
		}
		c.delivery = delivery.Mode(picked)
	}
	if !set["allow-commands"] {
		description, def := a.allowQuestion(*c, found)
		allow, err := k.confirm("Let agents run aboard commands without a permission prompt?", description, def)
		if err != nil {
			return stop(err)
		}
		c.allow = allow
	}
	return true, nil
}

// allowQuestion explains the question about allowing aboard commands, and its default.
// A chosen harness that can't reach Aboard without its rule (Codex, whose sandbox
// blocks network access) says why, and the answer defaults to yes. Otherwise the
// first chosen harness's rule is named, and the default is whether it is there.
func (a *app) allowQuestion(c initChoices, found []string) (description string, def bool) {
	var first harness.Harness
	for _, h := range a.registry() {
		rule, ok := a.item(h, c.scope, harness.ItemAllowRule)
		if !ok || !c.chooses(h.Profile().Harness, found) {
			continue
		}
		if rule.Required != nil {
			return rule.Required.Why, true
		}
		if first == nil {
			first = h
		}
	}
	if first == nil {
		return "", false
	}
	rule, _ := a.item(first, c.scope, harness.ItemAllowRule)
	return "Adds the rule " + rule.Rule + ", which allows the aboard command and nothing else.", a.allowed(first, c.scope)
}

// setupOverview describes what aboard init has already set up on this machine, for each
// harness and scope, and the delivery mode of agents without their own. allCurrent is
// true when every harness found is set up in some scope with this aboard's hooks and
// skill, and, for a harness that can't reach Aboard without it, its allow rule. scope is
// where to suggest installing: where the hooks already are when that is only this
// project, else everywhere.
func (a *app) setupOverview(ctx context.Context, known []harnessSetup, mode delivery.Mode, st styles) (text string, allCurrent bool, scope string) {
	var b strings.Builder
	b.WriteString(st.heading("Aboard setup on this machine") + "\n")
	allCurrent, scope = true, scopeGlobal
	var installedIn []string
	for _, k := range known {
		h, ok := a.registry().Get(k.Name)
		if !ok {
			continue
		}
		label := fmt.Sprintf("  %-13s", h.Profile().Name)
		if !k.Detected {
			b.WriteString(label + st.dim("not found on this machine") + "\n")
			continue
		}
		specs, req := a.currentHooks(ctx, h), requiredAllow(h)
		current, lines := false, 0
		for _, sc := range a.scopes() {
			hooks, sk := a.hooksState(h, sc, specs), a.skillState(h, sc)
			allow := a.allowed(h, sc)
			if hooks.State == stateMissing && sk.State == stateMissing && !allow {
				continue
			}
			if hooks.State != stateMissing {
				installedIn = append(installedIn, sc)
			}
			current = current || (hooks.State == stateCurrent && sk.State == stateCurrent && (req == nil || a.allowedAnywhere(h)))
			parts := []string{stateText(st, "hooks", "them", hooks), stateText(st, "skill", "it", sk)}
			switch {
			case allow:
				parts = append(parts, st.ok("aboard commands allowed"))
			case req != nil:
				parts = append(parts, st.warn(req.Missing))
			}
			if lines > 0 {
				label = strings.Repeat(" ", len(label))
			}
			b.WriteString(label + scopeText(sc) + ": " + strings.Join(parts, ", ") + "\n")
			lines++
		}
		if lines == 0 {
			b.WriteString(label + st.warn("not set up") + "\n")
		}
		allCurrent = allCurrent && current
	}
	fmt.Fprintf(&b, "  %-13s%s for agents without their own mode (%s)\n", "Delivery", st.name(string(mode)), modeText[mode])
	if len(installedIn) > 0 && !slices.Contains(installedIn, scopeGlobal) {
		scope = scopeProject
	}
	return b.String(), allCurrent, scope
}

// stateText says in a few words how an installed file compares with what this aboard
// writes. it is the pronoun for what: "them" for hooks, "it" for the skill.
func stateText(st styles, what, it string, s fileState) string {
	switch s.State {
	case stateCurrent:
		return st.ok(what + " current")
	case stateMissing:
		return st.warn("no " + what)
	case stateEdited:
		return st.warn(what + " edited since aboard " + s.WrittenBy + " wrote " + it)
	}
	if s.WrittenBy != "" {
		return st.warn(what + " outdated (aboard " + s.WrittenBy + " wrote " + it + ")")
	}
	return st.warn(what + " outdated")
}

// nextSteps is what the guided aboard init says once it has made its changes.
func nextSteps(setups []harnessSetup, st styles) string {
	var b strings.Builder
	b.WriteString("\n" + st.heading("Next") + "\n")
	for _, s := range setups {
		if slices.ContainsFunc(s.Changes, func(c fileChange) bool { return c.Kind == "hooks" && c.Action != actionUnchanged }) {
			b.WriteString("  Restart open sessions so they load the hooks.\n")
			break
		}
	}
	b.WriteString("  Pair two sessions: say \"Pair with another agent on Aboard.\" in one, or run " + st.code("aboard pair") + ".\n")
	b.WriteString("  Check the setup at any time with " + st.code("aboard doctor") + ".\n")
	return b.String()
}
