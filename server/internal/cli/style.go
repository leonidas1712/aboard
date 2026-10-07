package cli

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// People at a terminal get color and structure in text output; everything else (a
// pipe, a file, an agent's session, --json) gets the same plain text, byte for byte. A
// style only wraps single-line fragments in color codes, so the words and layout never
// depend on where the output goes.

// styles colors text fragments, or leaves them as they are when on is false.
type styles struct{ on bool }

var (
	styleHeading = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Green)
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	styleBad     = lipgloss.NewStyle().Foreground(lipgloss.Red)
	styleBadBold = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	styleName    = lipgloss.NewStyle().Foreground(lipgloss.Magenta).Bold(true)
	styleCode    = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
)

func (s styles) render(st lipgloss.Style, text string) string {
	if !s.on || text == "" {
		return text
	}
	return st.Inline(true).Render(text)
}

// heading styles a title or a section label.
func (s styles) heading(text string) string { return s.render(styleHeading, text) }

// dim styles secondary text, such as a path or an explanation.
func (s styles) dim(text string) string { return s.render(styleDim, text) }

// ok styles something that worked or is current.
func (s styles) ok(text string) string { return s.render(styleOK, text) }

// warn styles something that needs a look but isn't broken.
func (s styles) warn(text string) string { return s.render(styleWarn, text) }

// bad styles something that failed or will be removed.
func (s styles) bad(text string) string { return s.render(styleBad, text) }

// name styles a board's or an agent's name.
func (s styles) name(text string) string { return s.render(styleName, text) }

// code styles a command or a line to copy.
func (s styles) code(text string) string { return s.render(styleCode, text) }

// styleStatus colors the text of aboard status once it is written: each line's label,
// whether the server and the daemon run, and the board's and agent's names. Styling
// the finished text keeps the wording in one place.
func styleStatus(text string, st styles) string {
	if !st.on {
		return text
	}
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		label, rest, ok := strings.Cut(line, ":")
		if !ok || strings.Contains(label, " ") || label == "" {
			continue
		}
		pad := rest[:len(rest)-len(strings.TrimLeft(rest, " "))]
		rest = rest[len(pad):]
		switch label {
		case "Server", "Daemon":
			for _, w := range []struct {
				word string
				fn   func(string) string
			}{{"not running", st.warn}, {"unreachable", st.warn}, {"can't be reached", st.bad}, {"running", st.ok}, {"reachable", st.ok}} {
				if j := strings.Index(rest, w.word); j >= 0 {
					rest = rest[:j] + w.fn(w.word) + rest[j+len(w.word):]
					break
				}
			}
		case "Board", "Agent":
			if name, after, ok := strings.Cut(rest, " "); ok && strings.TrimRight(name, ";") != "none" {
				rest = st.name(name) + " " + after
			}
		}
		lines[i] = st.heading(label+":") + pad + rest
	}
	return strings.Join(lines, "")
}

// table lays out rows in aligned columns under a bold header row, indented two spaces.
// Cells are padded before they are styled, so color never shifts a column. style may be
// nil; it gets the column, the cell's text and the whole row, and returns the styled
// text. Trailing spaces are dropped.
func (s styles) table(header []string, rows [][]string, style func(col int, text string, row []string) string) string {
	all := append([][]string{header}, rows...)
	var widths []int
	for _, r := range all {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	var b strings.Builder
	for n, r := range all {
		line := ""
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c))
			switch {
			case n == 0:
				c = s.heading(c)
			case style != nil && c != "":
				c = style(i, c, r)
			}
			if i < len(r)-1 {
				c += pad + "  "
			}
			line += c
		}
		b.WriteString("  " + strings.TrimRight(line, " ") + "\n")
	}
	return b.String()
}

// colorOn reports whether output to a stream may be colored: the stream is a terminal,
// NO_COLOR is unset, the terminal isn't dumb, --json wasn't given, and the command isn't
// running inside a harness session, where an agent reads the output.
func (a *app) colorOn(terminal bool) bool {
	if !terminal || a.json || a.noColor || a.env.Getenv("NO_COLOR") != "" || a.env.Getenv("TERM") == "dumb" {
		return false
	}
	_, in := a.inSession()
	return !in
}

// out returns the styles for standard output.
func (a *app) out() styles { return styles{on: a.colorOn(a.env.StdoutTerminal)} }

// errStyles returns the styles for standard error.
func (a *app) errStyles() styles { return styles{on: a.colorOn(a.env.StderrTerminal)} }

// interactive reports whether a command may ask questions: standard input and output
// are a terminal, --json wasn't given, and the command isn't running inside a harness
// session, where an agent would have to answer. Every question has a flag that answers
// it, so an agent can make the same choices.
func (a *app) interactive() bool {
	if !a.env.Terminal || a.json {
		return false
	}
	_, in := a.inSession()
	return !in
}
