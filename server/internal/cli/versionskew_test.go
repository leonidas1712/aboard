package cli

import (
	"strings"
	"testing"
)

func TestVersionSkewPolicyAndUpgradeDirection(t *testing.T) {
	for _, tc := range []struct{ client, server, code, fix string }{
		{"0.5.0", "0.4.99", "", ""},
		{"0.5.0", "0.6.0", "", ""},
		{"0.5.0", "0.3.0", "version_skew", "ask the server's admin"},
		{"0.5.0", "0.7.0", "version_skew", "aboard upgrade"},
		{"1.9.0", "2.0.0", "", ""},
		{"2.0.0", "1.9.0", "", ""},
		{"1.0.0", "3.0.0", "version_skew", "aboard upgrade"},
		{"3.0.0", "1.0.0", "version_skew", "ask the server's admin"},
		{"0.99.0", "1.0.0", "", ""},
		{"1.0.0", "0.99.0", "", ""},
		{"0.5.0+dev.abc.dirty", "0.6.0-rc.1", "", ""},
		{"dev", "0.5.0", "version_unknown", "compatibility"},
		{"0.5.0", "0.05.0", "version_unknown", "compatibility"},
		{"0.5.0", "0.-1.0", "version_unknown", "compatibility"},
		{"0.5.0", "0.999999999999999999999999999999.0", "version_unknown", "compatibility"},
		{"0.5.0", "0.5.0-01", "version_unknown", "compatibility"},
		{"0.5.0", "0.5.0+", "version_unknown", "compatibility"},
	} {
		t.Run(tc.client+"/"+tc.server, func(t *testing.T) {
			c := checkVersionSkew("https://team.example.com", tc.client, tc.server)
			if tc.code == "" {
				if c.Level != levelOK || c.Code != nil || c.Fix != nil {
					t.Fatalf("supported pair: %+v", c)
				}
			} else {
				if c.Level != levelWarning || deref(c.Code) != tc.code {
					t.Fatalf("warning: %+v", c)
				}
				if tc.code == "version_unknown" {
					if !strings.Contains(c.Message, "compatibility can't be assessed") {
						t.Fatalf("unknown: %+v", c)
					}
				} else if !strings.Contains(deref(c.Fix), tc.fix) {
					t.Fatalf("upgrade direction: %+v", c)
				}
			}
		})
	}
}
