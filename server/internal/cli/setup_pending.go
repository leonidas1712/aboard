package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// setupPending retains the original proof across a lost redemption response. Attempted
// is saved before the request: a restart cannot guess that an invite remains unused.
type setupPending struct {
	Server      string                 `json:"server"`
	Invite      string                 `json:"invite"`
	Token       string                 `json:"token"`
	Handle      string                 `json:"handle"`
	Name        string                 `json:"name"`
	DisplayName *string                `json:"display_name,omitempty"`
	Attempted   bool                   `json:"attempted"`
	Receipt     *api.OnboardingReceipt `json:"receipt,omitempty"`
	Connected   *api.ClientConnected   `json:"connected,omitempty"`
}

func (a *app) setupPendingPath(srv serverRef, invite string) (string, error) {
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	if err := privateSetupDir(p.state); err != nil {
		return "", err
	}
	return filepath.Join(p.state, "onboarding", setupInviteID(srv, invite)+".json"), nil
}

func privateSetupDir(dir string) error {
	var created []string
	for current := dir; ; current = filepath.Dir(current) {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		created = append(created, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm()&0o077 != 0 || !ok || int64(owner.Uid) != int64(os.Getuid()) {
		return newError("setup_state_unwritable", "Setup needs a private state directory.", "Give this machine's Aboard state directory mode 0o700 and retry.")
	}
	for _, createdDir := range created {
		if err := syncSetupFile(filepath.Dir(createdDir)); err != nil {
			return err
		}
	}
	return nil
}

func lockSetup(path string) (*os.File, error) {
	if err := privateSetupDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Clean(path+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Mode().Perm()&0o077 != 0 || !setupOwned(st)) {
		err = errors.New("setup lock is not a private regular file")
	}
	if err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func readSetupPending(path string) (*setupPending, error) {
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
		return nil, newError("setup_state_unwritable", "The pending account proof is not a private regular file.", "Keep the pending proof unchanged and ask your person to fix its permissions.")
	}
	var pending setupPending
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pending); err != nil {
		return nil, newError("setup_state_invalid", "The saved setup proof cannot be read.", "Keep this file; ask your person to recover setup without creating another account.")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, invalidSetupState()
	}
	if err := validateSetupBinding(path, &pending); err != nil {
		return nil, err
	}
	if pending.Token == "" {
		if pending.Handle != "" || pending.Name != "" || pending.DisplayName != nil || pending.Attempted || pending.Receipt != nil || pending.Connected != nil {
			return nil, invalidSetupState()
		}
		return &pending, nil
	}
	raw := strings.TrimPrefix(pending.Token, "abh_")
	bits, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if !strings.HasPrefix(pending.Token, "abh_") || err != nil || len(bits) != 32 || base64.RawURLEncoding.EncodeToString(bits) != raw {
		return nil, newError("setup_state_invalid", "The saved setup proof is invalid.", "Keep this file; do not create a replacement account or key.")
	}
	return &pending, nil
}

func saveSetupPending(path string, pending *setupPending) error {
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncSetupFile(dir)
}

func syncSetupFile(path string) error {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func (a *app) createSetupPending(srv serverRef, invite, handle, name string, display *string) (*setupPending, error) {
	bits := make([]byte, 32)
	if _, err := io.ReadFull(a.env.Rand, bits); err != nil {
		return nil, fmt.Errorf("generate pending setup proof: %w", err)
	}
	return &setupPending{Server: srv.URL, Invite: invite, Token: "abh_" + base64.RawURLEncoding.EncodeToString(bits), Handle: handle, Name: name, DisplayName: display}, nil
}

func setupOwned(st os.FileInfo) bool {
	owner, ok := st.Sys().(*syscall.Stat_t)
	return ok && int64(owner.Uid) == int64(os.Getuid())
}

func invalidSetupState() error {
	return newError("setup_state_invalid", "The saved setup state is invalid.", "Keep the saved file unchanged; ask your person to recover setup without creating another account or key.")
}

func setupInviteID(srv serverRef, invite string) string {
	digest := sha256.Sum256([]byte(srv.URL + "\x00" + invite))
	return hex.EncodeToString(digest[:])
}

func validateSetupBinding(path string, pending *setupPending) error {
	srv, err := parseServerURL(pending.Server)
	if err != nil || srv.URL != pending.Server || !strings.HasPrefix(pending.Invite, "abi_") || strings.ContainsAny(pending.Invite, " \t\r\n?#") || filepath.Base(path) != setupInviteID(srv, pending.Invite)+".json" {
		return invalidSetupState()
	}
	return nil
}

func (a *app) stageSetupInvite(srv serverRef, invite string) error {
	path, err := a.setupPendingPath(srv, invite)
	if err != nil {
		return err
	}
	pending := &setupPending{Server: srv.URL, Invite: invite}
	if err := validateSetupBinding(path, pending); err != nil {
		return err
	}
	lock, err := lockSetup(path)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	existing, err := readSetupPending(path)
	if err != nil || existing != nil {
		return err
	}
	return saveSetupPending(path, pending)
}

func (a *app) continuedSetup(selector, serverFlag string) (serverRef, string, error) {
	if selector != "" {
		bits, err := hex.DecodeString(selector)
		if err != nil || len(bits) != 32 || hex.EncodeToString(bits) != selector {
			return serverRef{}, "", invalidSetupState()
		}
	}
	filter := ""
	if serverFlag != "" {
		srv, err := a.namedServer(serverFlag)
		if err != nil {
			return serverRef{}, "", err
		}
		filter = srv.URL
	}
	p, err := a.paths()
	if err != nil {
		return serverRef{}, "", err
	}
	if err := privateSetupDir(p.state); err != nil {
		return serverRef{}, "", err
	}
	dir := filepath.Join(p.state, "onboarding")
	if err := privateSetupDir(dir); err != nil {
		return serverRef{}, "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return serverRef{}, "", err
	}
	var candidates []*setupPending
	choices := []map[string]string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || selector != "" && entry.Name() != selector+".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		pending, err := readSetupPending(path)
		if err != nil {
			return serverRef{}, "", err
		}
		if pending == nil || filter != "" && pending.Server != filter {
			continue
		}
		candidates = append(candidates, pending)
		id := strings.TrimSuffix(entry.Name(), ".json")
		choices = append(choices, map[string]string{"id": id, "server": pending.Server, "command": "aboard setup --continue " + id + " --handle NAME"})
	}
	if len(candidates) == 0 {
		return serverRef{}, "", newError("setup_not_found", "No saved setup matches this selection.", "Start setup with the original invite link.")
	}
	if len(candidates) > 1 {
		e := newError("setup_ambiguous", "Several saved setups match this selection.", "Choose a saved setup id and run aboard setup --continue ID --handle NAME.")
		e.Details = map[string]any{"setups": choices}
		return serverRef{}, "", e
	}
	return a.serverRefFor(candidates[0].Server), candidates[0].Invite, nil
}

func (a *app) setupContinueCommand(srv serverRef, invite string) string {
	selected, saved, err := a.continuedSetup("", "")
	if err == nil && selected.URL == srv.URL && saved == invite {
		return "aboard setup --continue"
	}
	return "aboard setup --continue " + setupInviteID(srv, invite)
}
