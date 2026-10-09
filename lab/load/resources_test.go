package main

import (
	"math"
	"testing"
	"time"
)

func TestResourceAccountingParsesMacAndLinuxCPUTime(t *testing.T) {
	for _, row := range []struct {
		text string
		cpu  float64
		rss  int64
	}{
		{"0:01.25 1024", 1.25, 1024 * 1024},
		{"01:02:03 12", 3723, 12 * 1024},
		{"2-01:02:03 12", 2*86400 + 3723, 12 * 1024},
	} {
		cpu, rss, err := parseResources(row.text)
		if err != nil || math.Abs(cpu-row.cpu) > 0.001 || rss != row.rss {
			t.Fatalf("%q: CPU%v RSS%d error%v", row.text, cpu, rss, err)
		}
	}
	for _, invalid := range []string{"", "00:00 -1", "nan:00 42", "00:inf 42", "00:00 9223372036854775807"} {
		if _, _, err := parseResources(invalid); err == nil {
			t.Fatalf("accepted invalid accounting %q", invalid)
		}
	}
}

func TestSoakKeepsMinimumRoundsAndStopsAfterItsDuration(t *testing.T) {
	if !moreRounds(1, 2, time.Hour, time.Second) {
		t.Fatal("soak skipped the minimum rounds")
	}
	if !moreRounds(2, 2, time.Second, time.Minute) {
		t.Fatal("soak stopped before its requested duration")
	}
	if moreRounds(2, 2, time.Minute, time.Minute) {
		t.Fatal("soak did not stop at its boundary")
	}
	if moreRounds(2, 2, 0, 0) {
		t.Fatal("disabled soak added a round")
	}
}
