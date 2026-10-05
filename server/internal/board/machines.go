package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// A new machine gets a key of its own without a secret being copied to it: it asks the
// server for a request, shows its short code, and a person signed in elsewhere approves
// the code. The machine then collects a new key with the long secret only it holds, so
// someone who saw only the short code can't collect anything.
const (
	// MachineRequestTTL is how long a machine request works, from asking to collecting.
	MachineRequestTTL = 5 * time.Minute
	// MachineRequestPollEvery is how long a waiting machine is asked to leave between
	// collection attempts.
	MachineRequestPollEvery = 2 * time.Second
	// MachineRequestMaxPolls is how many times a machine may ask for its key before its
	// request ends: more than it needs at MachineRequestPollEvery within the request's
	// lifetime.
	MachineRequestMaxPolls = 200
)

// collectSecretPrefix starts the secret a machine collects its key with.
const collectSecretPrefix = "abc_"

// StartedMachineRequest is a new machine request. Code and Secret exist only here.
type StartedMachineRequest struct {
	Code      string
	Secret    string
	ExpiresAt string
	PollEvery time.Duration
}

// StartMachineRequest records a machine's request for a key for the person handle
// names: label is the name the machine gives itself, and from the client address it
// asked from. Anyone may ask; only that person, signed in elsewhere, can approve it. A
// handle nobody has is recorded the same way, without looking it up, so the answer
// never says which handles exist; such a request can never be approved.
func (s *Service) StartMachineRequest(ctx context.Context, handle, label, from string) (StartedMachineRequest, error) {
	if !validName(handle) {
		return StartedMachineRequest{}, apierr.New(http.StatusUnprocessableEntity, "handle_invalid",
			fmt.Sprintf("%q can't be a handle: use lowercase letters, digits and single dashes, at most 40 characters.", handle),
			"Give your handle on the server, such as maya.")
	}
	if !validName(label) {
		return StartedMachineRequest{}, invalid("A machine's name uses lowercase letters, digits and single dashes, at most 40 characters.",
			"Name the machine after its host name, such as maya-desktop.")
	}
	var out StartedMachineRequest
	err := s.st.Write(ctx, func(tx Tx) error {
		now := s.clk.Now()
		if err := tx.DeleteEndedMachineRequests(stamp(now)); err != nil {
			return err
		}
		// A short code names one live request: draw again in the rare case it is taken.
		var code string
		for range 5 {
			c, err := s.gen.JoinCode()
			if err != nil {
				return err
			}
			if _, err := tx.MachineRequestByCode(ids.Digest(s.key, c)); errors.Is(err, ErrNotFound) {
				code = c
				break
			} else if err != nil {
				return err
			}
		}
		if code == "" {
			return errors.New("no free short code for a machine request")
		}
		secret, err := s.gen.Token(strings.TrimSuffix(collectSecretPrefix, "_"))
		if err != nil {
			return err
		}
		r := MachineRequest{
			CodeDigest: ids.Digest(s.key, code), SecretDigest: ids.Digest(s.key, secret), Label: label, Handle: handle, RequestedFrom: from,
			CreatedAt: stamp(now), ExpiresAt: stamp(now.Add(MachineRequestTTL)), State: MachinePending,
		}
		if r.ID, err = s.gen.ID("mrq", now); err != nil {
			return err
		}
		if err := tx.InsertMachineRequest(r); err != nil {
			return fmt.Errorf("insert machine request: %w", err)
		}
		out = StartedMachineRequest{Code: code, Secret: secret, ExpiresAt: r.ExpiresAt, PollEvery: MachineRequestPollEvery}
		return nil
	})
	return out, err
}

// MachineRequestView is a machine request as the person deciding it sees it, with
// Person, the caller, as whom approving it signs the machine in.
type MachineRequestView struct {
	Request MachineRequest
	Person  Human
}

// machineCodeInvalid is the error for a short code that is wrong, expired, already
// decided or for another person; it doesn't say which.
func machineCodeInvalid() *apierr.Error {
	return apierr.New(http.StatusNotFound, "machine_request_invalid",
		"That code doesn't work for you: it is wrong, expired, already approved or refused, or the machine asked for someone else.",
		"Check the code the new machine shows and the person it names; if it has expired, run aboard connect <server URL> there again for a new one.")
}

