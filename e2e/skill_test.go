//go:build e2e

package e2e

import (
	"os"
	"testing"

	bundled "github.com/leonidas1712/aboard/skills/aboard"
)

func TestSkillMatchesTheBinaryWithoutSetup(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, vars := range [][]string{nil, {"ABOARD_SESSION=cloud-session", "CLAUDECODE=1"}, {"ABOARD_SESSION=cloud-session", "CLAUDECODE=1", "ABOARD_SUBAGENT=worker"}} {
		plain := e.exec(vars, "", "skill")
		if plain.code != 0 || plain.stdout != string(bundled.Skill) || plain.stderr != "" {
			t.Fatalf("bundled skill: %s", plain)
		}
		encoded := e.exec(vars, "", "skill", "--json")
		if encoded.code != 0 {
			t.Fatalf("JSON skill: %s", encoded)
		}
		result := encoded.json(t)
		matchesCLISpec(t, "SkillOutput", result)
		if field(t, result, "skill") != plain.stdout || field(t, result, "version") != field(t, e.run("version", "--json").json(t), "version") {
			t.Fatal("skill and binary version differ")
		}
	}
	if _, err := os.Stat(e.aboardHome()); !os.IsNotExist(err) {
		t.Fatalf("skill created or read setup state: %v", err)
	}
}
