package boardfile

import (
	"errors"
	"testing"
)

func TestEveryTemplateParses(t *testing.T) {
	names := TemplateNames()
	if len(names) == 0 {
		t.Fatal("no templates embedded")
	}
	for _, n := range names {
		f, err := Template(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if f.Charter == "" {
			t.Errorf("%s: empty charter", n)
		}
	}
}

func TestWriterReviewerPairsWriterWithReviewer(t *testing.T) {
	f, err := Template("writer-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Pair) != 2 || f.Pair[0] != "writer" || f.Pair[1] != "reviewer" {
		t.Fatalf("pair = %v", f.Pair)
	}
	if f.Policy.Preset != "starter" {
		t.Fatalf("preset = %q", f.Policy.Preset)
	}
}

func TestParseRejectsMistakes(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown key":        "charter: x\nmonitr: {}\n",
		"unknown permission": "roles: {w: {can: [fly]}}\n",
		"pair of undefined":  "roles: {w: {can: [post]}}\npair: [w, r]\n",
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Template("nope"); !errors.Is(err, ErrNoTemplate) {
		t.Errorf("missing template: %v", err)
	}
}
