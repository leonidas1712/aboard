package registry

import "testing"

// The contract keeps config_dir optional (spec/README.md: a contract only grows), but
// every bundled profile must declare it, so a development sandbox (scripts/sandbox) can
// point each harness at a folder of its own. scripts/sandbox refuses a profile without
// one; this fails the conformance kit first.
func TestEveryBundledProfileDeclaresConfigDir(t *testing.T) {
	for _, h := range Harnesses() {
		p := h.Profile()
		if p.ConfigDir.Env == "" || p.ConfigDir.Default == "" {
			t.Errorf("the %s profile has no config_dir: add config_dir with env (the variable the harness reads its config folder from) and default (the folder under the home directory), so make sandbox can isolate it", p.Harness)
		}
	}
}
