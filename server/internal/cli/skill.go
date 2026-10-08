package cli

import (
	"context"

	bundled "github.com/leonidas1712/aboard/skills/aboard"
)

func runSkill(_ context.Context, a *app, args []string) error {
	fs := a.flags("skill")
	if _, err := a.parse(fs, args, usageOf("skill"), 0, 0); err != nil {
		return err
	}
	a.emit(struct {
		Version string `json:"version"`
		Skill   string `json:"skill"`
	}{version, string(bundled.Skill)}, string(bundled.Skill))
	return nil
}