// requireOwnKeyToDecide refuses everything but a person's own access key: approving a
// machine gives it a key, which an agent or a browser never may.
func requireOwnKeyToDecide(p Principal) error {
	if err := requireHuman(p); err != nil {
		return apierr.New(http.StatusForbidden, "human_token_required",
			"Only a person can approve or refuse a machine, with their own access key; an agent can't.",
			"Ask your person to run aboard approve <code> in their own terminal.")
	}
	if p.Browser {
		return apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't approve or refuse a machine; only your own access key can.",
			"Run aboard approve <code> in a terminal.")
	}
	if p.KeyID == "" {
		return apierr.Unauthorized()
	}
	return nil
}

// pendingRequest finds the pending, unexpired request a typed short code names for the
// person me. A request for anyone else fails exactly as a wrong code does.
func (s *Service) pendingRequest(tx ReadTx, me Human, code, now string) (MachineRequest, error) {
	c, ok := ids.NormalizeJoinCode(code)
	if !ok {
		return MachineRequest{}, machineCodeInvalid()
	}
	r, err := tx.MachineRequestByCode(ids.Digest(s.key, c))
	if errors.Is(err, ErrNotFound) {
		return MachineRequest{}, machineCodeInvalid()
	}
	if err != nil {
		return MachineRequest{}, err
	}
	if r.State != MachinePending || r.ExpiresAt <= now || r.Polls >= MachineRequestMaxPolls || r.Handle != me.Name {
		return MachineRequest{}, machineCodeInvalid()
	}
	return r, nil
}

// LookupMachineRequest shows the pending request a short code names, so its person can
// see what they would approve. Only a person's own key can.
func (s *Service) LookupMachineRequest(ctx context.Context, p Principal, code string) (MachineRequestView, error) {
	if err := requireOwnKeyToDecide(p); err != nil {
		return MachineRequestView{}, err
	}
	var out MachineRequestView
	err := s.st.Read(ctx, func(tx ReadTx) error {
		now := stamp(s.clk.Now())
		if err := stillValid(tx, p, now); err != nil {
			return err
		}
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		r, err := s.pendingRequest(tx, h, code, now)
		if err != nil {
			return err
		}
		out = MachineRequestView{Request: r, Person: h}
		return nil
	})
	return out, err
}

// ApproveMachineRequest approves the pending request a short code names, when it names
// the caller: the machine may then collect a new key of the caller's. Only a person's own
// key can, and the key is made only if that key still works when the machine collects.
func (s *Service) ApproveMachineRequest(ctx context.Context, p Principal, code string) (MachineRequestView, error) {
	return s.decideMachineRequest(ctx, p, code, MachineApproved)
}

// RefuseMachineRequest refuses the pending request a short code names; the machine is
// told so when it next asks for its key.
func (s *Service) RefuseMachineRequest(ctx context.Context, p Principal, code string) (MachineRequestView, error) {
	return s.decideMachineRequest(ctx, p, code, MachineRefused)
}

func (s *Service) decideMachineRequest(ctx context.Context, p Principal, code, state string) (MachineRequestView, error) {
	if err := requireOwnKeyToDecide(p); err != nil {
		return MachineRequestView{}, err
	}
	var out MachineRequestView
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := stamp(s.clk.Now())
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		// A guest gets keys only through guest codes, never by approving a machine.
		if h.Role == ServerGuest && state == MachineApproved {
			return guestNotAllowed("approve a new machine")
		}
		r, err := s.pendingRequest(tx, h, code, now)
		if err != nil {
			return err
		}
		ok, err := tx.DecideMachineRequest(r.ID, state, h.ID, p.KeyID, now)
		if err != nil {
			return fmt.Errorf("decide machine request %s: %w", r.ID, err)
		}
		if !ok {
			return machineCodeInvalid()
		}
		r.State, r.DecidedBy, r.DecidedKey, r.DecidedAt = state, &h.ID, &p.KeyID, &now
		out = MachineRequestView{Request: r, Person: h}
		return nil
	})
	return out, err
}

