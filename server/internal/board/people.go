package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// DefaultInviteTTL is how long a server invite works when no lifetime is given, and
// MaxInviteTTL the longest it may work.
const (
	DefaultInviteTTL = 7 * 24 * time.Hour
	MaxInviteTTL     = 30 * 24 * time.Hour
)

// Prefixes of the secrets people use: an access key, and a server invite.
const (
	accessKeyPrefix = "abh_"
	invitePrefix    = "abi_"
)

// Principal is the authenticated caller: exactly one of Human, Agent and Delegation is
// set.
type Principal struct {
	Human *Human
	Agent *Member
	// Delegation is a machine's delegation, which acts for its person only to list their
	// boards (ListBoards) and to give a session a seat (JoinSession); everything else
	// refuses it.
	Delegation *Delegation
	// Browser is set for a browser token, which acts as its human with the human's
	// permissions, except that it can't log in another browser.
	Browser bool
	// KeyID is the access key behind the caller: the person's own key, the key that
	// started their browser login, or the key the agent's token came from. Empty for an
	// agent from before keys were recorded.
	KeyID string
	// browserDigest is the digest of a browser token, whose login is checked again with
	// the key.
	browserDigest string
	// keyUsedBefore is, for a person's own key, when it was last used before this
	// request.
	keyUsedBefore *string
	// delegated is set, on a copy of a delegation's principal, only by the operations a
	// delegation may do, so every other operation's credential check refuses it.
	delegated bool
}

// keyWorks reports whether an access key may still be used at now.
func keyWorks(k AccessKey, now string) bool {
	return k.RevokedAt == nil && (k.ExpiresAt == nil || *k.ExpiresAt > now)
}

// workingKey finds the access key id and fails with ErrNotFound unless it still works,
// so anything started with a revoked or expired key stops with it.
func workingKey(tx ReadTx, id, now string) (AccessKey, error) {
	k, err := tx.AccessKeyByID(id)
	if err != nil {
		return AccessKey{}, err
	}
	if !keyWorks(k, now) {
		return AccessKey{}, ErrNotFound
	}
	return k, nil
}

// writeAs runs fn in a write transaction on behalf of p, after checking, inside that
// transaction, that the credential p authenticated with still works: a key revoked or
// expired since the request was authenticated can't write, nor can a browser login or
// agent token it started. Every write a caller makes goes through here.
func (s *Service) writeAs(ctx context.Context, p Principal, fn func(Tx) error) error {
	return s.st.Write(ctx, func(tx Tx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		return fn(tx)
	})
}

// stillValid checks that p's person is still on the server, that the access key behind
// p still works and still belongs to that person, and for a browser that its login
// hasn't ended. An agent from before keys were recorded, or a guest's agent, has no key
// to check.
func stillValid(tx ReadTx, p Principal, now string) error {
	_, _, err := checkCredential(tx, p, now)
	return err
}

// caller checks p's credential as stillValid does and returns p's person as they are
// now, with their current server role.
func caller(tx ReadTx, p Principal, now string) (Human, error) {
	h, _, err := checkCredential(tx, p, now)
	return h, err
}

// credentialState checks p's credential as stillValid does and returns when it expires:
// the earlier of its key's expiry and its browser login's, or nil if neither expires.
func credentialState(tx ReadTx, p Principal, now string) (*string, error) {
	_, end, err := checkCredential(tx, p, now)
	return end, err
}

func checkCredential(tx ReadTx, p Principal, now string) (Human, *string, error) {
	if p.Delegation != nil {
		if !p.delegated {
			return Human{}, nil, delegationForbidden()
		}
		return checkDelegation(tx, *p.Delegation, now)
	}
	if p.Agent != nil && p.Agent.TokenDigest != nil {
		// A seat's token stops working once a later join gave the seat a new one.
		m, err := tx.MemberByTokenDigest(*p.Agent.TokenDigest)
		if errors.Is(err, ErrNotFound) || (err == nil && m.ID != p.Agent.ID) {
			return Human{}, nil, apierr.Unauthorized()
		}
		if err != nil {
			return Human{}, nil, err
		}
	}
	person, err := tx.HumanByID(p.personID())
	if errors.Is(err, ErrNotFound) || (err == nil && person.RemovedAt != nil) {
		return Human{}, nil, apierr.Unauthorized()
	}
	if err != nil {
		return Human{}, nil, err
	}
	end, err := keyState(tx, p, now)
	return person, end, err
}

// keyState checks the key and browser login behind p, and returns when the earlier of
// them expires.
func keyState(tx ReadTx, p Principal, now string) (*string, error) {
	if p.KeyID == "" {
		if p.Human != nil {
			return nil, apierr.Unauthorized()
		}
		return nil, nil
	}
	k, err := tx.AccessKeyByID(p.KeyID)
	if errors.Is(err, ErrNotFound) {
		return nil, apierr.Unauthorized()
	}
	if err != nil {
		return nil, err
	}
	if !keyWorks(k, now) || k.HumanID != p.personID() {
		return nil, apierr.Unauthorized()
	}
	end := k.ExpiresAt
	if p.browserDigest != "" {
		l, err := tx.BrowserLoginByDigest(p.browserDigest)
		if errors.Is(err, ErrNotFound) {
			return nil, browserLoginEnded()
		}
		if err != nil {
			return nil, err
		}
		if l.ExpiresAt <= now {
			return nil, browserLoginEnded()
		}
		if end == nil || l.ExpiresAt < *end {
			end = &l.ExpiresAt
		}
	}
	return end, nil
}

