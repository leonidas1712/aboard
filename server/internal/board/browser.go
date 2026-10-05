package board

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// LoginCodeTTL is how long a browser login code works.
const LoginCodeTTL = 60 * time.Second

// BrowserTokenTTL is how long a browser session works at most: its key ending first
// ends it sooner.
const BrowserTokenTTL = 30 * 24 * time.Hour

// browserTokenPrefix starts every browser session's secret, the way abh_ and aba_ start
// access keys and agent tokens.
const browserTokenPrefix = "abb_"

// How a browser session started: from aboard open's one-time code, or from an access
// key pasted on the login page.
const (
	SessionFromLoginCode = "login_code"
	SessionFromKey       = "access_key"
)

// loginCodes keeps one-time login codes in memory, by digest. A code lasts a minute, so
// one lost to a restart costs only running aboard open again. Browser sessions, which
// last much longer, are kept in the store.
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

// CreateLoginCode makes a code that StartBrowserSession exchanges once, within
// LoginCodeTTL, for a browser session. Only a human's own key can ask for one: a
// browser that could ask would keep itself signed in past BrowserTokenTTL.
func (s *Service) CreateLoginCode(_ context.Context, p Principal) (LoginCode, error) {
	if err := requireHuman(p); err != nil {
		return LoginCode{}, err
	}
	if p.Browser {
		return LoginCode{}, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't make a login link; only your own access key can.",
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

// accessKeyInvalid is the error for a pasted access key that is wrong, revoked or
// expired. It never says which.
func accessKeyInvalid() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "access_key_invalid",
		"That access key doesn't work: it is wrong, revoked or expired.",
		"Check you pasted the whole key, starting with abh_. Make a new one with aboard keys create <name> in a terminal.")
}

// BrowserSession is a browser login as its person sees it: never its secret.
type BrowserSession struct {
	ID          string
	KeyID       string
	KeyName     string
	StartedWith string
	CreatedAt   string
	ExpiresAt   string
}

// SessionStart is what signs a browser in: exactly one of a login code from aboard
// open, an access key pasted on the login page, or a browser token a page kept in its
// own storage before sessions moved into cookies.
type SessionStart struct {
	Code  string
	Key   string
	Token string
}

// StartedSession is a browser session that just started, with its person and its
// secret, which goes only into the browser's cookie.
type StartedSession struct {
	Session BrowserSession
	Person  Human
	Token   string
}

// sessionExpiry is when a session started at now from key k ends: BrowserTokenTTL on,
// or k's expiry if that is sooner. A key that expires only once unused moves its expiry
// with every use, the session's own included, so it doesn't shorten the session.
func sessionExpiry(now time.Time, k AccessKey) string {
	end := stamp(now.Add(BrowserTokenTTL))
	if k.IdleSeconds == nil && k.ExpiresAt != nil && *k.ExpiresAt < end {
		return *k.ExpiresAt
	}
	return end
}

// StartBrowserSession signs a browser in with what in carries and returns the session
// and its secret. A code works once, even if this call fails; a pasted key is checked
// and never kept; a stored browser token keeps its session, which only moves from the
// page's storage into a cookie.
func (s *Service) StartBrowserSession(ctx context.Context, in SessionStart) (StartedSession, error) {
	given := 0
	for _, v := range []string{in.Code, in.Key, in.Token} {
		if v != "" {
			given++
		}
	}
	if given != 1 {
		return StartedSession{}, apierr.New(http.StatusBadRequest, "invalid_request",
			"Send exactly one of code, key and token.", "Sign in with a login code from aboard open, or paste an access key.")
	}
	switch {
	case in.Code != "":
		return s.startFromCode(ctx, in.Code)
	case in.Key != "":
		return s.startFromKey(ctx, in.Key)
	}
	return s.keepStoredToken(ctx, in.Token)
}

