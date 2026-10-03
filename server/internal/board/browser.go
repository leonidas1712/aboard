package board

import (
	"context"
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

// browserLogins keeps login codes and browser tokens in memory, by digest. They are
// bookkeeping, not part of the record: none of them is an event, and all of them end
// when the server stops, which is how a person logs every browser out.
type browserLogins struct {
	mu     sync.Mutex
	codes  map[string]browserLogin // digest of a login code → who it logs in
	tokens map[string]browserLogin // digest of a browser token → who it acts as
}

type browserLogin struct {
	human   Human
	expires time.Time
}

func newBrowserLogins() *browserLogins {
	return &browserLogins{codes: map[string]browserLogin{}, tokens: map[string]browserLogin{}}
}

// prune drops expired entries, so the maps stay as small as the logins in use.
func prune(m map[string]browserLogin, now time.Time) {
	for k, l := range m {
		if !now.Before(l.expires) {
			delete(m, k)
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
	s.logins.mu.Lock()
	defer s.logins.mu.Unlock()
	prune(s.logins.codes, now)
	s.logins.codes[ids.Digest(s.key, code)] = browserLogin{human: *p.Human, expires: expires}
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
// permissions. A code works only once, even if this call fails.
func (s *Service) CreateBrowserToken(_ context.Context, code string) (token string, expires time.Time, err error) {
	digest := ids.Digest(s.key, code)
	now := s.clk.Now()
	s.logins.mu.Lock()
	defer s.logins.mu.Unlock()
	l, ok := s.logins.codes[digest]
	delete(s.logins.codes, digest)
	if !ok || !now.Before(l.expires) {
		return "", time.Time{}, loginCodeInvalid()
	}
	if token, err = s.gen.Token("abb"); err != nil {
		return "", time.Time{}, err
	}
	expires = now.Add(BrowserTokenTTL)
	prune(s.logins.tokens, now)
	s.logins.tokens[ids.Digest(s.key, token)] = browserLogin{human: l.human, expires: expires}
	return token, expires, nil
}

// authenticateBrowser resolves a browser token to the human it acts as.
func (s *Service) authenticateBrowser(token string) (Principal, error) {
	s.logins.mu.Lock()
	defer s.logins.mu.Unlock()
	l, ok := s.logins.tokens[ids.Digest(s.key, token)]
	if !ok || !s.clk.Now().Before(l.expires) {
		return Principal{}, apierr.New(http.StatusUnauthorized, "unauthorized",
			"This browser isn't logged in to Aboard, or its login has ended.",
			"Run aboard open in a terminal to log in again.")
	}
	h := l.human
	return Principal{Human: &h, Browser: true}, nil
}
