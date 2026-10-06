//go:build e2e

package e2e

import (
	"os"
	"reflect"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

// Keyboard cancellation and SIGINT both stop the setup without applying the
// displayed choices, and return the terminal to its original input mode.
func TestSetupInterruptsRestoreTerminalWithoutApplyingAnswers(t *testing.T) {
	for _, source := range []string{"keyboard", "signal"} {
		t.Run(source, func(t *testing.T) {
			e := newEnv(t)
			e.harnessHome()
			before := filesWithContent(t, e.home)
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			original, err := term.GetState(slave.Fd())
			_ = slave.Close()
			_ = master.Close()
			if err != nil {
				t.Fatal(err)
			}
			terminal := e.startTerminal(nil, "init")
			terminal.waitFor("Set up which harnesses?")
			if source == "keyboard" {
				terminal.press(ctrlC)
			} else if err := terminal.cmd.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			terminal.waitFor("Nothing changed.")
			if code := terminal.exit(); code != 0 {
				t.Fatalf("interrupt exit%d:\n%s", code, terminal.text())
			}
			if after := filesWithContent(t, e.home); !mapsEqual(before, after) {
				t.Fatal("interrupted setup changed configuration")
			}
			restored, err := term.GetState(terminal.pty.Fd())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(restored, original) {
				t.Fatal("interrupted setup left the terminal input mode changed")
			}
		})
	}
}
