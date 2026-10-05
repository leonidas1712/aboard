package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
)

// How long access keys work. A key made with aboard keys create expires DefaultKeyTTL
// after it is made unless its person picks between MinKeyTTL and MaxKeyTTL. A machine's
// key from an invite expires only once it has gone MachineKeyIdle without use, so a
// machine in use never has to sign in again and a lost one's key ends by itself. The
// local server's own key doesn't expire: it lives beside the database it unlocks.
const (
	DefaultKeyTTL  = 90 * 24 * time.Hour
	MinKeyTTL      = time.Hour
	MaxKeyTTL      = 365 * 24 * time.Hour
	MachineKeyIdle = 90 * 24 * time.Hour
)

// keyUseEvery is how often a key's use is recorded at most: "last used" is accurate to
// this, and a key that makes many requests costs one write in this time.
const keyUseEvery = time.Minute

// stampLayout is how stamp writes a time.
const stampLayout = "2006-01-02T15:04:05.000Z"

// keyUses remembers when each key's use was last recorded, so requests within
// keyUseEvery of it don't write again.
type keyUses struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// due reports whether a use of key at now should be recorded: when neither this server
// nor the store (stored, the key's LastUsedAt) has recorded one within keyUseEvery. It
// then counts the use as recorded.
func (u *keyUses) due(key string, stored *string, now time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if t, ok := u.last[key]; ok && now.Sub(t) < keyUseEvery {
		return false
	}
	if stored != nil {
		if t, err := time.Parse(stampLayout, *stored); err == nil && now.Sub(t) < keyUseEvery {
			u.last[key] = t
			return false
		}
	}
	u.last[key] = now
	return true
}

// recordUse records that k, or a browser login or agent token it started, was used by p,
// moving the expiry of a key that expires once unused. It writes at most once per
// keyUseEvery per key. It fails only when p's credential ended meanwhile; a store that
// can't record the use is logged and the request goes on.
func (s *Service) recordUse(ctx context.Context, p Principal, k AccessKey) error {
	if !s.uses.due(k.ID, k.LastUsedAt, s.clk.Now()) {
		return nil
	}
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		var expires *string
		if k.IdleSeconds != nil {
			expires = ptr(stamp(now.Add(time.Duration(*k.IdleSeconds) * time.Second)))
		}
		return tx.UseAccessKey(k.ID, stamp(now), expires)
	})
	if _, ok := apierr.As(err); ok {
		return err
	}
	if err != nil {
		s.log.Warn("record key use", "key", k.ID, "error", err)
	}
	return nil
}

// credentialsKey is the Notifier key that changes when a credential of the person ends
// before its time: one of their keys revoked, or their browser logins ended. Long reads
// and streams watch it to end with their credential.
func credentialsKey(humanID string) string { return "credentials/" + humanID }

// personID is the person p acts as or for.
func (p Principal) personID() string {
	if p.Human != nil {
		return p.Human.ID
	}
	if p.Agent != nil {
		return p.Agent.HumanID
	}
	return ""
}

// credentialEnd is what a long wait watches so that it ends with the credential it was
// made with. A nil channel never fires.
type credentialEnd struct {
	changed <-chan struct{}  // a credential of the person ended: check again
	expires <-chan time.Time // the credential expires
}

// watchCredential checks that p's credential still works, and returns what to watch for
// it to end. Call it before each wait, and again once either channel fires.
func (s *Service) watchCredential(ctx context.Context, p Principal) (credentialEnd, error) {
	w := credentialEnd{changed: s.notify.Watch(credentialsKey(p.personID()))}
	var end *string
	var now time.Time
	err := s.st.Read(ctx, func(tx ReadTx) error {
		now = s.clk.Now()
		var err error
		end, err = credentialState(tx, p, stamp(now))
		return err
	})
	if err != nil {
		return credentialEnd{}, err
	}
	if end != nil {
		if t, err := time.Parse(stampLayout, *end); err == nil {
			w.expires = s.clk.After(t.Sub(now))
		}
	}
	return w, nil
}

// requireOwnKey refuses everything but a person's own access key: keys are managed only
// with a key, never by an agent or from a browser.
func requireOwnKey(p Principal) error {
	if err := requireHuman(p); err != nil {
		return err
	}
	if p.Browser {
		return apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't manage access keys; only your own access key can.",
			"Run aboard keys in a terminal.")
	}
	if p.KeyID == "" {
		return apierr.Unauthorized()
	}
	return nil
}

// KeyView is an access key as its person or an admin sees it, with its state at the
// time it was read: working, revoked or expired.
type KeyView struct {
	KeyUsage
	State string
}

// Key states.
const (
	KeyWorking = "working"
	KeyRevoked = "revoked"
	KeyExpired = "expired"
)

func viewKey(u KeyUsage, now string) KeyView {
	state := KeyWorking
	switch {
	case u.RevokedAt != nil:
		state = KeyRevoked
	case !keyWorks(u.AccessKey, now):
		state = KeyExpired
	}
	return KeyView{KeyUsage: u, State: state}
}

// KeyList is a person's access keys, and the key the request that listed them came with.
type KeyList struct {
	Person Human
	Keys   []KeyView
	// CurrentKeyID is the caller's key, and CurrentKeyPreviousUse when it was used
	// before the request, to a minute; nil on its first use.
	CurrentKeyID          string
	CurrentKeyPreviousUse *string
}

