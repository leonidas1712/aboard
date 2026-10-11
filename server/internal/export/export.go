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

func text(s string) string { return html.EscapeString(s) }
func title(s *string) string {
	if s == nil {
		return ""
	}
	return text(*s)
}
func member(m board.Member) string { return "@" + text(m.Name) }
func source(s string) string {
	fence := "```"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence + "\n" + text(s) + "\n" + fence + "\n"
}

// Markdown renders the complete document with fixed section ordering.
func (d Document) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n%s\n\nServer: %s\nExported: %s\nBy: %s\n\n", text(d.Board.Name), title(d.Board.Title), text(d.Server), d.At.UTC().Format(time.RFC3339), text(d.By))
	out.WriteString("Only content visible to the exporting caller is included; hidden payloads are omitted.\n\n## Brief\n\n")
	if d.BriefName == "" {
		out.WriteString("No brief.\n")
	} else {
		out.WriteString(source(string(d.Brief)))
	}
	out.WriteString("\n" + d.TaskMarkdown() + "\n## Asks\n\n")
	for _, m := range d.Messages {
		if m.Ask == nil {
			continue
		}
		fmt.Fprintf(&out, "### #%d by @%s\n\n%s\n", m.Seq, text(m.SenderName), source(m.Body))
		for i, option := range m.Ask.Options {
			fmt.Fprintf(&out, "%d. %s\n", i+1, text(option))
		}
		fmt.Fprintf(&out, "\nState: %s\n\n", text(m.Ask.State))
		for _, answer := range d.Messages {
			if answer.Answer != nil && answer.Answer.AskID == m.ID {
				fmt.Fprintf(&out, "Answer #%d by @%s:\n\n%s\n", answer.Seq, text(answer.SenderName), source(answer.Body))
				if answer.Answer.Withdrawn {
					out.WriteString("Withdrawn.\n\n")
				}
				if answer.Answer.Option != nil {
					fmt.Fprintf(&out, "Chosen option: %d\n\n", *answer.Answer.Option)
				}
			}
		}
	}
	out.WriteString(d.TimelineMarkdown() + "\n## Files\n\n")
	for _, f := range sortedFiles(d.Files) {
		v := f.Versions[len(f.Versions)-1]
		fmt.Fprintf(&out, "- %s · v%d · %d bytes · by %s at %s\n", text(f.Name), v.Version, v.Size, member(v.By), text(v.At))
	}
	return out.String()
}

// TaskMarkdown groups tasks by their current state, ordered by task number.
func (d Document) TaskMarkdown() string {
	var out strings.Builder
	out.WriteString("## Tasks\n\n")
	tasks := append([]board.Task(nil), d.Tasks...)
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	for _, group := range []struct {
		label  string
		states string
	}{{"Done", "done cancelled"}, {"In progress", "in_progress"}, {"Open", "open"}} {
		fmt.Fprintf(&out, "### %s\n\n", group.label)
		for _, t := range tasks {
			if !strings.Contains(" "+group.states+" ", " "+t.State+" ") {
				continue
			}
			fmt.Fprintf(&out, "#### %s: %s\n\nState: %s\n", text(t.Ref), text(t.Title), text(t.State))
			if t.Owner != nil {
				fmt.Fprintf(&out, "Owner: %s\n", member(*t.Owner))
			}
			for _, helper := range t.Helpers {
				fmt.Fprintf(&out, "Helper: %s\n", member(helper))
			}
			if t.About != nil {
				out.WriteString("\nAbout:\n\n" + source(t.About.Text))
			}
			if t.Stands != nil {
				fmt.Fprintf(&out, "\nWhere it stands (by %s):\n\n%s", member(t.Stands.By), source(t.Stands.Text))
			}
			if t.ClosedNote != nil {
				out.WriteString("\nClosed note:\n\n" + source(*t.ClosedNote))
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

// TimelineMarkdown nests visible replies under visible roots without naming hidden roots.
func (d Document) TimelineMarkdown() string {
	var out strings.Builder
	out.WriteString("## Timeline\n\n")
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
			fmt.Fprintf(&entry, " (owner @%s)", text(*m.SenderOwner))
		}
		fmt.Fprintf(&entry, " · %s\n\n%s", text(m.At), source(m.Body))
		for _, r := range m.Reactions {
			fmt.Fprintf(&entry, "\nReaction: %s by %s\n", text(r.Emoji), text(strings.Join(r.By, ", ")))
		}
		for _, f := range m.Files {
			fmt.Fprintf(&entry, "\nAttachment: %s v%d\n", text(f.Name), f.Version)
		}
		entry.WriteString("\n")
		if nested {
			for _, line := range strings.Split(strings.TrimSuffix(entry.String(), "\n"), "\n") {
				out.WriteString("> " + line + "\n")
			}
			out.WriteString("\n")
		} else {
			out.WriteString(entry.String())
		}
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
func (d Document) Zip(w io.Writer, open func(board.File) (io.ReadCloser, error)) error {
	z := zip.NewWriter(w)
	add := func(name string, r io.Reader) error {
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") {
			return fmt.Errorf("invalid archive path")
		}
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
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
