package cli

import (
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestCompareBuilds(t *testing.T) {
	early := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	tests := []struct {
		name string
		a, b delivery.Build
		want int
	}{
		{"patch", delivery.Build{Version: "0.1.0"}, delivery.Build{Version: "0.1.1"}, -1},
		{"numeric, not text", delivery.Build{Version: "0.10.0"}, delivery.Build{Version: "0.9.0"}, 1},
		{"leading v", delivery.Build{Version: "v0.2.0"}, delivery.Build{Version: "0.2.0"}, 0},
		{"pre-release before release", delivery.Build{Version: "0.2.0-rc.1"}, delivery.Build{Version: "0.2.0"}, -1},
		{"pre-release numbers", delivery.Build{Version: "0.2.0-rc.2"}, delivery.Build{Version: "0.2.0-rc.10"}, -1},
		{"build metadata ignored", delivery.Build{Version: "0.2.0+abc"}, delivery.Build{Version: "0.2.0"}, 0},
		{"missing version is oldest", delivery.Build{}, delivery.Build{Version: "0.0.1"}, -1},
		{"unreadable version is oldest", delivery.Build{Version: "dev"}, delivery.Build{Version: "0.0.1"}, -1},
		{"two unreadable versions are equal", delivery.Build{Version: "dev"}, delivery.Build{}, 0},
		{"same version, later commit", delivery.Build{Version: "0.1.0", CommitTime: late}, delivery.Build{Version: "0.1.0", CommitTime: early}, 1},
		{"same version, no commit time is older", delivery.Build{Version: "0.1.0"}, delivery.Build{Version: "0.1.0", CommitTime: late}, -1},
		{"same version, neither has a commit time", delivery.Build{Version: "0.1.0", Commit: "abc"}, delivery.Build{Version: "0.1.0"}, 0},
		{"dev build, later commit than the installed release", delivery.Build{Version: "0.1.0+dev.1d0e798ab12c", CommitTime: late}, delivery.Build{Version: "0.1.0", CommitTime: early}, 1},
		{"dev build of the installed release's commit", delivery.Build{Version: "0.1.0+dev.1d0e798ab12c.dirty", CommitTime: late}, delivery.Build{Version: "0.1.0", CommitTime: late}, 0},
		{"version wins over commit time", delivery.Build{Version: "0.1.0", CommitTime: late}, delivery.Build{Version: "0.1.1", CommitTime: early}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareBuilds(tt.a, tt.b); got != tt.want {
				t.Fatalf("compareBuilds(%+v, %+v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
			if got := compareBuilds(tt.b, tt.a); got != -tt.want {
				t.Fatalf("compareBuilds(%+v, %+v) = %d, want %d", tt.b, tt.a, got, -tt.want)
			}
		})
	}
}

// Doctor and init name the aboard that wrote an installed file and this one. Two builds
// of the same version would read as "written by aboard 0.1.0 and differs from the one
// aboard 0.1.0 installs", so then each is named with its commit.
func TestBuildNamesTellTwoBuildsOfOneVersionApart(t *testing.T) {
	this := delivery.Build{Version: "0.1.0", Commit: "3f9a0c1e2b4d5e6f"}
	tests := []struct {
		name          string
		written       installRecord
		wantBy, wantN string
	}{
		{"older version", installRecord{Version: "0.0.9", Commit: "1d0e798ab12c"}, "0.0.9", "0.1.0"},
		{"same build", installRecord{Version: "0.1.0", Commit: "3f9a0c1e2b4d5e6f"}, "0.1.0", "0.1.0"},
		{"same version, another commit", installRecord{Version: "0.1.0", Commit: "1d0e798ab12c99"}, "0.1.0+dev.1d0e798ab12c", "0.1.0+dev.3f9a0c1e2b4d"},
		{"same version, written before commits were recorded", installRecord{Version: "0.1.0"}, "0.1.0", "0.1.0+dev.3f9a0c1e2b4d"},
	}
	for _, tt := range tests {
		by, now := buildNames(tt.written, this)
		if by != tt.wantBy || now != tt.wantN {
			t.Errorf("%s: written by %q, this aboard %q; want %q and %q", tt.name, by, now, tt.wantBy, tt.wantN)
		}
	}
	// A dev build's version already names its commit.
	by, now := buildNames(installRecord{Version: "0.1.0+dev.1d0e798ab12c", Commit: "1d0e798ab12c"},
		delivery.Build{Version: "0.1.0+dev.1d0e798ab12c", Commit: "3f9a"})
	if by != "0.1.0+dev.1d0e798ab12c" || now != "0.1.0+dev.1d0e798ab12c" {
		t.Errorf("dev builds: %q and %q", by, now)
	}
}
