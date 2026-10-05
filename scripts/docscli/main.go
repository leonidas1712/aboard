// Command docscli writes the docs site's CLI reference from the help aboard prints as
// data (aboard help --json, HelpOutput in spec/cli.yaml), read on standard input: one
// page per command in docs/reference/cli, an overview page, and the list of those pages
// in the "CLI reference" group of docs/docs.json. With -check it changes nothing and
// fails if any of them is out of date. Run it from the repository's root, through make
// docs-cli and make docs-check.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

const (
	refDir    = "docs/reference/cli"
	docsJSON  = "docs/docs.json"
	navGroup  = `"group": "CLI reference"`
	pagePath  = "reference/cli/"
	generated = "{/* Written by make docs-cli from aboard help --json. Change the help in server/internal/cli/help.go, then run make docs-cli. */}\n"
)

type command struct {
	Name        string    `json:"name"`
	Group       string    `json:"group"`
	Summary     string    `json:"summary"`
	Usage       []string  `json:"usage"`
	Description string    `json:"description"`
	Flags       []flagDoc `json:"flags"`
	Examples    []example `json:"examples"`
	SeeAlso     []string  `json:"see_also"`
}

type flagDoc struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Text  string `json:"text"`
}

type example struct {
	Command string `json:"command"`
	Text    string `json:"text"`
}

func main() {
	check := flag.Bool("check", false, "fail if the CLI reference is out of date, without changing it")
	flag.Parse()
	if err := run(os.Stdin, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docs-cli:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, check bool) error {
	var help struct {
		Commands []command `json:"commands"`
	}
	if err := json.NewDecoder(in).Decode(&help); err != nil {
		return fmt.Errorf("read aboard help --json from standard input: %w", err)
	}
	if len(help.Commands) == 0 {
		return errors.New("aboard help --json listed no commands")
	}

	want := map[string][]byte{"index.mdx": overview(help.Commands)}
	for _, c := range help.Commands {
		want[c.Name+".mdx"] = page(c, help.Commands)
	}

	var stale []string
	for name, content := range want {
		path := filepath.Join(refDir, name)
		old, err := os.ReadFile(path) //nolint:gosec // a page this program writes
		if err == nil && bytes.Equal(old, content) {
			continue
		}
		stale = append(stale, path)
		if !check {
			if err := os.MkdirAll(refDir, 0o755); err != nil { //nolint:gosec // a docs folder
				return err
			}
			if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // a docs page
				return err
			}
		}
	}
	entries, err := os.ReadDir(refDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if _, ok := want[e.Name()]; ok {
			continue
		}
		path := filepath.Join(refDir, e.Name())
		stale = append(stale, path+" (no such command)")
		if !check {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
	}

	pages := []string{pagePath + "index"}
	for _, c := range help.Commands {
		pages = append(pages, pagePath+c.Name)
	}
	navStale, err := updateNav(pages, check)
	if err != nil {
		return err
	}
	if navStale {
		stale = append(stale, docsJSON+" (the CLI reference group's pages)")
	}

	if check && len(stale) > 0 {
		slices.Sort(stale)
		return fmt.Errorf("the CLI reference is out of date: run make docs-cli\n  %s", strings.Join(stale, "\n  "))
	}
	return nil
}

// overview is the CLI reference's first page: every command by group, as aboard help
// lists them.
func overview(cmds []command) []byte {
	var b bytes.Buffer
	b.WriteString("---\ntitle: \"CLI reference\"\ndescription: \"Every aboard command, by what it is for.\"\n---\n\n")
	b.WriteString(generated + "\n")
	b.WriteString("`aboard` is one binary: the CLI, the local server, the delivery daemon and the board view. " +
		"Each command below has its own page with its usage, flags and examples, the same text `aboard help <command>` prints.\n\n")
	b.WriteString("Every command takes `--json` and then prints one JSON object, errors included. " +
		"The shapes and exit codes are in [spec/cli.yaml](https://github.com/leonidas1712/aboard/blob/main/spec/cli.yaml). " +
		"An error names its code and the next step: `{\"error\":{\"code\",\"message\",\"hint\"}}`.\n")
	group := ""
	for _, c := range cmds {
		if c.Group != group {
			group = c.Group
			fmt.Fprintf(&b, "\n## %s\n\n| Command | What it does |\n| --- | --- |\n", escapeText(group))
		}
		fmt.Fprintf(&b, "| [`aboard %s`](/%s%s) | %s |\n", c.Name, pagePath, c.Name, escapeCell(c.Summary))
	}
	return b.Bytes()
}

// page is one command's reference page.
func page(c command, all []command) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "---\ntitle: %s\ndescription: %s\n---\n\n", quote("aboard "+c.Name), quote(sentence(c.Summary)))
	b.WriteString(generated + "\n")
	b.WriteString("```text\n" + strings.Join(c.Usage, "\n") + "\n```\n\n")
	for _, para := range strings.Split(c.Description, "\n\n") {
		b.WriteString(escapeText(para) + "\n\n")
	}
	if len(c.Flags) > 0 {
		b.WriteString("## Flags\n\n| Flag | What it does |\n| --- | --- |\n")
		for _, f := range c.Flags {
			name := f.Name
			if f.Value != "" {
				name += " " + f.Value
			}
			fmt.Fprintf(&b, "| `%s` | %s |\n", strings.ReplaceAll(name, "|", `\|`), escapeCell(f.Text))
		}
		b.WriteString("\n")
	}
	if len(c.Examples) > 0 {
		b.WriteString("## Examples\n\n```bash\n")
		for i, e := range c.Examples {
			if i > 0 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "# %s\n%s\n", e.Text, e.Command)
		}
		b.WriteString("```\n\n")
	}
	var links []string
	for _, name := range c.SeeAlso {
		if slices.ContainsFunc(all, func(o command) bool { return o.Name == name }) {
			links = append(links, fmt.Sprintf("[`aboard %s`](/%s%s)", name, pagePath, name))
		} else {
			links = append(links, fmt.Sprintf("`aboard %s`", name))
		}
	}
	if len(links) > 0 {
		b.WriteString("## See also\n\n" + strings.Join(links, ", ") + "\n")
	}
	return append(bytes.TrimRight(b.Bytes(), "\n"), '\n')
}

