package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// excerptLength is how many characters of a thread's first line aboard read --threads
// shows.
const excerptLength = 40

// maxThreads is the most threads one request lists.
const maxThreads = 200

// readThreads prints the board's threads, the one with the newest reply first. rest
// holds the flags that repeat --as and --board in the hint for older threads.
func readThreads(ctx context.Context, a *app, c *client, b *api.Board, limit int, rest string) error {
	if limit == 0 {
		limit = defaultReadLimit
	}
	if limit > maxThreads {
		return usageError(fmt.Sprintf("--threads lists at most %d threads at once.", maxThreads), readUsage)
	}
	r, err := c.api.ListThreadsWithResponse(ctx, b.Name, &api.ListThreadsParams{Limit: &limit})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return apiError(r.StatusCode(), r.Body)
	}
	l := r.JSON200
	type thread struct {
		Root         cliMessage `json:"root"`
		Participants []string   `json:"participants"`
	}
	out := make([]thread, 0, len(l.Threads))
	text := l.Board + " · no threads\n"
	if len(l.Threads) > 0 {
		text = fmt.Sprintf("%s · %d %s\n", l.Board, len(l.Threads), plural(len(l.Threads), "thread", "threads"))
	}
	now := time.Now()
	for _, th := range l.Threads {
		out = append(out, thread{cliMessage{Message: th.Root}, th.Participants})
		text += threadLine(th, now) + "\n"
	}
	if l.More && limit < maxThreads {
		text += fmt.Sprintf("Older threads: aboard read --threads --limit %d%s\n", min(2*limit, maxThreads), rest)
	}
	a.emit(struct {
		Board      string   `json:"board"`
		Visibility string   `json:"visibility"`
		Threads    []thread `json:"threads"`
		More       bool     `json:"more"`
	}{l.Board, string(b.Policy.Visibility), out, l.More}, text)
	return nil
}

// threadLine describes one thread on a line: its first message's number and sender, its
// replies, when the last came, who else wrote in it, and how it starts.
func threadLine(th api.ThreadSummary, now time.Time) string {
	m := th.Root
	parts := []string{fmt.Sprintf("#%d  @%s", m.Seq, m.From.Name), repliesText(m.ReplyCount, "no replies")}
	if m.LastReplyAt != nil {
		parts = append(parts, "last "+agoText(*m.LastReplyAt, now))
	}
	others := slices.DeleteFunc(slices.Clone(th.Participants), func(n string) bool { return n == m.From.Name })
	if len(others) > 0 {
		parts = append(parts, strings.Join(others, ", "))
	}
	return strings.Join(append(parts, `"`+excerpt(m.Body)+`"`), " · ")
}

// excerpt returns the start of a message's first line, cut with an ellipsis.
func excerpt(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	line = strings.TrimSpace(line)
	if utf8.RuneCountInString(line) <= excerptLength {
		return line
	}
	return string([]rune(line)[:excerptLength]) + "…"
}

// agoText says how long ago t was: "just now", "5m ago", "2h ago" or "3d ago".
func agoText(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
