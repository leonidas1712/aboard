package cli

import (
	"cmp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// version is the release version of the CLI, the local server and the delivery daemon.
// A release build sets it with
// -ldflags "-X github.com/leonidas1712/aboard/server/internal/cli.version=0.2.0".
var version = "0.1.2"

// currentBuild returns this binary's build: its version and, when it was built from a
// Git checkout, the commit and the commit's time.
func currentBuild() delivery.Build {
	b := delivery.Build{Version: version}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return b
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			b.Commit = s.Value
		case "vcs.time":
			if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
				b.CommitTime = t
			}
		}
	}
	return b
}

// infoBuild returns the build a server reports in GET /v1/info.
func infoBuild(info *api.ServerInfo) delivery.Build {
	b := delivery.Build{Version: info.Version}
	if info.Commit != nil {
		b.Commit = *info.Commit
	}
	if info.CommitTime != nil {
		b.CommitTime = *info.CommitTime
	}
	return b
}

// compareBuilds returns -1 when a is older than b, 1 when it is newer, and 0 when they
// count as the same build. Versions decide first; between equal versions a later commit
// time is newer, and a build without a commit time is older than one with it, since it
// predates commit reporting. Two builds without commit times are the same build.
func compareBuilds(a, b delivery.Build) int {
	if c := compareVersions(a.Version, b.Version); c != 0 {
		return c
	}
	if a.CommitTime.IsZero() || b.CommitTime.IsZero() {
		return cmp.Compare(boolInt(!a.CommitTime.IsZero()), boolInt(!b.CommitTime.IsZero()))
	}
	return a.CommitTime.Compare(b.CommitTime)
}

// buildLabel names a build in doctor's text: its version, and its commit when known, so
// two builds of one version can be told apart.
func buildLabel(b delivery.Build) string {
	switch {
	case b.Commit != "":
		return b.Version + " (commit " + b.Commit[:min(12, len(b.Commit))] + ")"
	case b.CommitTime.IsZero():
		return b.Version + " (no commit recorded)"
	}
	return b.Version
}

// semver is a parsed semantic version; ok is false when the text wasn't one.
type semver struct {
	core [3]int
	pre  []string
	ok   bool
}

func parseVersion(s string) semver {
	s = strings.TrimPrefix(s, "v")
	s, _, _ = strings.Cut(s, "+") // build metadata never orders versions
	s, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}
	}
	var v semver
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}
		}
		v.core[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
	}
	v.ok = true
	return v
}

// compareVersions orders two versions by semantic version precedence. Text that isn't
// a version sorts before every version, and equal to other such text.
func compareVersions(a, b string) int {
	va, vb := parseVersion(a), parseVersion(b)
	switch {
	case !va.ok || !vb.ok:
		return cmp.Compare(boolInt(va.ok), boolInt(vb.ok))
	case va.core != vb.core:
		for i := range va.core {
			if c := cmp.Compare(va.core[i], vb.core[i]); c != 0 {
				return c
			}
		}
	}
	// A release is newer than any pre-release of the same version.
	if len(va.pre) == 0 || len(vb.pre) == 0 {
		return cmp.Compare(boolInt(len(va.pre) == 0), boolInt(len(vb.pre) == 0))
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		na, errA := strconv.Atoi(va.pre[i])
		nb, errB := strconv.Atoi(vb.pre[i])
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil: // numeric identifiers sort before alphanumeric ones
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(va.pre[i], vb.pre[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(va.pre), len(vb.pre))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