// CreateBrowserToken uses up a login code and returns a new browser token for its
// human, and when it ends, for a page that keeps the token itself. StartBrowserSession
// does the same and keeps the token in a cookie instead.
func (s *Service) CreateBrowserToken(ctx context.Context, code string) (token string, expires time.Time, err error) {
	started, err := s.startFromCode(ctx, code)
	if err != nil {
		return "", time.Time{}, err
	}
	expires, err = time.Parse(stampLayout, started.Session.ExpiresAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read session expiry: %w", err)
	}
	return started.Token, expires, nil
}

// startFromCode uses up a login code and starts a session for the key that asked for it.
func (s *Service) startFromCode(ctx context.Context, code string) (StartedSession, error) {
	digest := ids.Digest(s.key, code)
	now := s.clk.Now()
	s.codes.mu.Lock()
	l, ok := s.codes.codes[digest]
	delete(s.codes.codes, digest)
	s.codes.mu.Unlock()
	if !ok || !now.Before(l.expires) {
		return StartedSession{}, loginCodeInvalid()
	}
	var out StartedSession
	err := s.st.Write(ctx, func(tx Tx) error {
		// The time is read again once the transaction holds the write lock, so a code or
		// key that ends while the exchange waits for it doesn't work.
		now := s.clk.Now()
		if !now.Before(l.expires) {
			return loginCodeInvalid()
		}
		// The key that asked for the code must still work when the code is used.
		if err := stillValid(tx, Principal{Human: &l.human, KeyID: l.keyID}, stamp(now)); err != nil {
			if _, ok := apierr.As(err); ok {
				return loginCodeInvalid()
			}
			return err
		}
		k, err := tx.AccessKeyByID(l.keyID)
		if err != nil {
			return err
		}
		out, err = s.insertSession(tx, now, k, SessionFromLoginCode)
		return err
	})
	if err != nil {
		return StartedSession{}, fmt.Errorf("start browser session: %w", err)
	}
	return out, nil
}

// startFromKey checks a pasted access key and starts a session for it. Only the
// session's digest is stored; the key is used for the lookup and dropped.
func (s *Service) startFromKey(ctx context.Context, key string) (StartedSession, error) {
	if !strings.HasPrefix(key, accessKeyPrefix) {
		return StartedSession{}, accessKeyInvalid()
	}
	digest := ids.Digest(s.key, key)
	var out StartedSession
	var used AccessKey
	err := s.st.Write(ctx, func(tx Tx) error {
		now := s.clk.Now()
		k, err := tx.AccessKeyByDigest(digest)
		if errors.Is(err, ErrNotFound) {
			return accessKeyInvalid()
		}
		if err != nil {
			return err
		}
		if !keyWorks(k, stamp(now)) {
			return accessKeyInvalid()
		}
		used = k
		out, err = s.insertSession(tx, now, k, SessionFromKey)
		return err
	})
	if err != nil {
		return StartedSession{}, fmt.Errorf("start browser session: %w", err)
	}
	// Signing in is a use of the key, as any request made with it is.
	if err := s.recordUse(ctx, Principal{Human: &out.Person, KeyID: used.ID}, used); err != nil {
		return StartedSession{}, err
	}
	return out, nil
}

// insertSession stores a new session for key k and returns it with its secret.
func (s *Service) insertSession(tx Tx, now time.Time, k AccessKey, startedWith string) (StartedSession, error) {
	h, err := tx.HumanByID(k.HumanID)
	if err != nil {
		return StartedSession{}, err
	}
	token, err := s.gen.Token("abb")
	if err != nil {
		return StartedSession{}, err
	}
	id, err := s.gen.ID("ses", now)
	if err != nil {
		return StartedSession{}, err
	}
	if err := tx.DeleteExpiredBrowserLogins(stamp(now)); err != nil {
		return StartedSession{}, err
	}
	l := BrowserLogin{
		ID: id, TokenDigest: ids.Digest(s.key, token), HumanID: h.ID, KeyID: k.ID, StartedWith: startedWith,
		CreatedAt: stamp(now), ExpiresAt: sessionExpiry(now, k),
	}
	if err := tx.InsertBrowserLogin(l); err != nil {
		return StartedSession{}, err
	}
	return StartedSession{Session: viewSession(l, k.Name), Person: h, Token: token}, nil
}