// Authenticate resolves a bearer token to a person, through their access key or a
// browser login, or to an agent.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if strings.HasPrefix(token, browserTokenPrefix) {
		return s.authenticateBrowser(ctx, token)
	}
	if strings.HasPrefix(token, delegationPrefix) {
		return s.authenticateDelegation(ctx, token)
	}
	digest := ids.Digest(s.key, token)
	var p Principal
	var used *AccessKey
	err := s.st.Read(ctx, func(tx ReadTx) error {
		// The time is read inside the transaction, so a key that expires while the read
		// waits for the store isn't accepted.
		now := stamp(s.clk.Now())
		switch {
		case strings.HasPrefix(token, accessKeyPrefix):
			k, err := tx.AccessKeyByDigest(digest)
			if err != nil {
				return err
			}
			if !keyWorks(k, now) {
				return ErrNotFound
			}
			h, err := tx.HumanByID(k.HumanID)
			if err != nil {
				return err
			}
			if h.RemovedAt != nil {
				return ErrNotFound
			}
			p.Human, p.KeyID, p.keyUsedBefore, used = &h, k.ID, k.LastUsedAt, &k
		case strings.HasPrefix(token, "aba_"):
			m, err := tx.MemberByTokenDigest(digest)
			if err != nil {
				return err
			}
			if h, err := tx.HumanByID(m.HumanID); err != nil || h.RemovedAt != nil {
				if err != nil && !errors.Is(err, ErrNotFound) {
					return err
				}
				return ErrNotFound
			}
			if m.KeyID != nil {
				k, err := workingKey(tx, *m.KeyID, now)
				if err != nil {
					return err
				}
				p.KeyID, used = k.ID, &k
			}
			p.Agent = &m
		default:
			return ErrNotFound
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Principal{}, apierr.Unauthorized()
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	if used != nil {
		if err := s.recordUse(ctx, p, *used); err != nil {
			return Principal{}, err
		}
	}
	return p, nil
}

// BootstrapOwner makes the server's first person, its admin, with a first access key
// named machine, if the server has no person yet, and returns the key, or "" if a person
// already existed. Keys moved over from before keys had names get machine's name too.
// It never makes a second admin.
func (s *Service) BootstrapOwner(ctx context.Context, name, machine string) (string, error) {
	var token string
	err := s.st.Write(ctx, func(tx Tx) error {
		if err := tx.NameUnnamedKeys(machine); err != nil {
			return err
		}
		n, err := tx.HumanCount()
		if err != nil || n > 0 {
			return err
		}
		now := s.clk.Now()
		h := Human{Name: name, Role: ServerAdmin, CreatedAt: stamp(now)}
		if h.ID, err = s.gen.ID("hum", now); err != nil {
			return err
		}
		if err := tx.InsertHuman(h); err != nil {
			return err
		}
		// The local server's own key doesn't expire: it is kept beside the database it
		// unlocks, so whoever can read it can read the database too.
		token, _, err = s.newKey(tx, h.ID, machine, now, nil, nil)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("create local owner: %w", err)
	}
	return token, nil
}

// newKey stores a new access key for a person and returns its secret, which exists only
// here. It expires at expires (never when nil), or, with idle set, that long after its
// last use.
func (s *Service) newKey(tx Tx, humanID, name string, now time.Time, expires *string, idle *time.Duration) (string, AccessKey, error) {
	token, err := s.gen.Token(strings.TrimSuffix(accessKeyPrefix, "_"))
	if err != nil {
		return "", AccessKey{}, err
	}
	k := AccessKey{HumanID: humanID, Name: name, Digest: ids.Digest(s.key, token), CreatedAt: stamp(now), ExpiresAt: expires}
	if idle != nil {
		k.ExpiresAt = ptr(stamp(now.Add(*idle)))
		k.IdleSeconds = ptr(int64(idle.Seconds()))
	}
	if k.ID, err = s.gen.ID("key", now); err != nil {
		return "", AccessKey{}, err
	}
	if err := tx.InsertAccessKey(k); err != nil {
		return "", AccessKey{}, fmt.Errorf("insert access key: %w", err)
	}
	return token, k, nil
}

// NewServerInvite is a created server invite. Secret is only available at creation.
type NewServerInvite struct {
	Invite ServerInvite
	Secret string
}

// CreateServerInvite makes a server invite that Connect redeems once, within ttl, to
// create one new person as a member. Only a server admin can, with their own access key:
// not an agent, and not a browser.
func (s *Service) CreateServerInvite(ctx context.Context, p Principal, ttl time.Duration) (NewServerInvite, error) {
	if err := requireHuman(p); err != nil {
		return NewServerInvite{}, err
	}
	if p.Browser {
		return NewServerInvite{}, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't invite people; only your own access key can.",
			"Run aboard invite --server in a terminal.")
	}
	if ttl == 0 {
		ttl = DefaultInviteTTL
	}
	if ttl < time.Minute || ttl > MaxInviteTTL {
		return NewServerInvite{}, invalid("An invite works for at least a minute and at most 30 days.", "Pick a lifetime between 1m and 720h.")
	}
	var out NewServerInvite
	err := s.writeAs(ctx, p, func(tx Tx) error {
		// The role is read again here, so a person demoted since they authenticated can't.
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		if h.Role != ServerAdmin {
			return apierr.New(http.StatusForbidden, "server_admin_required",
				"Only an admin of this server can invite people to it.",
				"Ask an admin of this server to run aboard invite --server.")
		}
		now := s.clk.Now()
		secret, err := s.gen.Token(strings.TrimSuffix(invitePrefix, "_"))
		if err != nil {
			return err
		}
		inv := ServerInvite{Digest: ids.Digest(s.key, secret), CreatedBy: h.ID, CreatedAt: stamp(now), ExpiresAt: stamp(now.Add(ttl))}
		if inv.ID, err = s.gen.ID("inv", now); err != nil {
			return err
		}
		if err := tx.InsertServerInvite(inv); err != nil {
			return fmt.Errorf("insert server invite: %w", err)
		}
		out = NewServerInvite{Invite: inv, Secret: secret}
		return nil
	})
	return out, err
}

// ConnectInput redeems a server invite: the invite, and the new person's handle,
// display name and first key's name.
type ConnectInput struct {
	Invite      string
	Handle      string
	DisplayName string
	KeyName     string
}

// Connected is a new person with their first access key, whose secret is only
// available here.
type Connected struct {
	Person Human
	Key    AccessKey
	Token  string
}

func inviteInvalid() *apierr.Error {
	return apierr.New(http.StatusNotFound, "invite_invalid",
		"That invite doesn't work: it is wrong, expired or already used.",
		"Ask an admin of the server for a new invite link.")
}

// validName reports whether s is a handle or key name: lowercase letters, digits and
// single dashes, at most 40 characters.
func validName(s string) bool {
	return s != "" && rules.NormalizeName(s) == s
}

// Connect uses up a server invite and, in the same transaction, creates a new person as
// a server member with their first access key. An invite that is wrong, expired, used or
// made by someone no longer an admin fails the same way. A handle someone has fails with
// handle_taken and leaves the invite unused: a handle never reaches an existing person.
func (s *Service) Connect(ctx context.Context, in ConnectInput) (Connected, error) {
	if !validName(in.Handle) {
		return Connected{}, apierr.New(http.StatusUnprocessableEntity, "handle_invalid",
			fmt.Sprintf("%q can't be a handle: use lowercase letters, digits and single dashes, at most 40 characters.", in.Handle),
			"Pick a handle such as "+rules.NormalizeName(in.Handle)+".")
	}
	if !validName(in.KeyName) {
		return Connected{}, invalid("A key's name uses lowercase letters, digits and single dashes, at most 40 characters.",
			"Name the key after the machine that keeps it, such as maya-laptop.")
	}
	display := strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(display) > 80 {
		return Connected{}, invalid("A display name has at most 80 characters.", "Shorten the display name.")
	}
	if !strings.HasPrefix(in.Invite, invitePrefix) {
		return Connected{}, inviteInvalid()
	}
	var out Connected
	err := s.st.Write(ctx, func(tx Tx) error {
		now := s.clk.Now()
		inv, err := tx.ServerInviteByDigest(ids.Digest(s.key, in.Invite))
		if errors.Is(err, ErrNotFound) {
			return inviteInvalid()
		}
		if err != nil {
			return err
		}
		if inv.UsedAt != nil || inv.ExpiresAt <= stamp(now) {
			return inviteInvalid()
		}
		// The admin's authority is checked again as the invite is used.
		if admin, err := tx.HumanByID(inv.CreatedBy); err != nil || admin.Role != ServerAdmin || admin.RemovedAt != nil {
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			return inviteInvalid()
		}
		if _, err := tx.HumanByName(in.Handle); err == nil {
			return apierr.New(http.StatusConflict, "handle_taken",
				fmt.Sprintf("Someone on this server is already called %s.", in.Handle),
				"Pick another handle with --handle; the invite still works.")
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		h := Human{Name: in.Handle, Role: ServerMember, CreatedAt: stamp(now)}
		if display != "" {
			h.DisplayName = &display
		}
		if h.ID, err = s.gen.ID("hum", now); err != nil {
			return err
		}
		if err := tx.InsertHuman(h); err != nil {
			return fmt.Errorf("insert person: %w", err)
		}
		token, key, err := s.newKey(tx, h.ID, in.KeyName, now, nil, ptr(MachineKeyIdle))
		if err != nil {
			return err
		}
		if used, err := tx.UseServerInvite(inv.ID, stamp(now), h.ID); err != nil || !used {
			if err != nil {
				return err
			}
			return inviteInvalid()
		}
		out = Connected{Person: h, Key: key, Token: token}
		return nil
	})
	return out, err
}
