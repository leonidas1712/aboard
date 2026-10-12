// Package export renders caller-visible board content as Markdown and zip entries.
// It receives already authorized read models and never changes the board.
package export

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// Document holds only the content selected by ordinary board reads.
type Document struct {
	Board      board.Board
	Server, By string
	At         time.Time
	BriefName  string
	Brief      []byte
	Tasks      []board.Task
	Messages   []board.Message
	Files      []board.File
}

// text escapes a short label (a name, title or path) so it can never become HTML.
var text = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

func title(s *string) string {
	if s == nil {
		return ""
	}
	return text(*s)
}

func member(m board.Member) string { return "@" + text(m.Name) }

// source shows text as escaped source in a fenced block, for a brief that is not Markdown.
func source(s string) string {
	fence := "```"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence + "\n" + html.EscapeString(strings.TrimRight(s, "\n")) + "\n" + fence + "\n"
}

// prose renders Markdown someone wrote as Markdown in the document. Raw HTML is escaped
// so it shows as text, headings move down depth levels so they sit under the document's
// own, and a code fence left open is closed so it can't swallow what follows.
func prose(s string, depth int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "<", "&lt;")
	var out strings.Builder
	fence := ""
	for _, line := range strings.Split(strings.Trim(s, "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		switch {
		case fence != "":
			if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == "" {
				fence = ""
			}
		case strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~"):
			fence = trimmed[:3]
			for strings.HasPrefix(trimmed[len(fence):], fence[:1]) {
				fence += fence[:1]
			}
		case strings.HasPrefix(line, "#"):
			level := len(line) - len(strings.TrimLeft(line, "#"))
			if level <= 6 && (level == len(line) || line[level] == ' ') {
				line = strings.Repeat("#", min(level+depth, 6)) + line[level:]
			}
		}
		out.WriteString(line + "\n")
	}
	if fence != "" {
		out.WriteString(fence + "\n")
	}
	return out.String()
}

// when formats a recorded time as a person reads it: to the minute, in UTC.
func when(at string) string {
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return text(at)
	}
	return stamp(t)
}

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// recipients says who a message went to, in the board view's words.
func recipients(to []string) string {
	if len(to) == 0 {
		return "everyone"
	}
	words := make([]string, 0, len(to))
	for _, t := range to {
		switch kind, v, _ := rules.ParseTarget(t); kind {
		case rules.TargetAll:
			words = append(words, "everyone")
		case rules.TargetName:
			words = append(words, "@"+text(v))
		case rules.TargetOwner:
			words = append(words, text(v)+"'s agents")
		case rules.TargetRole:
			words = append(words, "role "+text(v))
		default:
			words = append(words, text(t))
		}
	}
	return strings.Join(words, ", ")
}

// words turns a stored state such as in_progress or went_with into words.
func words(state string) string {
	if state == "went_with" {
		return "went with the default"
	}
	return text(strings.ReplaceAll(state, "_", " "))
}

const none = "None.\n\n"

// Markdown renders the complete document with fixed section ordering.
func (d Document) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n", text(d.Board.Name))
	if t := title(d.Board.Title); t != "" {
		out.WriteString(t + "\n\n")
	}
	fmt.Fprintf(&out, "This export holds what %s can read on %s, as of %s, from %s.\n\n## Brief\n\n", text(d.By), text(d.Board.Name), stamp(d.At), text(d.Server))
	switch {
	case d.BriefName == "":
		out.WriteString("No brief.\n\n")
	case strings.HasSuffix(strings.ToLower(d.BriefName), ".md"):
		out.WriteString(prose(string(d.Brief), 2) + "\n")
	default:
		out.WriteString(source(string(d.Brief)) + "\n")
	}
	out.WriteString(d.TaskMarkdown() + "## Asks\n\n")
	asks := 0
	for _, m := range d.Messages {
		if m.Ask == nil {
			continue
		}
		asks++
		fmt.Fprintf(&out, "### #%d · @%s asks · %s\n\n%s\n", m.Seq, text(m.SenderName), when(m.At), prose(m.Body, 3))
		for i, option := range m.Ask.Options {
			fmt.Fprintf(&out, "%d. %s\n", i+1, text(option))
		}
		if len(m.Ask.Options) > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "State: %s.\n\n", words(m.Ask.State))
		for _, answer := range d.Messages {
			if answer.Answer == nil || answer.Answer.AskID != m.ID {
				continue
			}
			fmt.Fprintf(&out, "Answer #%d by @%s", answer.Seq, text(answer.SenderName))
			if o := answer.Answer.Option; o != nil {
				fmt.Fprintf(&out, ", chose option %d", *o)
				if *o >= 1 && *o <= len(m.Ask.Options) {
					fmt.Fprintf(&out, " (%s)", text(m.Ask.Options[*o-1]))
				}
			}
			if answer.Answer.Withdrawn {
				out.WriteString(", withdrawn")
			}
			fmt.Fprintf(&out, ":\n\n%s\n", prose(answer.Body, 3))
		}
	}
	if asks == 0 {
		out.WriteString(none)
	}
	out.WriteString(d.TimelineMarkdown() + "## Files\n\n")
	files := sortedFiles(d.Files)
	for _, f := range files {
		v := f.Versions[len(f.Versions)-1]
		fmt.Fprintf(&out, "- %s · v%d · %d bytes · by %s at %s\n", text(f.Name), v.Version, v.Size, member(v.By), when(v.At))
	}
	if len(files) == 0 {
		out.WriteString("None.\n")
	}
	return out.String()
}

