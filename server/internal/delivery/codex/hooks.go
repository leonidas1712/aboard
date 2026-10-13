package codex

import (
	"context"
	"encoding/json"
	"fmt"
)

// HookTrust is Codex's read-only approval metadata for one configured command.
type HookTrust struct {
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	TrustStatus string `json:"trustStatus"`
}

// ListHookTrust reads approval state in home for the requested project.
func (a *Adapter) ListHookTrust(ctx context.Context, home, dir string) ([]HookTrust, error) {
	response, err := a.callAppServer(ctx, home, dir, "hooks/list", map[string]any{"cwds": []string{dir}})
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("codex hooks/list: %s", response.Error.Message)
	}
	var result struct {
		Data []struct {
			Hooks []HookTrust `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil || result.Data == nil {
		return nil, fmt.Errorf("codex hooks/list returned no approval metadata")
	}
	var hooks []HookTrust
	for _, entry := range result.Data {
		hooks = append(hooks, entry.Hooks...)
	}
	return hooks, nil
}
