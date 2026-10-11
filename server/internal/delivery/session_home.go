package delivery

import (
	"context"
	"fmt"
	"path/filepath"
)

func (d *Daemon) validateSession(ctx context.Context, adapter Adapter, req Request) error {
	a, ok := adapter.(SessionHomeAdapter)
	if !ok {
		return adapter.Validate(ctx, req.Session)
	}
	home := req.CodexHome
	if home != "" && !filepath.IsAbs(home) {
		return fmt.Errorf("codex_home must be an absolute path")
	}
	if home == "" {
		records, err := d.cfg.Journal.Sessions(ctx)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.Key == req.Key() {
				home = record.CodexHome
				break
			}
		}
	}
	return a.ValidateSession(ctx, req.Session, home)
}

func (s *session) noteSessionHome(ctx context.Context, req Request, currentHook bool) {
	if s.key.Harness != "codex" || req.Subagent != "" || req.CodexHome == "" ||
		!filepath.IsAbs(req.CodexHome) || req.CodexHome == s.codexHome {
		return
	}
	// Delayed hooks from the previous boot must not redirect a resumed session.
	if req.Op != OpRegister && req.Op != OpBind && !currentHook &&
		(req.RuntimeHook || req.Boot != "") {
		return
	}
	s.codexHome = req.CodexHome
	s.saveSession(ctx)
}