func personNotFound(handle string) *apierr.Error {
	return apierr.New(http.StatusNotFound, "person_not_found", fmt.Sprintf("Nobody on this server is called %s.", handle),
		"Check the handle; aboard keys --person takes a person's handle.")
}

func othersKeysRefused() *apierr.Error {
	return apierr.New(http.StatusForbidden, "server_admin_required",
		"Only an admin of this server can list another person's keys.",
		"Run aboard keys without --person to see your own keys.")
}

// ListKeys returns a person's access keys, oldest first: the caller's own, or, for a
// server admin, the person called handle.
func (s *Service) ListKeys(ctx context.Context, p Principal, handle string) (KeyList, error) {
	if err := requireOwnKey(p); err != nil {
		return KeyList{}, err
	}
	out := KeyList{CurrentKeyID: p.KeyID, CurrentKeyPreviousUse: p.keyUsedBefore}
	err := s.st.Read(ctx, func(tx ReadTx) error {
		me, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		person := me
		if handle != "" && handle != me.Name {
			if me.Role != ServerAdmin {
				return othersKeysRefused()
			}
			if person, err = tx.HumanByName(handle); errors.Is(err, ErrNotFound) {
				return personNotFound(handle)
			} else if err != nil {
				return err
			}
		}
		now := stamp(s.clk.Now())
		keys, err := tx.KeysOf(person.ID, now)
		if err != nil {
			return err
		}
		out.Person = person
		for _, k := range keys {
			out.Keys = append(out.Keys, viewKey(k, now))
		}
		return nil
	})
	return out, err
}

// NewKey is a created access key. Token, its secret, exists only here.
type NewKey struct {
	Key   KeyView
	Token string
}

// CreateKey makes an access key for the caller, named name, that expires ttl after it
// is made (DefaultKeyTTL when zero). Only a person's own key can: never an agent, never
// a browser, and never for anyone else.
func (s *Service) CreateKey(ctx context.Context, p Principal, name string, ttl time.Duration) (NewKey, error) {
	if err := requireOwnKey(p); err != nil {
		return NewKey{}, err
	}
	if !validName(name) {
		return NewKey{}, invalid("A key's name uses lowercase letters, digits and single dashes, at most 40 characters.",
			"Name the key after where it is kept or what uses it, such as phone or nightly-summary.")
	}
	if ttl == 0 {
		ttl = DefaultKeyTTL
	}
	if ttl < MinKeyTTL || ttl > MaxKeyTTL {
		return NewKey{}, invalid("A key works for at least an hour and at most 365 days.", "Pick an expiry between 1h and 365d.")
	}
	var out NewKey
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := s.clk.Now()
		keys, err := tx.KeysOf(p.Human.ID, stamp(now))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if k.Name == name && keyWorks(k.AccessKey, stamp(now)) {
				return apierr.New(http.StatusConflict, "key_name_taken",
					fmt.Sprintf("You already have a working key called %s.", name),
					"Pick another name, or revoke that key first with aboard keys revoke "+name+".")
			}
		}
		token, k, err := s.newKey(tx, p.Human.ID, name, now, ptr(stamp(now.Add(ttl))), nil)
		if err != nil {
			return err
		}
		out = NewKey{Key: viewKey(KeyUsage{AccessKey: k}, stamp(now)), Token: token}
		return nil
	})
	return out, err
}

func keyNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, "key_not_found", "There is no such key that you can revoke.",
		"List the keys with aboard keys, and name one of them.")
}

// RevokeKey revokes an access key: one of the caller's own, or, for a server admin,
// anyone's. Everything the key started (browser logins, agent tokens) stops with it, and
// long reads and streams made with them end. A key the caller can't revoke is reported
// as not found, so key ids don't leak. Revoking a revoked key changes nothing.
func (s *Service) RevokeKey(ctx context.Context, p Principal, keyID string) (KeyView, error) {
	if err := requireOwnKey(p); err != nil {
		return KeyView{}, err
	}
	var out KeyView
	var owner string
	err := s.writeAs(ctx, p, func(tx Tx) error {
		k, err := tx.AccessKeyByID(keyID)
		if errors.Is(err, ErrNotFound) {
			return keyNotFound()
		}
		if err != nil {
			return err
		}
		if k.HumanID != p.Human.ID {
			// The role is read here, in the write, so an admin demoted since they
			// authenticated can't.
			me, err := tx.HumanByID(p.Human.ID)
			if err != nil {
				return err
			}
			if me.Role != ServerAdmin {
				return keyNotFound()
			}
		}
		now := stamp(s.clk.Now())
		if err := tx.RevokeAccessKey(k.ID, now); err != nil {
			return fmt.Errorf("revoke key %s: %w", k.ID, err)
		}
		keys, err := tx.KeysOf(k.HumanID, now)
		if err != nil {
			return err
		}
		for _, u := range keys {
			if u.ID == k.ID {
				out = viewKey(u, now)
			}
		}
		owner = k.HumanID
		return nil
	})
	if err != nil {
		return KeyView{}, err
	}
	s.notify.Changed(credentialsKey(owner))
	return out, nil
}
