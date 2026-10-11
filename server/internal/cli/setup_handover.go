package cli

import (
	"errors"
	"strings"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/api"
)

const (
	codexSetupTerminal = "aboard init --harness codex --allow-commands"
	codexSetupHandover = "If your agent cannot run commands outside its sandbox, do not retry and do not use sudo. Ask your person to run these commands in their own terminal, in order: if Aboard is missing, curl -fsSL https://comeaboard.dev/install | sh; then aboard init --harness codex --allow-commands. Then restart Codex and say \"continue Aboard setup\"."
)

func (a *app) setupWriteHandover(err error) error {
	if err == nil || (!errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES)) {
		return err
	}
	harness, _, sandboxed := a.sandboxed()
	if !sandboxed || !strings.EqualFold(harness, "codex") {
		return err
	}
	cause := *asError(err)
	cause.Next = &api.NextStep{Command: codexSetupTerminal, Resume: codexSetupHandover}
	cause.Hint = codexSetupHandover
	return &cause
}