// keepStoredToken checks a browser token a page kept in its own storage and returns its
// session unchanged, so the page can move it into a cookie.
func (s *Service) keepStoredToken(ctx context.Context, token string) (StartedSession, error) {
	if !strings.HasPrefix(token, browserTokenPrefix) {
		return StartedSession{}, browserLoginEnded()
	}
	p, err := s.authenticateBrowser(ctx, token)
	if err != nil {
		return StartedSession{}, err
	}
	cur, err := s.CurrentBrowserSession(ctx, p)
	if err != nil {
		return StartedSession{}, err
	}
	return StartedSession{Session: cur.Session, Person: cur.Person, Token: token}, nil
}

func viewSession(l BrowserLogin, keyName string) BrowserSession {
	return BrowserSession{
		ID: l.ID, KeyID: l.KeyID, KeyName: keyName, StartedWith: l.StartedWith, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}
}

// CSRFToken is the token a browser session sends with each write it makes, proving the
// write comes from a page that could read the session's own responses. It is derived
// from the session's secret, so it works only with that session and needs no storage.
func (s *Service) CSRFToken(sessionToken string) string {
	return ids.Digest(s.key, "csrf:"+sessionToken)
}

// browserLoginEnded is the error for a browser session that is wrong, expired or ended.
func browserLoginEnded() *apierr.Error {
	return apierr.New(http.StatusUnauthorized, "unauthorized",
		"This browser isn't signed in to Aboard, or its session has ended.",
		"Sign in again: paste an access key on the board view's login page, or run aboard open in a terminal.")
}

func browserSessionRequired() *apierr.Error {
	return apierr.New(http.StatusForbidden, "browser_session_required",
		"Only a browser session can do this, for itself.",
		"To end a browser session from a terminal, run aboard keys sessions.")
}

// authenticateBrowser resolves a browser session's secret to the human it acts as.
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
		// A browser session never outlives the access key that started it.
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

// CurrentSession is the browser session a request came with, and its person.
type CurrentSession struct {
	Session BrowserSession
	Person  Human
}

// CurrentBrowserSession returns the session p, a browser, is signed in with.
func (s *Service) CurrentBrowserSession(ctx context.Context, p Principal) (CurrentSession, error) {
	if p.Human == nil || !p.Browser {
		return CurrentSession{}, browserSessionRequired()
	}
	var out CurrentSession
	err := s.st.Read(ctx, func(tx ReadTx) error {
		if err := stillValid(tx, p, stamp(s.clk.Now())); err != nil {
			return err
		}
		l, err := tx.BrowserLoginByDigest(p.browserDigest)
		if err != nil {
			return err
		}
		k, err := tx.AccessKeyByID(l.KeyID)
		if err != nil {
			return err
		}
		h, err := tx.HumanByID(l.HumanID)
		if err != nil {
			return err
		}
		out = CurrentSession{Session: viewSession(l, k.Name), Person: h}
		return nil
	})
	if err != nil {
		return CurrentSession{}, fmt.Errorf("read browser session: %w", err)
	}
	return out, nil
}

// SignOut ends the browser session p is signed in with, and only that one.
func (s *Service) SignOut(ctx context.Context, p Principal) (BrowserSession, error) {
	if p.Human == nil || !p.Browser {
		return BrowserSession{}, browserSessionRequired()
	}
	var out BrowserSession
	err := s.writeAs(ctx, p, func(tx Tx) error {
		l, err := tx.BrowserLoginByDigest(p.browserDigest)
		if err != nil {
			return err
		}
		k, err := tx.AccessKeyByID(l.KeyID)
		if err != nil {
			return err
		}
		out = viewSession(l, k.Name)
		return tx.DeleteBrowserLogin(l.ID)
	})
	if err != nil {
		return BrowserSession{}, fmt.Errorf("sign out: %w", err)
	}
	// The session's open stream notices and ends.
	s.notify.Changed(credentialsKey(p.Human.ID))
	return out, nil
}

