package cli

import (
	"flag"
	"io"
	"slices"
	"testing"
)

func TestParseInterspersedAcceptsFlagsAnywhere(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantPos []string
		wantTo  []string
		wantAs  string
		wantErr bool
	}{
		{"flags first", []string{"--to", "@x", "hi"}, []string{"hi"}, []string{"@x"}, "", false},
		{"flags last", []string{"hi", "--to", "@x"}, []string{"hi"}, []string{"@x"}, "", false},
		{"flags between words", []string{"hello", "--as", "w", "there"}, []string{"hello", "there"}, nil, "w", false},
		{"equals form", []string{"hi", "--as=w"}, []string{"hi"}, nil, "w", false},
		{"comma list", []string{"--to", "@a,role:b", "hi"}, []string{"hi"}, []string{"@a", "role:b"}, "", false},
		{"repeated list", []string{"--to", "@a", "hi", "--to", "@b"}, []string{"hi"}, []string{"@a", "@b"}, "", false},
		{"double dash ends flags", []string{"--as", "w", "--", "--to", "@x"}, []string{"--to", "@x"}, nil, "w", false},
		{"no arguments", nil, nil, nil, "", false},
		{"unknown flag", []string{"hi", "--nope"}, nil, nil, "", true},
		{"missing value", []string{"hi", "--as"}, nil, nil, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			var to listFlag
			fs.Var(&to, "to", "")
			as := fs.String("as", "", "")
			pos, err := parseInterspersed(fs, tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !slices.Equal(pos, tt.wantPos) {
				t.Errorf("positional = %q, want %q", pos, tt.wantPos)
			}
			if !slices.Equal([]string(to), tt.wantTo) {
				t.Errorf("--to = %q, want %q", to, tt.wantTo)
			}
			if *as != tt.wantAs {
				t.Errorf("--as = %q, want %q", *as, tt.wantAs)
			}
		})
	}
}

func TestWantsJSONStopsAtDoubleDash(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"say", "hi", "--json"}, true},
		{[]string{"say", "--json=true", "hi"}, true},
		{[]string{"say", "--", "--json"}, false},
		{[]string{"say", "hi"}, false},
	}
	for _, tt := range tests {
		if got := wantsJSON(tt.args); got != tt.want {
			t.Errorf("wantsJSON(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}