// MachineCollection is the answer to a machine asking for its key: still pending, with
// when to ask again, or collected, with the new key.
type MachineCollection struct {
	Pending   bool
	ExpiresAt string
	PollEvery time.Duration
	Connected Connected
}

// machineRequestEnded is the error for a collection secret that is wrong, or a request
// that expired, was collected, was asked about too often, or whose approving key
// stopped working; it doesn't say which.
func machineRequestEnded() *apierr.Error {
	return apierr.New(http.StatusNotFound, "machine_request_invalid",
		"This machine's request has ended: it expired, was already used, or the key that approved it stopped working.",
		"Run aboard connect <server URL> again for a new code, or paste a key with aboard login.")
}

// CollectMachineRequest answers a machine asking for its key with its collection
// secret. Once the request is approved, it makes a new key for the person who approved
// it and uses the request up, in one transaction, so a key is collected at most once.
// The key is named after the request's label and expires once unused for
// MachineKeyIdle, like the key an invite gives.
func (s *Service) CollectMachineRequest(ctx context.Context, secret string) (MachineCollection, error) {
	if !strings.HasPrefix(secret, collectSecretPrefix) {
		return MachineCollection{}, machineRequestEnded()
	}
	var out MachineCollection
	err := s.st.Write(ctx, func(tx Tx) error {
		// The time is read once the transaction holds the write lock, so a request that
		// expires while the collection waits for it isn't collected.
		now := s.clk.Now()
		r, err := tx.MachineRequestBySecret(ids.Digest(s.key, secret))
		if errors.Is(err, ErrNotFound) {
			return machineRequestEnded()
		}
		if err != nil {
			return err
		}
		if r.ExpiresAt <= stamp(now) || r.Polls >= MachineRequestMaxPolls {
			return machineRequestEnded()
		}
		switch r.State {
		case MachinePending:
			out = MachineCollection{Pending: true, ExpiresAt: r.ExpiresAt, PollEvery: MachineRequestPollEvery}
			return tx.CountMachineRequestPoll(r.ID)
		case MachineRefused:
			return apierr.New(http.StatusForbidden, "machine_request_refused",
				"The request to connect this machine was refused.",
				"If that was a mistake, run aboard connect <server URL> again, or paste a key with aboard login.")
		case MachineApproved:
		default:
			return machineRequestEnded()
		}
		if r.DecidedBy == nil || r.DecidedKey == nil {
			return machineRequestEnded()
		}
		// The approving person's key must still work: a key revoked since it approved
		// gives nothing.
		h, err := tx.HumanByID(*r.DecidedBy)
		if errors.Is(err, ErrNotFound) {
			return machineRequestEnded()
		}
		if err != nil {
			return err
		}
		if err := stillValid(tx, Principal{Human: &h, KeyID: *r.DecidedKey}, stamp(now)); err != nil {
			if _, ok := apierr.As(err); ok {
				return machineRequestEnded()
			}
			return err
		}
		name, err := freeKeyName(tx, h.ID, r.Label, stamp(now))
		if err != nil {
			return err
		}
		token, key, err := s.newKey(tx, h.ID, name, now, nil, ptr(MachineKeyIdle))
		if err != nil {
			return err
		}
		ok, err := tx.CollectMachineRequest(r.ID, key.ID)
		if err != nil {
			return fmt.Errorf("collect machine request %s: %w", r.ID, err)
		}
		if !ok {
			return machineRequestEnded()
		}
		out = MachineCollection{Connected: Connected{Person: h, Key: key, Token: token}}
		return nil
	})
	return out, err
}

// freeKeyName is label, or label-2, label-3 and so on when the person already has a
// working key with that name.
func freeKeyName(tx ReadTx, humanID, label, now string) (string, error) {
	keys, err := tx.KeysOf(humanID, now)
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, k := range keys {
		if keyWorks(k.AccessKey, now) {
			taken[k.Name] = true
		}
	}
	name := label
	for n := 2; taken[name]; n++ {
		suffix := "-" + strconv.Itoa(n)
		base := label
		if len(base)+len(suffix) > 40 {
			base = strings.TrimRight(base[:40-len(suffix)], "-")
		}
		name = base + suffix
	}
	return name, nil
}
