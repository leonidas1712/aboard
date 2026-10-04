package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

// askingAfter is how long swarm up waits for a session's seat before it says the
// harness may be asking a question, when the launcher can't tell.
const askingAfter = 12 * time.Second

// swarmProgress shows on standard error what swarm up is doing while it runs, so it
// never looks stuck: each step as it happens, then, while it waits, where each agent
// is. At a person's terminal the agents' lines update in place, with a spinner; anywhere
// else (a pipe, a file) each change is one plain line, with no control codes. --json
// shows none, so its standard error stays for errors.
type swarmProgress struct {
	w     io.Writer
	on    bool
	live  bool
	st    styles
	width int
	rows  []*progressRow
	drawn int
	frame int
}

// progressRow is one agent's line.
type progressRow struct {
	name, harness, attach string
	state                 progressState
	verb                  string
	since                 time.Time
	took                  time.Duration
}

type progressState int

const (
	rowStarting progressState = iota
	rowSlow
	rowAsking
	rowSeated
	rowRunning
	rowExited
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (a *app) swarmProgress() *swarmProgress {
	p := &swarmProgress{w: a.env.Stderr, on: !a.json}
	_, inSession := a.inSession()
	p.live = p.on && a.env.StderrTerminal && !inSession && a.env.Getenv("TERM") != "dumb"
	p.st = a.errStyles()
	if f, ok := a.env.Stderr.(*os.File); ok && p.live {
		if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
			p.width = w
		}
	}
	return p
}

// step says one thing swarm up did, such as creating the board.
func (p *swarmProgress) step(text string) {
	if !p.on {
		return
	}
	if !p.live {
		_, _ = fmt.Fprintln(p.w, text)
		return
	}
	p.clear()
	_, _ = fmt.Fprintln(p.w, text)
	p.draw()
}

// started adds an agent whose session swarm up just started, resumed or found running.
func (p *swarmProgress) started(ag swarmUpAgent) {
	if !p.on {
		return
	}
	r := &progressRow{name: ag.Name, harness: ag.Harness, attach: deref(ag.Attach), since: time.Now()}
	switch ag.Action {
	case actionResumed:
		r.verb = "resuming"
	case actionAlreadyRunning:
		r.verb, r.state = "already running", rowRunning
	default:
		r.verb = "starting"
	}
	if i := p.find(ag.Name); i >= 0 {
		r.verb = "starting again, fresh"
		p.rows[i] = r
	} else {
		p.rows = append(p.rows, r)
	}
	if !p.live {
		line := ag.Name + ": " + r.verb + " (" + ag.Harness + ", " + ag.Launcher + ")"
		if r.attach != "" && r.state != rowRunning {
			line += "; watch it with " + r.attach
		}
		_, _ = fmt.Fprintln(p.w, line)
		return
	}
	p.clear()
	p.draw()
}

func (p *swarmProgress) find(name string) int {
	for i, r := range p.rows {
		if r.name == name {
			return i
		}
	}
	return -1
}

// update moves agents along from what one look at the swarm found: seated, waiting on
// a question (blocked, as the launcher says), or not seated for a while.
func (p *swarmProgress) update(out []swarmUpAgent, blocked map[string]bool) {
	if !p.on {
		return
	}
	for _, ag := range out {
		i := p.find(ag.Name)
		if i < 0 {
			continue
		}
		r := p.rows[i]
		switch {
		case r.state == rowSeated || r.state == rowExited:
			continue
		case ag.Seated:
			if r.state == rowRunning {
				r.state = rowSeated
				p.say(r, "seated")
				continue
			}
			r.state, r.took = rowSeated, time.Since(r.since)
			p.say(r, "seated after "+seconds(r.took))
		case ag.State == "exited":
			r.state = rowExited
			p.say(r, "its session ended before it took its seat")
		case blocked[ag.Name] && r.state != rowAsking:
			r.state = rowAsking
			p.say(r, "waiting on a question in its window, such as trusting the folder"+answerThere(r.attach))
		case !blocked[ag.Name] && r.state == rowAsking:
			r.state = rowStarting
			p.say(r, "answered; "+r.verb)
		case r.state == rowStarting && time.Since(r.since) > askingAfter:
			r.state = rowSlow
			p.say(r, "not seated after "+seconds(time.Since(r.since))+": it may be asking a question in its window, such as trusting the folder"+answerThere(r.attach))
		}
	}
	if p.live {
		p.frame++
		p.clear()
		p.draw()
	}
}

func answerThere(attach string) string {
	if attach == "" {
		return ""
	}
	return "; answer it there: " + attach
}

// say reports a change of one agent: a plain line, or at a terminal, for a question,
// a line above the agents' lines, which keeps the attach line whole and in view.
func (p *swarmProgress) say(r *progressRow, text string) {
	switch {
	case !p.live:
		_, _ = fmt.Fprintln(p.w, r.name+": "+text)
	case r.state == rowAsking || r.state == rowSlow:
		p.clear()
		_, _ = fmt.Fprintln(p.w, p.st.warn(r.name+": "+text))
		p.draw()
	}
}

// finish leaves the agents' last lines on the screen.
func (p *swarmProgress) finish() {
	if !p.live {
		return
	}
	p.clear()
	p.draw()
	p.drawn = 0
}

// clear moves back over the agents' lines, which draw writes again.
func (p *swarmProgress) clear() {
	if p.drawn > 0 {
		_, _ = fmt.Fprintf(p.w, "\x1b[%dA\r\x1b[J", p.drawn)
		p.drawn = 0
	}
}

func (p *swarmProgress) draw() {
	nameW, harnessW := 0, 0
	for _, r := range p.rows {
		nameW, harnessW = max(nameW, len(r.name)), max(harnessW, len(r.harness))
	}
	var b strings.Builder
	for _, r := range p.rows {
		mark, text, style := spinner[p.frame%len(spinner)], r.verb+" "+seconds(time.Since(r.since)), p.st.dim
		switch r.state {
		case rowSeated:
			mark, text, style = "✓", "seated", p.st.ok
			if r.took > 0 {
				text += " after " + seconds(r.took)
			}
		case rowRunning:
			mark, text = "·", "already running"
		case rowAsking:
			mark, text, style = "⚠", "waiting on a question in its window", p.st.warn
		case rowSlow:
			mark, text, style = "⏳", "not seated yet "+seconds(time.Since(r.since))+"; it may be asking a question", p.st.warn
		case rowExited:
			mark, text, style = "✗", "ended before it took its seat", p.st.bad
		case rowStarting:
		}
		line := fmt.Sprintf("  %s %-*s  %-*s  %s", mark, nameW, r.name, harnessW, r.harness, text)
		if p.width > 1 && utf8.RuneCountInString(line) >= p.width {
			line = string([]rune(line)[:p.width-1])
		}
		b.WriteString(style(line) + "\n")
	}
	_, _ = io.WriteString(p.w, b.String())
	p.drawn = len(p.rows)
}

// seconds is a duration as people read it in progress: 0.4s, 12s, 1m05s.
func seconds(d time.Duration) string {
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}
