package cli

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

type hookReloadSession struct {
	Session string `json:"session"`
	Boot    string `json:"boot"`
}

func sameHookDirectory(left, right string) bool {
	if left == right {
		return true
	}
	l, err := os.Stat(left)
	if err != nil {
		return false
	}
	r, err := os.Stat(right)
	return err == nil && os.SameFile(l, r)
}

func (a *app) hookSessionBoots(ctx context.Context) ([]delivery.SessionRecord, error) {
	p, err := a.paths()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return sqlitejournal.ReadSessionBoots(ctx, p.deliveryDB())
}

func (a *app) checkHookReloads(ctx context.Context) []doctorCheck {
	manifest := a.loadManifest()
	var checks []doctorCheck
	var records []delivery.SessionRecord
	var readErr error
	read := false
	for _, h := range a.registry() {
		pending := false
		for _, scope := range a.scopes() {
			file, ok := manifest.find(a.hooksFile(h, scope), "hooks")
			if !ok || len(file.ReloadSessions) == 0 {
				continue
			}
			if !read {
				records, readErr = a.hookSessionBoots(ctx)
				read = true
			}
			for _, before := range file.ReloadSessions {
				resolved := false
				for _, now := range records {
					if before.Session == now.Key.String() {
						resolved = !now.Open || now.Boot != before.Boot
						break
					}
				}
				pending = pending || readErr != nil || !resolved
			}
		}
		if !pending {
			continue
		}
		p := h.Profile()
		fix := strings.TrimSpace(hookChangesText([]string{p.Harness}))
		checks = append(checks, problem(p.CheckName+"_hooks_reload", levelWarning, p.CheckName+"_hooks_reload_required",
			p.Name+": an open session still needs the changed hooks approved and reloaded", fix))
	}
	return checks
}
