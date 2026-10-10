package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUntouchedStagedSetupInviteIsReadableWithoutAccountProof(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	srv := serverRef{URL: "https://issuer.example"}
	path, err := a.setupPendingPath(srv, "abi_stage")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockSetup(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := saveSetupPending(path, &setupPending{Server: srv.URL, Invite: "abi_stage"}); err != nil {
		t.Fatal(err)
	}
	pending, err := readSetupPending(path)
	if err != nil {
		t.Fatalf("untouched saved invite cannot continue: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Token != "" || pending.Handle != "" || st.Mode().Perm() != 0o600 {
		t.Fatal("staged invite gained account proof or became public")
	}
}

func TestStageSetupNeverReplacesOriginalAttemptedKey(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	srv := serverRef{URL: "https://issuer.example"}
	path, err := a.setupPendingPath(srv, "abi_original")
	if err != nil {
		t.Fatal(err)
	}
	if err := privateSetupDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	original, err := a.createSetupPending(srv, "abi_original", "colleague", "machine", nil)
	if err != nil {
		t.Fatal(err)
	}
	original.Attempted = true
	if err := saveSetupPending(path, original); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.stageSetupInvite(srv, "abi_original"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("staging replaced the original account proof")
	}
}

func TestStagedSetupSelectionIsIssuerBoundAndNonsecret(t *testing.T) {
	e := lifecycleMachine(t, "https://one.example", "unused", agentCredential{})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	first, second := serverRef{URL: "https://one.example"}, serverRef{URL: "https://two.example"}
	if err := a.stageSetupInvite(first, "abi_first"); err != nil {
		t.Fatal(err)
	}
	if a.setupContinueCommand(first, "abi_first") != "aboard setup --continue" {
		t.Fatal("unique saved setup unnecessarily requires selector")
	}
	srv, invite, err := a.continuedSetup("", "")
	if err != nil || srv.URL != first.URL || invite != "abi_first" {
		t.Fatal("unique setup lost issuer binding")
	}
	if err := a.stageSetupInvite(second, "abi_second"); err != nil {
		t.Fatal(err)
	}
	_, _, err = a.continuedSetup("", "")
	if err == nil || asError(err).Code != "setup_ambiguous" {
		t.Fatal("multiple saved setups selected an account")
	}
	encoded, encodeErr := json.Marshal(asError(err).wire())
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if strings.Contains(string(encoded), "abi_first") || strings.Contains(string(encoded), "abi_second") {
		t.Fatal("continuation choices disclosed an invite")
	}
	srv, invite, err = a.continuedSetup("", second.URL)
	if err != nil || srv.URL != second.URL || invite != "abi_second" {
		t.Fatal("issuer filter selected another setup")
	}
	id := setupInviteID(first, "abi_first")
	if a.setupContinueCommand(first, "abi_first") != "aboard setup --continue "+id {
		t.Fatal("ambiguous next command omits stable selector")
	}
	srv, invite, err = a.continuedSetup(id, "")
	if err != nil || srv.URL != first.URL || invite != "abi_first" {
		t.Fatal("selector lost original binding")
	}
	if _, _, err := a.continuedSetup(id, second.URL); err == nil || asError(err).Code != "setup_not_found" {
		t.Fatal("selector crossed issuer filter")
	}
	if _, _, err := a.continuedSetup("../escape", ""); err == nil || asError(err).Code != "setup_state_invalid" {
		t.Fatal("noncanonical selector accepted")
	}
}

func TestStagedSetupRejectsUnsafeOrMismatchedSavedState(t *testing.T) {
	for _, kind := range []string{"symlink", "public", "wrong_filename", "wrong_issuer", "attempted", "handle", "name", "display", "receipt", "connected", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
			a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
			srv := serverRef{URL: "https://issuer.example"}
			if err := a.stageSetupInvite(srv, "abi_saved"); err != nil {
				t.Fatal(err)
			}
			path, err := a.setupPendingPath(srv, "abi_saved")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				target := filepath.Join(e.home, "outside")
				// #nosec G703 -- The target is a fixed filename inside this test's private HOME.
				if err := os.WriteFile(filepath.Clean(target), data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				} // #nosec G302 -- Deliberately unsafe scratch state.
			case "wrong_filename":
				path = filepath.Join(filepath.Dir(path), strings.Repeat("a", 64)+".json")
				// #nosec G703 -- The destination is a fixed hash filename inside the scratch onboarding directory.
				if err := os.WriteFile(filepath.Clean(path), data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "trailing":
				// #nosec G703 -- The path came from setupPendingPath for this test's private HOME.
				if err := os.WriteFile(filepath.Clean(path), append(data, []byte(` {}`)...), 0o600); err != nil {
					t.Fatal(err)
				}
			default:
				var state map[string]any
				if err := json.Unmarshal(data, &state); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "wrong_issuer":
					state["server"] = "https://issuer.example/"
				case "attempted":
					state["attempted"] = true
				case "handle":
					state["handle"] = "chosen"
				case "name":
					state["name"] = "machine"
				case "display":
					state["display_name"] = "Chosen"
				case "receipt":
					state["receipt"] = map[string]any{}
				case "connected":
					state["connected"] = map[string]any{}
				}
				altered, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Clean(path), altered, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readSetupPending(path); err == nil {
				t.Fatalf("accepted %s staged state", kind)
			}
		})
	}
}