// TaskMarkdown groups tasks by their current state, live work first, each group
// ordered by task number.
func (d Document) TaskMarkdown() string {
	var out strings.Builder
	out.WriteString("## Tasks\n\n")
	tasks := append([]board.Task(nil), d.Tasks...)
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	for _, group := range []struct {
		label  string
		states string
	}{{"In progress", "in_progress"}, {"Open", "open"}, {"Done", "done cancelled"}} {
		fmt.Fprintf(&out, "### %s\n\n", group.label)
		found := false
		for _, t := range tasks {
			if !strings.Contains(" "+group.states+" ", " "+t.State+" ") {
				continue
			}
			found = true
			fmt.Fprintf(&out, "#### %s: %s\n\n- State: %s\n", text(t.Ref), text(t.Title), words(t.State))
			if t.Owner != nil {
				fmt.Fprintf(&out, "- Owner: %s\n", member(*t.Owner))
			}
			if len(t.Helpers) > 0 {
				helpers := make([]string, len(t.Helpers))
				for i, h := range t.Helpers {
					helpers[i] = member(h)
				}
				fmt.Fprintf(&out, "- Helping: %s\n", strings.Join(helpers, ", "))
			}
			if t.About != nil {
				out.WriteString("\nAbout:\n\n" + prose(t.About.Text, 4))
			}
			if t.Stands != nil {
				fmt.Fprintf(&out, "\nWhere it stands (by %s):\n\n%s", member(t.Stands.By), prose(t.Stands.Text, 4))
			}
			if t.ClosedNote != nil {
				out.WriteString("\nClosing note:\n\n" + prose(*t.ClosedNote, 4))
			}
			out.WriteString("\n")
		}
		if !found {
			out.WriteString(none)
		}
	}
	return out.String()
}

// TimelineMarkdown nests visible replies under visible roots without naming hidden roots.
func (d Document) TimelineMarkdown() string {
	var out strings.Builder
	out.WriteString("## Timeline\n\n")
	if len(d.Messages) == 0 {
		out.WriteString(none)
		return out.String()
	}
	out.WriteString("Messages are numbered by their place on the board. Board events, such as joins and task changes, are not listed, so the numbers skip.\n\n")
	visible := map[string]bool{}
	replies := map[string][]board.Message{}
	for _, m := range d.Messages {
		visible[m.ID] = true
	}
	for _, m := range d.Messages {
		if m.ThreadRoot != nil && visible[*m.ThreadRoot] {
			replies[*m.ThreadRoot] = append(replies[*m.ThreadRoot], m)
		}
	}
	render := func(m board.Message, nested bool) {
		var entry strings.Builder
		fmt.Fprintf(&entry, "### #%d · @%s", m.Seq, text(m.SenderName))
		if m.SenderOwner != nil {
			fmt.Fprintf(&entry, " (%s's agent)", text(*m.SenderOwner))
		}
		fmt.Fprintf(&entry, " → %s", recipients(m.To))
		for _, tag := range m.About {
			entry.WriteString(" · " + text(tag.Ref))
		}
		fmt.Fprintf(&entry, " · %s\n\n%s", when(m.At), prose(m.Body, 3))
		for _, r := range m.Reactions {
			fmt.Fprintf(&entry, "\nReaction: %s by %s\n", text(r.Emoji), text(strings.Join(r.By, ", ")))
		}
		for _, f := range m.Files {
			fmt.Fprintf(&entry, "\nAttachment: %s v%d\n", text(f.Name), f.Version)
		}
		if nested {
			for _, line := range strings.Split(strings.TrimSuffix(entry.String(), "\n"), "\n") {
				out.WriteString(strings.TrimRight("> "+line, " ") + "\n")
			}
		} else {
			out.WriteString(entry.String())
		}
		out.WriteString("\n")
	}
	for _, m := range d.Messages {
		if m.ThreadRoot != nil && visible[*m.ThreadRoot] {
			continue
		}
		render(m, false)
		for _, reply := range replies[m.ID] {
			render(reply, true)
		}
	}
	return out.String()
}

func sortedFiles(files []board.File) []board.File {
	out := append([]board.File(nil), files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Zip writes documents and unchanged latest bytes; open must authorize the exact file version.
// Every entry is dated at the export time.
func (d Document) Zip(w io.Writer, open func(board.File) (io.ReadCloser, error)) error {
	z := zip.NewWriter(w)
	add := func(name string, r io.Reader) error {
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") {
			return fmt.Errorf("invalid archive path")
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: d.At.UTC()}
		h.SetMode(0o600)
		entry, err := z.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, r)
		return err
	}
	for _, entry := range []struct{ name, body string }{{"board.md", d.Markdown()}, {"tasks.md", d.TaskMarkdown()}, {"timeline.md", d.TimelineMarkdown()}} {
		if err := add(entry.name, strings.NewReader(entry.body)); err != nil {
			return err
		}
	}
	if d.BriefName != "" {
		if err := add(d.BriefName, strings.NewReader(string(d.Brief))); err != nil {
			return err
		}
	}
	for _, f := range sortedFiles(d.Files) {
		r, err := open(f)
		if err != nil {
			return err
		}
		err = add("files/"+f.Name, r)
		closeErr := r.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return z.Close()
}
