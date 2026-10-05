package mention

import (
	"reflect"
	"strings"
	"testing"
)

func TestFind(t *testing.T) {
	name := func(n string) Ref { return Ref{Text: "@" + n, Name: n} }
	role := func(r string) Ref { return Ref{Text: "@role:" + r, Role: r} }
	tests := []struct {
		name string
		body string
		want []Ref
	}{
		{"one name", "@codex can you look?", []Ref{name("codex")}},
		{"in a sentence", "Thanks, @codex. And (@omp)!", []Ref{name("codex"), name("omp")}},
		{"name with digits and dashes", "ping @claude-2", []Ref{name("claude-2")}},
		{"possessive", "@codex's branch", []Ref{name("codex")}},
		{"after a colon", "cc:@codex", []Ref{name("codex")}},
		{"repeated", "@codex then @codex", []Ref{name("codex"), name("codex")}},
		{"role", "@role:reviewer please check", []Ref{role("reviewer")}},
		{"role then punctuation", "Over to @role:reviewer.", []Ref{role("reviewer")}},
		{"role prefix without a role is a name", "@role: is a word", []Ref{name("role")}},
		{"email", "mail maya@example.com", nil},
		{"after a dot", "x.@codex", nil},
		{"after an underscore", "a_@codex", nil},
		{"after a dash", "re-@codex", nil},
		{"double at", "@@codex", nil},
		{"escaped", `\@codex isn't a mention`, nil},
		{"escaped then a real one", `\@codex but @omp`, []Ref{name("omp")}},
		{"url path", "see https://example.com/@codex today", nil},
		{"url query", "see https://example.com/?u=@codex today", nil},
		{"after a url", "https://example.com @codex", []Ref{name("codex")}},
		{"path without a scheme", "example.com/@codex", nil},
		{"uppercase is not a name", "@Codex", nil},
		{"uppercase after the name", "@codexA", nil},
		{"underscore after the name", "@codex_x", nil},
		{"too long", "@" + strings.Repeat("a", 41), nil},
		{"longest name", "@" + strings.Repeat("a", 40), []Ref{name(strings.Repeat("a", 40))}},
		{"role too long", "@role:" + strings.Repeat("r", 33), []Ref{name("role")}},
		{"role starting with a digit", "@role:2x", []Ref{name("role")}},
		{"name starting with a dash", "@-codex", nil},
		{"lone at", "meet @ noon", nil},
		{"inline code", "run `aboard say --to @codex` now", nil},
		{"inline code then a mention", "`@codex` and @omp", []Ref{name("omp")}},
		{"double backtick code", "``a ` @codex`` and @omp", []Ref{name("omp")}},
		{"unmatched backtick is text", "a ` @codex", []Ref{name("codex")}},
		{"fenced block", "Look:\n```\n@codex\n```\n@omp", []Ref{name("omp")}},
		{"tilde fence", "~~~go\n@codex\n~~~\n@omp", []Ref{name("omp")}},
		{"indented fence", "   ```\n@codex\n   ```\n@omp", []Ref{name("omp")}},
		{"longer closing fence", "```\n@codex\n`````\n@omp", []Ref{name("omp")}},
		{"shorter fence doesn't close", "````\n```\n@codex\n````\n@omp", []Ref{name("omp")}},
		{"unclosed fence runs to the end", "```\n@codex\n@omp", nil},
		{"four spaces aren't a fence", "    ```\n@codex", []Ref{name("codex")}},
		{"mention after a fence's text", "```\ncode\n```\nthanks @codex", []Ref{name("codex")}},
		{"multibyte before", "é@codex", nil},
		{"emoji before", "👀@codex", []Ref{name("codex")}},
		{"start of a line", "first\n@codex second", []Ref{name("codex")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Find(tt.body); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Find(%q)\n got  %+v\n want %+v", tt.body, got, tt.want)
			}
		})
	}
}
