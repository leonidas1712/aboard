package ids

import (
	"bytes"
	"regexp"
	"testing"
	"time"
)

func TestIDsSortByCreationTime(t *testing.T) {
	g := New(bytes.NewReader(bytes.Repeat([]byte{0xff}, 64)))
	early, err := g.ID("msg", time.UnixMilli(1_000))
	if err != nil {
		t.Fatal(err)
	}
	late, err := g.ID("msg", time.UnixMilli(2_000))
	if err != nil {
		t.Fatal(err)
	}
	if early >= late {
		t.Fatalf("%s should sort before %s", early, late)
	}
	if !regexp.MustCompile(`^msg_[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(early) {
		t.Fatalf("bad id %q", early)
	}
}

func TestULIDOfZeroIsAllZeros(t *testing.T) {
	if got := encodeULID([16]byte{}); got != "00000000000000000000000000" {
		t.Fatalf("got %s", got)
	}
}

func TestNormalizeJoinCode(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"7Q4-K2M", "7Q4-K2M", true},
		{"7q4k2m", "7Q4-K2M", true},
		{" 7Q4 K2M ", "7Q4-K2M", true},
		{"7O4-K2L", "704-K21", true},
		{"7Q4-K2", "", false},
		{"7Q4-K2U", "", false},
	}
	for _, tt := range tests {
		got, ok := NormalizeJoinCode(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("NormalizeJoinCode(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestJoinCodesAreValidCodes(t *testing.T) {
	g := New(bytes.NewReader(bytes.Repeat([]byte{0, 31, 200, 7, 99, 255}, 4)))
	code, err := g.JoinCode()
	if err != nil {
		t.Fatal(err)
	}
	if norm, ok := NormalizeJoinCode(code); !ok || norm != code {
		t.Fatalf("generated code %q does not normalize to itself", code)
	}
}
