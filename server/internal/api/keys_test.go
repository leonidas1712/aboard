package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// createKey makes an access key called name with token, lasting ttl seconds (the default
// when 0), and returns its secret.
func (s *testServer) createKey(token, name string, ttl int) *api.CreateKeyResponse {
	s.t.Helper()
	req := api.CreateKeyRequest{Name: name}
	if ttl != 0 {
		req.TtlSeconds = &ttl
	}
	r, err := s.client(token).CreateKeyWithResponse(context.Background(), nil, req)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) newKey(token, name string) (id, secret string) {
	s.t.Helper()
	r := s.createKey(token, name, 0)
	mustStatus(s.t, r, nil, 201)
	return r.JSON201.Id, r.JSON201.Token
}

func (s *testServer) keys(token, person string) *api.ListKeysResponse {
	s.t.Helper()
	var params api.ListKeysParams
	if person != "" {
		params.Person = &person
	}
	r, err := s.client(token).ListKeysWithResponse(context.Background(), &params)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) revokeKey(token, id string) *api.RevokeKeyResponse {
	s.t.Helper()
	r, err := s.client(token).RevokeKeyWithResponse(context.Background(), id, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// keyNamed finds a key in a listing by name.
func keyNamed(t *testing.T, r *api.ListKeysResponse, name string) api.AccessKey {
	t.Helper()
	mustStatus(t, r, nil, 200)
	for _, k := range r.JSON200.Keys {
		if k.Name == name {
			return k
		}
	}
	t.Fatalf("no key %s in %s", name, bodyOf(r))
	return api.AccessKey{}
}

func (s *testServer) works(token string, want bool) {
	s.t.Helper()
	r, err := s.client(token).GetMeWithResponse(context.Background())
	if want {
		mustStatus(s.t, r, err, 200)
	} else {
		mustStatus(s.t, r, err, 401)
	}
}

// A person makes a key, sees it listed without its secret, and revokes it; the key stops
// working and their other key doesn't.
func TestAPersonCreatesListsAndRevokesKeys(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	r := s.createKey(s.owner, "phone", 0)
	mustStatus(t, r, nil, 201)
	phone := r.JSON201
	if !strings.HasPrefix(phone.Token, "abh_") || phone.ExpiresAt == nil ||
		!phone.ExpiresAt.Equal(s.clock.Now().Add(90*24*time.Hour)) || phone.IdleExpirySeconds != nil {
		t.Fatalf("a new key: %s", bodyOf(r))
	}
	s.works(phone.Token, true)

	list := s.keys(s.owner, "")
	if strings.Contains(bodyOf(list), phone.Token) {
		t.Fatalf("the listing shows a secret: %s", bodyOf(list))
	}
	laptop := keyNamed(t, list, "laptop")
	if list.JSON200.CurrentKeyId != laptop.Id || list.JSON200.Person.Handle != "alex" || len(list.JSON200.Keys) != 2 ||
		laptop.ExpiresAt != nil || *laptop.State != api.AccessKeyStateWorking {
		t.Fatalf("the listing: %s", bodyOf(list))
	}

	revoked := s.revokeKey(s.owner, phone.Id)
	mustStatus(t, revoked, nil, 200)
	if *revoked.JSON200.State != api.AccessKeyStateRevoked || revoked.JSON200.RevokedAt == nil {
		t.Fatalf("revoking: %s", bodyOf(revoked))
	}
	s.works(phone.Token, false)
	s.works(s.owner, true)
	// Revoking it again changes nothing.
	again := s.revokeKey(s.owner, phone.Id)
	mustStatus(t, again, nil, 200)
	if !again.JSON200.RevokedAt.Equal(*revoked.JSON200.RevokedAt) {
		t.Fatalf("revoking twice moved the time: %s", bodyOf(again))
	}
	if k := keyNamed(t, s.keys(s.owner, ""), "phone"); *k.State != api.AccessKeyStateRevoked {
		t.Fatalf("the revoked key listed: %+v", k)
	}
}

// Names say where each key is kept, so a working key's name isn't given twice; a revoked
// key's name is free again. A key works for an hour to a year.
func TestKeyNamesAndLifetimes(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	id, _ := s.newKey(s.owner, "phone")
	wantCode(t, s.createKey(s.owner, "phone", 0), 409, "key_name_taken")
	wantCode(t, s.createKey(s.owner, "laptop", 0), 409, "key_name_taken")
	mustStatus(t, s.revokeKey(s.owner, id), nil, 200)
	mustStatus(t, s.createKey(s.owner, "phone", 0), nil, 201)

	wantCode(t, s.createKey(s.owner, "Phone 2", 0), 400, "invalid_request")
	wantCode(t, s.createKey(s.owner, "short", 3599), 400, "invalid_request")
	wantCode(t, s.createKey(s.owner, "long", 365*24*3600+1), 400, "invalid_request")
	r := s.createKey(s.owner, "year", 365*24*3600)
	mustStatus(t, r, nil, 201)
	s.clock.Advance(365*24*time.Hour - time.Second)
	s.works(r.JSON201.Token, true)
	s.clock.Advance(time.Second)
	s.works(r.JSON201.Token, false)
}

// Keys are managed only with a person's own key: an agent's token and a browser can't
// list, create or revoke one, the browser not even for its own person.
func TestOnlyAPersonsOwnKeyManagesKeys(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	_, agent := s.joinAs(s.owner, s.newBoard(), "writer", "")
	browser := s.browserToken(s.owner)
	laptop := keyNamed(t, s.keys(s.owner, ""), "laptop").Id
	for name, token := range map[string]string{"agent": agent, "browser": browser} {
		if code := errorCode(t, s.keys(token, ""), nil, 403); code != "human_token_required" {
			t.Fatalf("%s listing keys: %s", name, code)
		}
		if code := errorCode(t, s.createKey(token, "stolen", 0), nil, 403); code != "human_token_required" {
			t.Fatalf("%s creating a key: %s", name, code)
		}
		if code := errorCode(t, s.revokeKey(token, laptop), nil, 403); code != "human_token_required" {
			t.Fatalf("%s revoking a key: %s", name, code)
		}
	}
	s.works(s.owner, true)
	if n := len(s.keys(s.owner, "").JSON200.Keys); n != 1 {
		t.Fatalf("keys after the refusals: %d", n)
	}
	// A key's secret never stands in for its id.
	r, err := s.client(s.owner).RevokeKeyWithResponse(ctx, s.owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.StatusCode() != http.StatusBadRequest {
		t.Fatalf("revoking by secret: %d %s", r.StatusCode(), bodyOf(r))
	}
}

// An admin lists and revokes anyone's keys but creates keys only for themselves; a
// member sees and revokes only their own, and another person's key looks like no key.
func TestAdminRevokesAMembersKeyButCantCreateOne(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	sam := s.addHuman("sam")
	mayaKey := keyNamed(t, s.keys(maya, ""), "laptop")
	adminKey := keyNamed(t, s.keys(s.owner, ""), "laptop")

	if k := keyNamed(t, s.keys(s.owner, "maya"), "laptop"); k.Id != mayaKey.Id {
		t.Fatalf("the admin listing maya's keys: %+v", k)
	}
	wantCode(t, s.keys(s.owner, "nobody"), 404, "person_not_found")
	wantCode(t, s.keys(maya, "alex"), 403, "server_admin_required")
	wantCode(t, s.keys(maya, "sam"), 403, "server_admin_required")
	wantCode(t, s.keys(maya, "nobody"), 403, "server_admin_required")
	wantCode(t, s.revokeKey(maya, adminKey.Id), 404, "key_not_found")
	wantCode(t, s.revokeKey(maya, "key_01M3W33B0000000000000000ZZ"), 404, "key_not_found")
	s.works(s.owner, true)

	// There is no way to name another person when creating a key: a key made with the
	// admin's key is the admin's.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.url+"/v1/keys",
		strings.NewReader(`{"name":"for-maya","person":"maya"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.owner)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("creating a key for someone else: %d", resp.StatusCode)
	}
	_, made := s.newKey(s.owner, "for-maya")
	me, err := s.client(made).GetMeWithResponse(context.Background())
	mustStatus(t, me, err, 200)
	if me.JSON200.Name != "alex" {
		t.Fatalf("a key the admin made acts as %s", me.JSON200.Name)
	}

	mustStatus(t, s.revokeKey(s.owner, mayaKey.Id), nil, 200)
	s.works(maya, false)
	s.works(sam, true)
}

// A revoked key's browser logins and agent tokens stop at once: their open streams end
// and a waiting inbox read returns 401, while the person's other key and what it started
// carry on.
func TestRevokingAKeyEndsItsStreamsAndWaits(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	id, phone := s.newKey(s.owner, "phone")
	boardName := s.newBoard()
	_, agent := s.joinAs(phone, boardName, "writer", "")
	_, other := s.joinAs(s.owner, boardName, "reviewer", "")
	browser := s.browserToken(phone)
	if k := keyNamed(t, s.keys(s.owner, ""), "phone"); *k.BrowserSessions != 1 || *k.AgentSeats != 1 {
		t.Fatalf("what the key started: %+v", k)
	}

	byKey, byBrowser, byOwner := s.openStream(phone), s.openStream(browser), s.openStream(s.owner)
	for _, st := range []*eventStream{byKey, byBrowser, byOwner} {
		st.head()
	}
	wait := 600
	waited := make(chan *api.GetInboxResponse, 2)
	for _, token := range []string{agent, other} {
		go func() {
			r, err := s.client(token).GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait})
			if err != nil {
				t.Error(err)
			}
			waited <- r
		}()
	}

	mustStatus(t, s.revokeKey(s.owner, id), nil, 200)
	byKey.ends()
	byBrowser.ends()
	select {
	case r := <-waited:
		mustStatus(t, r, nil, 401)
	case <-time.After(streamWait):
		t.Fatal("the agent's inbox wait didn't end with its key")
	}
	for _, token := range []string{phone, browser, agent} {
		s.works(token, false)
	}
	r, err := s.streamRequest(ctx, browser)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reopening the browser's stream: %d", r.StatusCode)
	}

	// The owner's other key, its stream and its agent's wait are untouched.
	s.say(s.owner, boardName, "still here")
	if h := byOwner.head(); h.Board != boardName {
		t.Fatalf("the owner's stream after the revocation: %+v", h)
	}
	select {
	case r := <-waited:
		mustStatus(t, r, nil, 200)
	case <-time.After(streamWait):
		t.Fatal("the other agent's wait didn't return the message")
	}
}

// timersWaiting waits until at least n timers wait on the fake clock, so the test moves
// it only once every waiter it expects has registered, and returns how many there are.
func (s *testServer) timersWaiting(n int) int {
	s.t.Helper()
	deadline := time.Now().Add(streamWait)
	for s.clock.Waiters() < n {
		if time.Now().After(deadline) {
			s.t.Fatalf("%d timers wait on the clock, want %d", s.clock.Waiters(), n)
		}
		<-time.After(time.Millisecond) // a poll interval, not a wait for the server
	}
	return s.clock.Waiters()
}

// When a key expires, its stream ends at that moment and a read waiting with an agent
// token it started returns 401, while the person's other key keeps working.
func TestAnExpiredKeyEndsItsStreamsAndWaits(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	id, phone := s.newKey(s.owner, "phone")
	_, agent := s.joinAs(phone, s.newBoard(), "writer", "")
	// The key expires in five minutes, before the inbox wait's ten.
	db, err := sql.Open("sqlite", "file:"+s.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE access_keys SET expires_at = '2026-10-01T16:05:00.000Z' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	st := s.openStream(phone)
	st.head()
	// The stream's keepalive and expiry, counted before the wait starts, so the wait's own
	// two timers are always counted on top of them.
	base := s.timersWaiting(2)
	wait := 600
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		r, err := s.client(agent).GetInboxWithResponse(ctx, &api.GetInboxParams{Wait: &wait})
		if err != nil {
			t.Error(err)
			return
		}
		if r.StatusCode() != http.StatusUnauthorized {
			t.Errorf("the agent's inbox wait as its key expired: %d %s", r.StatusCode(), r.Body)
		}
	}()
	// Then the wait's timeout and expiry.
	s.timersWaiting(base + 2)
	s.clock.Advance(5 * time.Minute)
	select {
	case <-waited:
	case <-time.After(streamWait):
		t.Fatal("the inbox wait didn't end when its key expired")
	}
	st.ends()
	s.works(phone, false)
	s.works(agent, false)
	s.works(s.owner, true)
}

// A browser login's stream ends when the login expires, after 30 days, without waiting
// for its key.
func TestABrowsersStreamEndsWhenItsLoginExpires(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.newBoard()
	browser := s.browserToken(s.owner)
	st := s.openStream(browser)
	st.head()
	// The stream's keepalive and its login's expiry.
	s.timersWaiting(2)
	s.clock.Advance(30*24*time.Hour - time.Second)
	s.works(browser, true)
	s.clock.Advance(time.Second)
	st.ends()
	s.works(browser, false)
	s.works(s.owner, true)
}

// Logging the browsers out ends their open streams too.
func TestLoggingBrowsersOutEndsTheirStreams(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	s.newBoard()
	st := s.openStream(s.browserToken(s.owner))
	st.head()
	r, err := s.client(s.owner).EndBrowserTokensWithResponse(context.Background(), nil)
	mustStatus(t, r, err, 200)
	st.ends()
}

// "Last used" counts the key's own requests and those of the browser logins and agent
// tokens it started, recorded once a minute at most; the listing says when the key it
// was made with was used before.
func TestLastUsedCountsWhatTheKeyStarted(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	start := s.clock.Now()
	_, phone := s.newKey(s.owner, "phone")
	if k := keyNamed(t, s.keys(s.owner, ""), "phone"); k.LastUsedAt != nil {
		t.Fatalf("an unused key: %+v", k)
	}
	_, agent := s.joinAs(phone, s.newBoard(), "writer", "")
	browser := s.browserToken(phone)
	lastUsed := func() time.Time {
		t.Helper()
		k := keyNamed(t, s.keys(s.owner, ""), "phone")
		if k.LastUsedAt == nil {
			t.Fatalf("no last use: %+v", k)
		}
		return *k.LastUsedAt
	}
	if got := lastUsed(); !got.Equal(start) {
		t.Fatalf("last used after joining: %s", got)
	}
	s.clock.Advance(5 * time.Minute)
	s.works(agent, true)
	if got := lastUsed(); !got.Equal(start.Add(5 * time.Minute)) {
		t.Fatalf("last used after the agent's request: %s", got)
	}
	s.clock.Advance(5 * time.Minute)
	s.works(browser, true)
	if got := lastUsed(); !got.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("last used after the browser's request: %s", got)
	}
	// Within the minute a use isn't written again.
	s.clock.Advance(30 * time.Second)
	s.works(phone, true)
	if got := lastUsed(); !got.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("last used half a minute later: %s", got)
	}
	s.clock.Advance(time.Minute)
	l := s.keys(phone, "")
	mustStatus(t, l, nil, 200)
	if p := l.JSON200.CurrentKeyPreviousUse; p == nil || !p.Equal(start.Add(10*time.Minute)) {
		t.Fatalf("the previous use of the listing's key: %s", bodyOf(l))
	}
}

// A machine's key from an invite expires only after 90 days without use: each use
// moves its expiry on.
func TestAMachinesKeyExpiresOnlyOnceUnused(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	k := keyNamed(t, s.keys(maya, ""), "laptop")
	if k.IdleExpirySeconds == nil || *k.IdleExpirySeconds != 90*24*3600 || !k.ExpiresAt.Equal(s.clock.Now().Add(90*24*time.Hour)) {
		t.Fatalf("a machine's key: %+v", k)
	}
	s.clock.Advance(80 * 24 * time.Hour)
	s.works(maya, true)
	s.clock.Advance(80 * 24 * time.Hour)
	s.works(maya, true)
	s.clock.Advance(90 * 24 * time.Hour)
	s.works(maya, false)
}
