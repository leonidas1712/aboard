package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// asker asks a person questions at a terminal with selectors: arrow keys move, space
// picks in a list, Enter confirms. With TERM=dumb or ACCESSIBLE set it asks line by line
// instead, for screen readers and terminals that can't redraw. Commands ask only when
// app.interactive says so, and every question has a flag that answers it.
type asker struct {
	in         io.Reader
	out        io.Writer
	accessible bool
	// width is the questions' width: the terminal's, up to askWidth.
	width int
}

// askWidth is the widest questions get, so text stays readable on a wide terminal.
const askWidth = 80

// errAborted means the person pressed Ctrl-C or Esc instead of answering.
var errAborted = errors.New("aborted")

func (a *app) asker() *asker {
	k := &asker{
		in: a.env.Stdin, out: a.env.Stdout,
		accessible: a.env.Getenv("TERM") == "dumb" || a.env.Getenv("ACCESSIBLE") != "",
	}
	k.width = askWidth
	if f, ok := a.env.Stdout.(*os.File); ok {
		if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
			k.width = min(w, askWidth)
		}
	}
	if k.accessible && !a.out().on {
		// Questions asked line by line are written as they are, so drop their color here.
		k.out = &colorprofile.Writer{Forward: k.out, Profile: colorprofile.NoTTY}
	}
	return k
}

// run asks one question.
func (k *asker) run(field huh.Field) error {
	interrupted := false
	// Interrupt closes Bubble Tea's input reader without joining its read loop.
	// Quit waits for that loop; remember the signal so no answer is applied.
	filter := tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.InterruptMsg); ok {
			interrupted = true
			return tea.QuitMsg{}
		}
		return msg
	})
	err := huh.NewForm(huh.NewGroup(field)).
		WithProgramOptions(filter).
		WithInput(k.in).WithOutput(k.out).
		WithTheme(huh.ThemeFunc(huh.ThemeBase16)).
		WithAccessible(k.accessible).WithWidth(k.width).
		Run()
	if interrupted || errors.Is(err, huh.ErrUserAborted) {
		return errAborted
	}
	if err != nil {
		return fmt.Errorf("ask on the terminal: %w", err)
	}
	return nil
}

// choice is one option of a question: the value it stands for and what it says.
type choice struct{ value, label string }

func options(choices []choice) []huh.Option[string] {
	opts := make([]huh.Option[string], len(choices))
	for i, c := range choices {
		opts[i] = huh.NewOption(c.label, c.value)
	}
	return opts
}

// pickMany asks for any number of choices, starting with selected picked. It insists on
// at least one.
func (k *asker) pickMany(title, description string, choices []choice, selected []string) ([]string, error) {
	value := append([]string(nil), selected...)
	// Without a height, the list takes its title and description off the options' room
	// and shows none of them; with one, it has room for every option.
	height := len(choices) + 1
	if description != "" {
		height++
	}
	err := k.run(huh.NewMultiSelect[string]().
		Title(title).Description(description).
		Options(options(choices)...).
		Height(height).Filterable(false).
		Value(&value).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("pick at least one")
			}
			return nil
		}))
	return value, err
}

// pickOne asks for one of choices, starting on value.
func (k *asker) pickOne(title, description string, choices []choice, value string) (string, error) {
	err := k.run(huh.NewSelect[string]().
		Title(title).Description(description).
		Options(options(choices)...).
		Value(&value))
	return value, err
}

// text asks for a line of text, starting with value, which Enter alone keeps.
func (k *asker) text(title, description, value string) (string, error) {
	err := k.run(huh.NewInput().Title(title).Description(description).Value(&value))
	return value, err
}

// secret asks for a line of text without showing what is typed.
func (k *asker) secret(title, description string) (string, error) {
	var value string
	err := k.run(huh.NewInput().Title(title).Description(description).EchoMode(huh.EchoModePassword).Value(&value))
	return value, err
}

// confirm asks a yes-or-no question, starting on value.
func (k *asker) confirm(title, description string, value bool) (bool, error) {
	err := k.run(huh.NewConfirm().
		Title(title).Description(description).
		Affirmative("Yes").Negative("No").
		WithButtonAlignment(lipgloss.Left).
		Value(&value))
	return value, err
}
