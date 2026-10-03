// Package registry lists the harnesses Aboard sets up and delivers to. It is the one
// place in the code that names them: init, doctor, status, uninstall, the hooks,
// session detection and the delivery daemon loop over this list.
package registry

import (
	"github.com/leonidas1712/aboard/server/internal/harness"
	"github.com/leonidas1712/aboard/server/internal/harness/claudecode"
	"github.com/leonidas1712/aboard/server/internal/harness/codex"
	"github.com/leonidas1712/aboard/server/internal/harness/omp"
)

// Harnesses returns every harness, in the order aboard init lists and sets them up.
func Harnesses() harness.Set {
	return harness.Set{claudecode.New(), codex.New(), omp.New()}
}
