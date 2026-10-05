// Command moderules writes the board view's copy of the delivery modes' rules, from
// deliverytext.ModeRule, so the web UI, the CLI and the hooks say the same thing about
// each mode. go generate runs it (see ../generate.go); make generate-check fails when
// the copy is out of date.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// modes are the delivery modes a person sets, in the order the board view lists them.
var modes = []string{"focused", "all", "humans", "off"}

func main() {
	out := flag.String("o", "", "the TypeScript file to write")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "moderules: -o is required")
		os.Exit(2)
	}
	var b strings.Builder
	b.WriteString("// Written by go generate from server/internal/deliverytext (deliverytext.ModeRule); don't edit.\n")
	b.WriteString("// What each delivery mode means for when an agent wakes, in the words the CLI, the delivery\n")
	b.WriteString("// daemon and the hooks tell the agent.\n\n")
	b.WriteString("/** SettableMode is a delivery mode a person sets for their agent. */\n")
	b.WriteString("export type SettableMode = " + quoted(modes, " | ") + ";\n\n")
	b.WriteString("/** settableModes lists the modes in the order the board view offers them. */\n")
	b.WriteString("export const settableModes: SettableMode[] = [" + quoted(modes, ", ") + "];\n\n")
	b.WriteString("/** modeRules is each mode's rule, as the agent is told it. */\n")
	b.WriteString("export const modeRules: Record<SettableMode, string> = {\n")
	for _, m := range modes {
		rule, err := json.Marshal(deliverytext.ModeRule(m))
		if err != nil {
			fmt.Fprintln(os.Stderr, "moderules:", err)
			os.Exit(1)
		}
		fmt.Fprintf(&b, "  %s: %s,\n", m, rule)
	}
	b.WriteString("};\n")
	if err := os.WriteFile(*out, []byte(b.String()), 0o644); err != nil { //nolint:gosec // a source file in the repository
		fmt.Fprintln(os.Stderr, "moderules:", err)
		os.Exit(1)
	}
}

// quoted writes each mode as a TypeScript string, joined by sep.
func quoted(ms []string, sep string) string {
	q := make([]string, len(ms))
	for i, m := range ms {
		q[i] = `"` + m + `"`
	}
	return strings.Join(q, sep)
}
