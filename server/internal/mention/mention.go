// Package mention finds the mentions in a message body: `@name` and `@role:R` written
// in text, outside code and links. It only reads text; which members a mention names is
// decided by the board, against its members when the message is posted.
package mention

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Ref is one mention as written: Text is what the body says ("@codex" or
// "@role:reviewer"), and exactly one of Name and Role is set.
type Ref struct {
	Text string
	Name string
	Role string
}

// Longest member and role names, as the API's patterns allow them.
const (
	maxName = 40
	maxRole = 32
)

// url matches a link with a scheme, up to the next whitespace.
var url = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://\S*`)

// Find returns the mentions in body, in the order written, repeats included. Code
// (fenced blocks and inline code spans) and links are skipped; an "@" is a mention only
// where a word starts, so an email address or a "\@" isn't one.
func Find(body string) []Ref {
	skip := skipped(body)
	var out []Ref
	for i := 0; i < len(body); i++ {
		if body[i] != '@' || skip[i] || !startsWord(body[:i]) {
			continue
		}
		rest := body[i+1:]
		if r, ok := strings.CutPrefix(rest, "role:"); ok {
			if role := word(r); validRole(role) {
				out = append(out, Ref{Text: "@role:" + role, Role: role})
				i += len("@role:") + len(role) - 1
				continue
			}
		}
		if name := word(rest); validName(name) {
			out = append(out, Ref{Text: "@" + name, Name: name})
			i += len(name)
		}
	}
	return out
}

// startsWord reports whether an "@" after before starts a word: it is first, or follows
// a character that can't be part of an address, a path or an escape.
func startsWord(before string) bool {
	r, size := utf8.DecodeLastRuneInString(before)
	if size == 0 {
		return true
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(`_.-@/\`, r)
}

// word returns the run of name characters that s starts with, or "" when that run is
// followed by a character that would make it part of a longer word.
func word(s string) string {
	n := 0
	for n < len(s) && (s[n] >= 'a' && s[n] <= 'z' || s[n] >= '0' && s[n] <= '9' || s[n] == '-') {
		n++
	}
	if n < len(s) {
		if r, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			return ""
		}
	}
	return s[:n]
}

func validName(s string) bool {
	return s != "" && len(s) <= maxName && s[0] != '-'
}

func validRole(s string) bool {
	return s != "" && len(s) <= maxRole && s[0] >= 'a' && s[0] <= 'z'
}

// skipped marks every byte of body inside code or a link.
func skipped(body string) []bool {
	skip := make([]bool, len(body))
	mark := func(from, to int) {
		for i := from; i < to; i++ {
			skip[i] = true
		}
	}
	// Fenced code blocks, line by line; the text between them is checked for inline code.
	var (
		fence     string // the open fence's run, such as "```", or "" outside a block
		openAt    int
		textStart int
	)
	for start := 0; start < len(body); {
		end := strings.IndexByte(body[start:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += start + 1
		}
		line := body[start:end]
		switch {
		case fence == "":
			if run := fenceRun(line); run != "" {
				markCode(body, textStart, start, mark)
				fence, openAt = run, start
			}
		case closes(line, fence):
			mark(openAt, end)
			fence, textStart = "", end
		}
		start = end
	}
	if fence != "" {
		mark(openAt, len(body))
	} else {
		markCode(body, textStart, len(body), mark)
	}
	for _, loc := range url.FindAllStringIndex(body, -1) {
		mark(loc[0], loc[1])
	}
	return skip
}

// fenceRun returns the backticks or tildes a line opens a fenced code block with: up to
// three spaces, then three or more of one of them. "" when it opens none.
func fenceRun(line string) string {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" || (trimmed[0] != '`' && trimmed[0] != '~') {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}

// closes reports whether line closes a block opened with fence: up to three spaces, at
// least as many of the same character, then only whitespace.
func closes(line, fence string) bool {
	run := fenceRun(line)
	if run == "" || run[0] != fence[0] || len(run) < len(fence) {
		return false
	}
	rest := strings.TrimLeft(line, " ")[len(run):]
	return strings.TrimSpace(rest) == ""
}

// markCode marks the inline code spans in body[from:to]: a run of backticks to the next
// run of the same length. A run with no match is plain text.
func markCode(body string, from, to int, mark func(from, to int)) {
	for i := from; i < to; {
		if body[i] != '`' {
			i++
			continue
		}
		n := run(body, i, to)
		end := -1
		for j := i + n; j < to; {
			if body[j] != '`' {
				j++
				continue
			}
			m := run(body, j, to)
			if m == n {
				end = j + m
				break
			}
			j += m
		}
		if end < 0 {
			i += n
			continue
		}
		mark(i, end)
		i = end
	}
}

// run counts the backticks at body[i:], up to to.
func run(body string, i, to int) int {
	n := 0
	for i+n < to && body[i+n] == '`' {
		n++
	}
	return n
}
