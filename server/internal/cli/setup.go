package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

type setupStep struct {
	Step    string `json:"step"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}
type setupOutput struct {
	Server serverRef     `json:"server"`
	State  string        `json:"state"`
	Steps  []setupStep   `json:"steps"`
	Person *api.Person   `json:"person,omitempty"`
	Next   *api.NextStep `json:"next,omitempty"`
}

func newSetupOutput(srv serverRef) setupOutput {
	out := setupOutput{Server: srv, State: "pending", Steps: []setupStep{}}
	for _, name := range []string{"installed", "account", "memberships", "harness", "pairing", "delivery"} {
		out.Steps = append(out.Steps, setupStep{Step: name, State: "pending"})
	}
	return out
}

func runSetup(ctx context.Context, a *app, args []string) error {
	use := usageOf("setup")
	flags := a.flags("setup")
	handle := flags.String("handle", "", "your visible name on this server")
	name := flags.String("name", "", "this machine's name")
	server := flags.String("server", "", "the issuer of a pairing request")
	pos, err := a.parse(flags, args, use, 1, 1)
	if err != nil {
		return err
	}
	if !isInviteLink(pos[0]) {
		if !strings.HasPrefix(pos[0], "ppr_") {
			return usageError("Paste an invite link or a pairing request id.", use)
		}
		srv, err := a.namedServer(*server)
		if err != nil {
			return err
		}
		out := newSetupOutput(srv)
		out.Next = &api.NextStep{Command: "aboard pairing accept " + commandWord(pos[0]) + " --here --server " + commandWord(srv.URL), Resume: "Pairing acceptance is not available in this build. Continue Aboard setup once it is available."}
		return emitSetup(a, out)
	}
	srv, invite, err := parseInviteLink(pos[0])
	if err != nil {
		return err
	}
	if *server != "" {
		selected, err := a.namedServer(*server)
		if err != nil {
			return err
		}
		if selected.URL != srv.URL {
			return newError("invite_link_invalid", "The invite belongs to another server.", "Use the invite's issuing server; setup never switches issuers.")
		}
	}
	if err := a.prepareServerName(&srv, ""); err != nil {
		return err
	}
	h := strings.TrimSpace(*handle)
	if h == "" {
		h = rules.NormalizeName(a.env.Getenv("USER"))
	}
	machine := *name
	if machine == "" {
		machine = machineName()
	}
	out := newSetupOutput(srv)
	exe, err := a.env.Executable()
	if err != nil {
		return err
	}
	st, err := os.Stat(exe)
	if err != nil {
		return err
	}
	owner, owned := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o200 == 0 || !owned || int64(owner.Uid) != int64(os.Getuid()) {
		out.Next = &api.NextStep{Command: "aboard doctor", Resume: "Make the installed Aboard binary writable by its owner, then Continue Aboard setup."}
		return emitSetup(a, out)
	}
	out.Steps[0].State = "complete"
	out.Steps[0].Message = "Aboard is installed; its owner can update it."
	pending, err := a.redeemSetup(ctx, srv, invite, h, machine, nil)
	if err != nil {
		path, pathErr := a.setupPendingPath(srv, invite)
		if pathErr != nil {
			return pathErr
		}
		proof, readErr := readSetupPending(path)
		if readErr != nil || proof == nil || !proof.Attempted {
			return err
		}
		out.State = "uncertain"
		out.Steps[1].State = "uncertain"
		out.Steps[1].Message = "The original account proof is retained; this account has not been confirmed."
		out.Next = setupRecoveryNext(srv)
		return emitSetup(a, out)
	}
	out.Steps[1].State = "complete"
	out.Steps[1].Message = "The original machine-held key authenticated its onboarding outcome."
	out.Steps[2].State = "complete"
	out.Steps[2].Message = "Current accessible memberships were checked; removed access was not recreated."
	if pending.Connected != nil {
		out.Person = &pending.Connected.Person
	}
	next, err := a.setupHarness(ctx, exe)
	if err != nil {
		return err
	}
	out.Steps[3].Message = "Harness configuration needs trust or restart confirmation."
	out.Steps[4].Message = "Pairing acceptance is not available in this build."
	out.Steps[5].Message = "Delivery has not been verified by a session round trip."
	out.Next = next
	return emitSetup(a, out)
}

func setupRecoveryNext(srv serverRef) *api.NextStep {
	return &api.NextStep{Command: "aboard doctor --server " + commandWord(srv.URL), Resume: "Keep the saved pending proof. Ask your person to check this server and the original account's key, then Continue Aboard setup. Do not create another account or replace the key."}
}

func emitSetup(a *app, out setupOutput) error {
	var text strings.Builder
	fmt.Fprintf(&text, "Setup on %s: %s\n", out.Server.URL, out.State)
	for _, step := range out.Steps {
		fmt.Fprintf(&text, "%s: %s", step.Step, step.State)
		if step.Message != "" {
			fmt.Fprintf(&text, " · %s", step.Message)
		}
		text.WriteByte('\n')
	}
	if out.Next != nil {
		text.WriteString(out.Next.Command + "\n" + out.Next.Resume + "\n")
	}
	a.emit(out, text.String())
	return nil
}

func (a *app) redeemSetup(ctx context.Context, srv serverRef, invite, handle, name string, display *string) (*setupPending, error) {
	path, err := a.setupPendingPath(srv, invite)
	if err != nil {
		return nil, err
	}
	lock, err := lockSetup(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	pending, err := readSetupPending(path)
	if err != nil {
		return nil, err
	}
	if pending == nil {
		// An invite never chooses an account already logged in on this machine.
		logins, err := a.readServerLogins()
		if err != nil {
			return nil, err
		}
		if _, exists := logins.find(srv.URL); exists {
			return nil, newError("already_connected", "This machine already holds a login for this issuer.", "Use that account; an invite cannot select or replace it.")
		}
		pending, err = a.createSetupPending(srv, invite, handle, name, display)
		if err != nil {
			return nil, err
		}
		if err := saveSetupPending(path, pending); err != nil {
			return nil, err
		}
	}
	if pending.Server != srv.URL || pending.Invite != invite || pending.Handle != handle {
		return nil, newError("setup_state_invalid", "This request does not match the saved setup proof.", "Use the original issuer, invite and handle; do not replace the pending key.")
	}
	if !pending.Attempted {
		pending.Attempted = true
		if err := saveSetupPending(path, pending); err != nil {
			return nil, err
		}
		c, err := a.newClient(srv, "", requestTimeout)
		if err != nil {
			return nil, err
		}
		rctx, cancel := a.requestContext(ctx)
		response, callErr := c.api.ConnectWithResponse(rctx, nil, api.ConnectRequest{ClientToken: &pending.Token, Invite: pending.Invite, Handle: pending.Handle, KeyName: pending.Name, DisplayName: pending.DisplayName})
		cancel()
		if callErr != nil {
			return nil, c.unreachable(callErr)
		}
		if response.JSON200 == nil {
			return nil, apiError(response.StatusCode(), response.Body)
		}
		if response.JSON200 != nil {
			pending.Connected = response.JSON200
			if !matchingSetupReceipt(pending, &response.JSON200.Onboarding) || response.JSON200.Person.Id != response.JSON200.Onboarding.PersonId || response.JSON200.ServerId != response.JSON200.Onboarding.ServerId || response.JSON200.Key.Id != response.JSON200.Onboarding.KeyId {
				return nil, newError("setup_state_invalid", "The server's onboarding response does not match this proof.", "Keep the pending proof and ask your person to check the issuer.")
			}
			pending.Receipt = &response.JSON200.Onboarding
			if err := saveSetupPending(path, pending); err != nil {
				return nil, err
			}
		}
	}
	c, err := a.newClient(srv, pending.Token, requestTimeout)
	if err != nil {
		return nil, err
	}
	rctx, cancel := a.requestContext(ctx)
	receipt, err := c.api.GetOnboardingReceiptWithResponse(rctx)
	cancel()
	if err != nil {
		return nil, c.unreachable(err)
	}
	if receipt.JSON200 == nil {
		return nil, apiError(receipt.StatusCode(), receipt.Body)
	}
	if !matchingSetupReceipt(pending, receipt.JSON200) {
		return nil, newError("setup_state_invalid", "The authenticated outcome does not match this saved account proof.", "Keep the pending proof; ask your person to recover the original account.")
	}
	rctx, cancel = a.requestContext(ctx)
	me, err := c.api.GetMeWithResponse(rctx)
	cancel()
	if err != nil {
		return nil, c.unreachable(err)
	}
	if me.JSON200 == nil {
		return nil, apiError(me.StatusCode(), me.Body)
	}
	if me.JSON200.Id != receipt.JSON200.PersonId || me.JSON200.Kind != api.MeKindHuman {
		return nil, newError("setup_state_invalid", "The saved key does not authenticate the original onboarding person.", "Keep the original proof and ask your person to check this issuer.")
	}
	pending.Receipt = receipt.JSON200
	if err := saveSetupPending(path, pending); err != nil {
		return nil, err
	}
	if err := a.promoteSetup(srv, pending); err != nil {
		return nil, err
	}
	return pending, nil
}

func matchingSetupReceipt(pending *setupPending, receipt *api.OnboardingReceipt) bool {
	if receipt.InviteId == "" || receipt.ServerId == "" || receipt.PersonId == "" || receipt.KeyId == "" || receipt.Handle != pending.Handle {
		return false
	}
	if old := pending.Receipt; old != nil {
		return old.InviteId == receipt.InviteId && old.ServerId == receipt.ServerId && old.PersonId == receipt.PersonId && old.KeyId == receipt.KeyId && old.Handle == receipt.Handle
	}
	return true
}

func (a *app) promoteSetup(srv serverRef, pending *setupPending) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	var saved serverLogins
	if err := updateJSONFile(p.servers(), &saved, func() error {
		if existing, ok := saved.find(srv.URL); ok {
			if existing.Key != pending.Token || existing.PersonID != pending.Receipt.PersonId {
				return newError("already_connected", "This issuer already has another saved account.", "Keep both records and ask your person which account to use.")
			}
			return nil
		}
		if err := a.saveServerName(&saved, srv); err != nil {
			return err
		}
		saved.Servers = append(saved.Servers, serverLogin{URL: srv.URL, ServerID: pending.Receipt.ServerId, PersonID: pending.Receipt.PersonId, Handle: pending.Receipt.Handle, KeyID: pending.Receipt.KeyId, KeyName: pending.Name, Key: pending.Token})
		return nil
	}); err != nil {
		return err
	}
	if err := syncSetupFile(p.servers()); err != nil {
		return err
	}
	return syncSetupFile(filepath.Dir(p.servers()))
}

func (a *app) setupHarness(ctx context.Context, exe string) (*api.NextStep, error) {
	key, ok := a.sessionKey()
	if !ok {
		return &api.NextStep{Command: "aboard init", Resume: "Set up the harness you use, approve its Aboard hooks, restart it, then Continue Aboard setup."}, nil
	}
	h, ok := a.registry().Get(key.Harness)
	if !ok {
		return &api.NextStep{Command: "aboard init", Resume: "Set up this harness and restart it, then Continue Aboard setup."}, nil
	}
	c := initChoices{scope: scopeGlobal, harnesses: []string{key.Harness}}
	setups, err := a.planInit(ctx, c, []harnessSetup{{Name: key.Harness, Detected: h.Detected(a.henv())}}, exe)
	if err != nil {
		return nil, err
	}
	if err := a.applyInit(ctx, setups, nil); err != nil {
		return nil, err
	}
	if err := a.recordInit(setups, c.scope); err != nil {
		return nil, err
	}
	trust := "Approve Aboard's hooks in your harness, then restart this session."
	if key.Harness == "claude-code" {
		trust = "Run /hooks, approve Aboard's hooks, then restart Claude Code."
	}
	if key.Harness == "codex" {
		trust = "Approve Aboard's project hooks in Codex, then restart Codex."
	}
	return &api.NextStep{Command: "aboard init --harness " + commandWord(key.Harness), Resume: trust + " Continue Aboard setup."}, nil
}
