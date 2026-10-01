package joinline

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Line
	}{
		{
			"exact", "Join Aboard board docs-review on localhost as reviewer with code 7Q4-K2M",
			Line{"docs-review", "localhost", "reviewer", "7Q4-K2M"},
		},
		{
			"port", "Join Aboard board docs on localhost:7411 as reviewer with code 7Q4-K2M",
			Line{"docs", "localhost:7411", "reviewer", "7Q4-K2M"},
		},
		{
			"domain and surrounding prose", "Please run this: Join Aboard board docs on aboard.example.com as reviewer with code 7Q4-K2M.",
			Line{"docs", "aboard.example.com", "reviewer", "7Q4-K2M"},
		},
		{
			"lowercase, no dash", "join aboard board docs on localhost as reviewer with code 7q4k2m",
			Line{"docs", "localhost", "reviewer", "7Q4-K2M"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{
		"Join Aboard board docs on localhost as reviewer",
		"join the docs board please",
		"Join Aboard board docs on localhost as reviewer with code 7Q4-K2U",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("accepted %q", in)
		}
	}
}

func TestStringRoundTrips(t *testing.T) {
	l := Line{"writer-reviewer", "localhost", "reviewer", "7Q4-K2M"}
	got, err := Parse(l.String())
	if err != nil || got != l {
		t.Fatalf("got %+v, %v", got, err)
	}
}
