package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

type setupRuntimeFence struct {
	Harness    string `json:"harness"`
	Boot       string `json:"boot"`
	ConfigHash string `json:"config_hash"`
}

func (a *app) guardSetupRuntime(ctx context.Context, key delivery.SessionKey, setups []harnessSetup, path string) (bool, error) {
	fence, err := readSetupRuntimeFence(path)
	if err != nil {
		return false, err
	}
	hash := sha256.New()
	changed := false
	for _, setup := range setups {
		for _, change := range setup.Changes {
			if change.Kind != "hooks" && change.Kind != "file" {
				continue
			}
			_, _ = fmt.Fprintf(hash, "%d:%s%d:%s%d:", len(change.Path), change.Path, len(change.Kind), change.Kind, len(change.data))
			_, _ = hash.Write(change.data)
			changed = changed || change.Action != actionUnchanged
		}
	}
	config := hex.EncodeToString(hash.Sum(nil))
	boot := a.env.Getenv("ABOARD_BOOT")
	resp, readErr := a.callDaemon(ctx, delivery.Request{Op: delivery.OpAgents, Harness: key.Harness, Session: key.ID, Boot: boot})
	if changed {
		oldBoot := boot
		if oldBoot == "" {
			oldBoot = resp.Boot
		}
		fence = &setupRuntimeFence{Harness: key.Harness, Boot: oldBoot, ConfigHash: config}
		// Save the fence before changing hooks: a crash or another saved invite must not
		// mistake their now-unchanged files for confirmation of the old runtime.
		if err := writeJSONFile(path, fence, 0o600); err != nil {
			return false, err
		}
		if err := syncSetupFile(path); err != nil {
			return false, err
		}
		if err := syncSetupFile(filepath.Dir(path)); err != nil {
			return false, err
		}
		return false, nil
	}
	observed := readErr == nil && resp.RuntimeReady && (boot == "" || boot == resp.Boot)
	if !observed {
		return false, nil
	}
	if fence != nil && (fence.Harness != key.Harness || fence.ConfigHash != config || (fence.Boot != "" && fence.Boot == resp.Boot)) {
		return false, nil
	}
	return true, nil
}

func readSetupRuntimeFence(path string) (*setupRuntimeFence, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || !setupOwned(st) {
		return nil, newError("setup_state_unwritable", "The saved harness restart state is not private.", "Give Aboard's setup state mode 0600, then Continue Aboard setup.")
	}
	var out setupRuntimeFence
	if err := json.NewDecoder(f).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (a *app) setupRuntimeFencePath(harness string) (string, error) {
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	id := sha256.Sum256([]byte(harness))
	return filepath.Join(p.state, "setup-runtime", hex.EncodeToString(id[:])+".json"), nil
}