// updateNav makes the pages list of the "CLI reference" group in docs.json the given
// pages, leaving every other byte of the file as it is. It reports whether the list
// differed.
func updateNav(pages []string, check bool) (bool, error) {
	old, err := os.ReadFile(docsJSON)
	if err != nil {
		return false, err
	}
	g := bytes.Index(old, []byte(navGroup))
	if g < 0 {
		return false, fmt.Errorf("%s has no group %s", docsJSON, navGroup)
	}
	p := bytes.Index(old[g:], []byte(`"pages": [`))
	if p < 0 {
		return false, fmt.Errorf("%s: the group %s has no pages list", docsJSON, navGroup)
	}
	start := g + p + len(`"pages": [`)
	end := bytes.IndexByte(old[start:], ']')
	if end < 0 {
		return false, fmt.Errorf("%s: the group %s's pages list doesn't end", docsJSON, navGroup)
	}
	end += start
	// Indent the entries one step deeper than the line that opens the list.
	lineStart := bytes.LastIndexByte(old[:start], '\n') + 1
	line := old[lineStart:start]
	indent := string(line[:len(line)-len(bytes.TrimLeft(line, " "))])
	var list bytes.Buffer
	list.WriteString("\n")
	for i, page := range pages {
		q, _ := json.Marshal(page)
		list.WriteString(indent + "  " + string(q))
		if i < len(pages)-1 {
			list.WriteString(",")
		}
		list.WriteString("\n")
	}
	list.WriteString(indent)
	if bytes.Equal(old[start:end], list.Bytes()) {
		return false, nil
	}
	if check {
		return true, nil
	}
	out := slices.Concat(old[:start], list.Bytes(), old[end:])
	if !json.Valid(out) {
		return false, fmt.Errorf("updating %s would leave it invalid JSON", docsJSON)
	}
	return true, os.WriteFile(docsJSON, out, 0o644) //nolint:gosec // the docs site's config
}

// sentence ends s with a full stop.
func sentence(s string) string {
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

// quote writes s as a YAML double-quoted string, which JSON's quoting also is.
func quote(s string) string {
	q, _ := json.Marshal(s)
	return string(q)
}

// escapeText makes help text safe as MDX prose. Help text is plain, so each character
// that MDX or Markdown would read as syntax there is escaped; the rest stay as they are,
// so the page's Markdown source reads like the help. A flag (--to) becomes code, which
// also keeps the site's typography from turning its "--" into a dash.
func escapeText(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if n := flagAt(rs, i); n > 0 {
			b.WriteString("`" + string(rs[i:i+n]) + "`")
			i += n - 1
			continue
		}
		switch {
		case strings.ContainsRune("\\`*<>{}[]", r):
			b.WriteByte('\\')
		case r == '_' && (i == 0 || i == len(rs)-1 || !isWord(rs[i-1]) || !isWord(rs[i+1])):
			b.WriteByte('\\')
		case r == '#' && (i == 0 || rs[i-1] == '\n'):
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// flagAt returns the length of the flag, such as --wait-reply, that starts at rs[i], or 0.
func flagAt(rs []rune, i int) int {
	if i+2 >= len(rs) || rs[i] != '-' || rs[i+1] != '-' || !unicode.IsLetter(rs[i+2]) || (i > 0 && isWord(rs[i-1])) {
		return 0
	}
	n := 2
	for i+n < len(rs) && (isWord(rs[i+n]) || rs[i+n] == '-') {
		n++
	}
	return n
}

func isWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// escapeCell is escapeText for a table cell, which is one line and ends at a |.
func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(escapeText(s), "\n", " "), "|", `\|`)
}
