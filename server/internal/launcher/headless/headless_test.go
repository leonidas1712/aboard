package headless_test

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/launcher/headless"
	"github.com/leonidas1712/aboard/server/internal/launcher/launchertest"
)

// The headless launcher passes the launcher kit.
func TestHeadlessLauncherPassesTheKit(t *testing.T) {
	launchertest.Run(t, headless.Launcher{Dir: t.TempDir()})
}