// SessionList is a person's browser sessions that still work.
type SessionList struct {
	Person   Human
	Sessions []BrowserSession
}

func browserSessionNotFound() *apierr.Error {
	return apierr.New(http.StatusNotFound, "browser_session_not_found",
		"There is no such browser session that you can end: it is wrong, or it already ended.",
		"List your browser sessions with aboard keys sessions, and name one of them.")
}

// ListBrowserSessions returns the caller's browser sessions that still work, newest
// first: all of them, or those of the key keyID. Only a person's own key can list them.
func (s *Service) ListBrowserSessions(ctx context.Context, p Principal, keyID string) (SessionList, error) {
	if err := requireOwnKey(p); err != nil {
		return SessionList{}, err
	}
	var out SessionList
	err := s.st.Read(ctx, func(tx ReadTx) error {
		now := stamp(s.clk.Now())
		if err := stillValid(tx, p, now); err != nil {
			return err
		}
		h, err := tx.HumanByID(p.Human.ID)
		if err != nil {
			return err
		}
		out.Person = h
		keys, err := tx.KeysOf(h.ID, now)
		if err != nil {
			return err
		}
		working := map[string]string{} // key id → name, for the keys that still work
		found := keyID == ""
		for _, k := range keys {
			if k.ID == keyID {
				found = true
			}
			if keyWorks(k.AccessKey, now) {
				working[k.ID] = k.Name
			}
		}
		if !found {
			return apierr.New(http.StatusNotFound, "key_not_found", "You have no such key.",
				"List your keys with aboard keys, and name one of them.")
		}
		logins, err := tx.BrowserLoginsOf(h.ID, now)
		if err != nil {
			return err
		}
		for _, l := range logins {
			name, ok := working[l.KeyID]
			if !ok || (keyID != "" && l.KeyID != keyID) {
				continue
			}
			out.Sessions = append(out.Sessions, viewSession(l, name))
		}
		return nil
	})
	if err != nil {
		return SessionList{}, err
	}
	return out, nil
}

// EndBrowserSession ends one of the caller's browser sessions by id. Only a person's own
// key can; a session that isn't theirs, or that already ended, is reported as not found.
func (s *Service) EndBrowserSession(ctx context.Context, p Principal, id string) (BrowserSession, error) {
	if err := requireOwnKey(p); err != nil {
		return BrowserSession{}, err
	}
	var out BrowserSession
	err := s.writeAs(ctx, p, func(tx Tx) error {
		now := stamp(s.clk.Now())
		l, err := tx.BrowserLoginByID(id)
		if errors.Is(err, ErrNotFound) {
			return browserSessionNotFound()
		}
		if err != nil {
			return err
		}
		if l.HumanID != p.Human.ID || l.ExpiresAt <= now {
			return browserSessionNotFound()
		}
		k, err := tx.AccessKeyByID(l.KeyID)
		if err != nil {
			return err
		}
		if !keyWorks(k, now) {
			return browserSessionNotFound()
		}
		out = viewSession(l, k.Name)
		return tx.DeleteBrowserLogin(l.ID)
	})
	if err != nil {
		return BrowserSession{}, err
	}
	s.notify.Changed(credentialsKey(p.Human.ID))
	return out, nil
}

// EndBrowserLogins ends every browser session of the calling human and returns how
// many had not yet expired. Only the human's own key can: a browser that could would
// sign the person's other browsers out.
func (s *Service) EndBrowserLogins(ctx context.Context, p Principal) (int, error) {
	if err := requireHuman(p); err != nil {
		return 0, err
	}
	if p.Browser {
		return 0, apierr.New(http.StatusForbidden, "human_token_required",
			"A browser can't sign browsers out; only your own access key can.",
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
