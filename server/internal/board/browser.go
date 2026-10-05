package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// LoginCodeTTL is how long a browser login code works.
const LoginCodeTTL = 60 * time.Second

// BrowserTokenTTL is how long a browser token works.
const BrowserTokenTTL = 30 * 24 * time.Hour

// browserTokenPrefix starts every browser token, the way abh_ and aba_ start human
// and agent tokens.
const browserTokenPrefix = "abb_"

// loginCodes keeps one-time login codes in memory, by digest. A code lasts a minute, so
// one lost to a restart costs only running aboard open again. Browser tokens, which last
// much longer, are kept in the store.
type loginCodes struct {
	mu    sync.Mutex
	codes map[string]loginCode // digest of a login code → who it logs in
}

type loginCode struct {
	human   Human
	keyID   string // the access key that asked for the code
	expires time.Time
}

func newLoginCodes() *loginCodes {
	return &loginCodes{codes: map[string]loginCode{}}
}

// prune drops expired codes, so the map stays as small as the codes in use.
func (c *loginCodes) prune(now time.Time) {
	for k, l := range c.codes {
		if !now.Before(l.expires) {
			delete(c.codes, k)
		}
	}
}

// LoginCode is a one-time code that logs a browser in as the human who asked for it.
type LoginCode struct {
	Code      string
	ExpiresAt string
}

// CreateLoginCode makes a code that CreateBrowserToken exchanges once, within
// LoginCodeTTL, for a browser token. Only a human's own login can ask for one: a
// browser that could ask would keep itself logged in past BrowserTokenTTL.
func (s *Service) CreateLoginCode(_ context.Context, p Principal) (LoginCode, error) {
	if err := requireHuman(p); err != nil {
		return LoginCode{}, err
	}
	if p.Browser {
		return LoginCode{}, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't make a login link; only your own login can.",
			"Run aboard open in a terminal.")
	}
	code, err := s.gen.Token("abl")
	if err != nil {
		return LoginCode{}, err
	}
	now := s.clk.Now()
	expires := now.Add(LoginCodeTTL)
	s.codes.mu.Lock()
	defer s.codes.mu.Unlock()
	s.codes.prune(now)
	s.codes.codes[ids.Digest(s.key, code)] = loginCode{human: *p.Human, keyID: p.KeyID, expires: expires}
	return LoginCode{Code: code, ExpiresAt: stamp(expires)}, nil
}

// loginCodeInvalid is the error for a login code that is wrong, expired or used.
func loginCodeInvalid() *apierr.Error {
	return apierr.New(http.StatusNotFound, "login_code_invalid",
		"That login link doesn't work: it is wrong, expired or already used.",
		"Run aboard open in a terminal to get a new one.")
}

// CreateBrowserToken uses up a login code and returns a new browser token for its
// human, and when the token ends. A browser token acts as its human, with the human's
// permissions. A code works only once, even if this call fails. The store keeps only the
// token's digest, so the login lasts BrowserTokenTTL across restarts of the server.
func (s *Service) CreateBrowserToken(ctx context.Context, code string) (token string, expires time.Time, err error) {
	digest := ids.Digest(s.key, code)
	now := s.clk.Now()
	s.codes.mu.Lock()
	l, ok := s.codes.codes[digest]
	delete(s.codes.codes, digest)
	s.codes.mu.Unlock()
	if !ok || !now.Before(l.expires) {
		return "", time.Time{}, loginCodeInvalid()
	}
	if token, err = s.gen.Token("abb"); err != nil {
		return "", time.Time{}, err
	}
	err = s.st.Write(ctx, func(tx Tx) error {
		// The time is read again once the transaction holds the write lock, so a code or
		// key that ends while the exchange waits for it doesn't work.
		now := s.clk.Now()
		if !now.Before(l.expires) {
			return loginCodeInvalid()
		}
		expires = now.Add(BrowserTokenTTL)
		// The key that asked for the code must still work when the code is used.
		if err := stillValid(tx, Principal{Human: &l.human, KeyID: l.keyID}, stamp(now)); err != nil {
			if _, ok := apierr.As(err); ok {
				return loginCodeInvalid()
			}
			return err
		}
		if err := tx.DeleteExpiredBrowserLogins(stamp(now)); err != nil {
			return err
		}
		return tx.InsertBrowserLogin(BrowserLogin{
			TokenDigest: ids.Digest(s.key, token), HumanID: l.human.ID, KeyID: l.keyID, CreatedAt: stamp(now), ExpiresAt: stamp(expires),
		})
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("save browser login: %w", err)
	}
	return token, expires, nil
}

// browserLoginEnded is the error for a browser token that is wrong, expired or ended.
func browserLoginEnded() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "unauthorized",
		"This browser isn't logged in to Aboard, or its login has ended.",
		"Run aboard open in a terminal to log in again.")
}

// authenticateBrowser resolves a browser token to the human it acts as.
func (s *Service) authenticateBrowser(ctx context.Context, token string) (Principal, error) {
	var h Human
	var key *AccessKey
	digest := ids.Digest(s.key, token)
	err := s.st.Read(ctx, func(tx ReadTx) error {
		l, err := tx.BrowserLoginByDigest(digest)
		if err != nil {
			return err
		}
		now := stamp(s.clk.Now())
		if l.ExpiresAt <= now {
			return ErrNotFound
		}
		// A browser login never outlives the access key that started it.
		if l.KeyID != "" {
			k, err := workingKey(tx, l.KeyID, now)
			if err != nil {
				return err
			}
			key = &k
		}
		h, err = tx.HumanByID(l.HumanID)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return Principal{}, browserLoginEnded()
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate browser: %w", err)
	}
	p := Principal{Human: &h, Browser: true, browserDigest: digest}
	if key != nil {
		p.KeyID = key.ID
		if err := s.recordUse(ctx, p, *key); err != nil {
			return Principal{}, err
		}
	}
	return p, nil
}

// EndBrowserLogins ends every browser login of the calling human and returns how many
// had not yet expired. Only the human's own login can: a browser that could would log
// the person's other browsers out.
func (s *Service) EndBrowserLogins(ctx context.Context, p Principal) (int, error) {
	if err := requireHuman(p); err != nil {
		return 0, err
	}
	if p.Browser {
		return 0, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't log browsers out; only your own login can.",
			"Run aboard logout --browsers in a terminal.")
	}
	var n int
	err := s.writeAs(ctx, p, func(tx Tx) error {
		var err error
		n, err = tx.DeleteBrowserLogins(p.Human.ID, stamp(s.clk.Now()))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("end browser logins: %w", err)
	}
	// Streams the ended browsers hold notice and end.
	s.notify.Changed(credentialsKey(p.Human.ID))
	return n, nil
}
