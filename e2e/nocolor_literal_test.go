//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestNoColorKeepsArgumentsAfterTheSeparator(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	result := e.runExit("help", "--", "--no-color")
	if result.code != 2 || !strings.Contains(result.stderr, `"--no-color" is not an aboard command`) {
		t.Fatalf("literal argument was swallowed: %s", result)
	}
}
