package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/board"
)

// startMachine asks for a key for handle as a new machine called label, and returns its
// short code and collection secret. The answer, which holds secrets, is never cached.
func (s *testServer) startMachine(handle, label string) (code, secret string) {
	s.t.Helper()
	r, err := s.client("").StartMachineRequestWithResponse(context.Background(), nil, api.StartMachineRequest{Handle: handle, Label: label})
	mustStatus(s.t, r, err, 201)
	if r.JSON201.PollIntervalSeconds < 1 || !strings.HasPrefix(r.JSON201.Secret, "abc_") || r.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		s.t.Fatalf("started: %v %s", r.HTTPResponse.Header, bodyOf(r))
	}
	return r.JSON201.Code, r.JSON201.Secret
}

func (s *testServer) collect(secret string) *api.CollectMachineRequestResponse {
	s.t.Helper()
	r, err := s.client("").CollectMachineRequestWithResponse(context.Background(), nil, api.CollectMachineRequest{Secret: secret})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) lookupMachine(token, code string) *api.LookupMachineRequestResponse {
	s.t.Helper()
	r, err := s.client(token).LookupMachineRequestWithResponse(context.Background(), nil, api.MachineRequestCode{Code: code})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) approveMachine(token, code string) *api.ApproveMachineRequestResponse {
	s.t.Helper()
	r, err := s.client(token).ApproveMachineRequestWithResponse(context.Background(), nil, api.MachineRequestCode{Code: code})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s *testServer) refuseMachine(token, code string) *api.RefuseMachineRequestResponse {
	s.t.Helper()
	r, err := s.client(token).RefuseMachineRequestWithResponse(context.Background(), nil, api.MachineRequestCode{Code: code})
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// The whole flow: a new machine asks, its person sees and approves the code on a machine
// where she is signed in, and the new machine collects a key of its own, once. The key
// is hers, independent of the key that approved it: revoking that one leaves it working.
func TestApprovingAMachineGivesItAnIndependentKey(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	code, secret := s.startMachine("maya", "maya-desktop")

	waiting := s.collect(secret)
	mustStatus(t, waiting, nil, 202)
	if waiting.JSON202.State != "pending" {
		t.Fatalf("waiting: %s", bodyOf(waiting))
	}
	seen := s.lookupMachine(maya, strings.ToLower(strings.ReplaceAll(code, "-", "")))
	mustStatus(t, seen, nil, 200)
	if seen.JSON200.Label != "maya-desktop" || seen.JSON200.Person.Handle != "maya" || seen.JSON200.State != "pending" || seen.JSON200.RequestedFrom != "127.0.0.1" {
		t.Fatalf("the request maya sees: %s", bodyOf(seen))
	}
	approved := s.approveMachine(maya, code)
	mustStatus(t, approved, nil, 200)
	if approved.JSON200.State != "approved" {
		t.Fatalf("approved: %s", bodyOf(approved))
	}

	got := s.collect(secret)
	mustStatus(t, got, nil, 201)
	if got.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the collected key's answer may be cached: %v", got.HTTPResponse.Header)
	}
	key := got.JSON201.Key
	if got.JSON201.Person.Handle != "maya" || key.Name != "maya-desktop" || key.IdleExpirySeconds == nil ||
		*key.IdleExpirySeconds != int(board.MachineKeyIdle.Seconds()) || !strings.HasPrefix(key.Token, "abh_") || key.Token == maya {
		t.Fatalf("collected: %s", bodyOf(got))
	}
	wantCode(t, s.collect(secret), 404, "machine_request_invalid")
	wantCode(t, s.approveMachine(maya, code), 404, "machine_request_invalid")

	mustStatus(t, s.revokeKey(key.Token, keyNamed(t, s.keys(key.Token, ""), "laptop").Id), nil, 200)
	s.works(maya, false)
	s.works(key.Token, true)
}

// Someone who saw only the short code can't collect the key: neither the code nor a
// guessed secret is taken for the collection secret, and the code itself isn't secret
// enough to look like one.
func TestTheShortCodeAloneCantCollect(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	code, secret := s.startMachine("alex", "alex-desktop")
	mustStatus(t, s.approveMachine(s.owner, code), nil, 200)
	for _, guess := range []string{code, "abc_" + code, "abc_" + strings.Repeat("A", 43), s.owner} {
		wantCode(t, s.collect(guess), 404, "machine_request_invalid")
	}
	mustStatus(t, s.collect(secret), nil, 201)
}

// A request names its person, and only they decide it: a stranger with the right code
// gets exactly what a wrong code gets, and the request stays pending for its person. A
// request for a handle nobody has starts like any other and can never be approved, so
// starting one doesn't reveal which handles exist.
func TestOnlyTheNamedPersonApprovesAMachine(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya, sam := s.addHuman("maya"), s.addHuman("sam")
	code, secret := s.startMachine("maya", "maya-desktop")
	wrong := s.approveMachine(sam, "AAA-AAA")
	wantCode(t, wrong, 404, "machine_request_invalid")
	for _, r := range []response{s.lookupMachine(sam, code), s.approveMachine(sam, code), s.refuseMachine(sam, code)} {
		wantCode(t, r, 404, "machine_request_invalid")
		if bodyOf(r) != bodyOf(wrong) {
			t.Fatalf("a stranger's attempt differs from a wrong code's: %s, not %s", bodyOf(r), bodyOf(wrong))
		}
	}
	mustStatus(t, s.collect(secret), nil, 202)
	mustStatus(t, s.approveMachine(maya, code), nil, 200)
	got := s.collect(secret)
	mustStatus(t, got, nil, 201)
	if got.JSON201.Person.Handle != "maya" {
		t.Fatalf("the machine was signed in as %s", got.JSON201.Person.Handle)
	}

	nobodyCode, nobodySecret := s.startMachine("nobody", "desktop")
	for _, token := range []string{s.owner, maya, sam} {
		wantCode(t, s.approveMachine(token, nobodyCode), 404, "machine_request_invalid")
	}
	mustStatus(t, s.collect(nobodySecret), nil, 202)
	r, err := s.client("").StartMachineRequestWithResponse(context.Background(), nil, api.StartMachineRequest{Handle: "Maya Chen", Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, r, 422, "handle_invalid")
}

// Only a person's own key looks up, approves or refuses a machine: never an agent's
// token, a browser, or no token at all. A refused attempt changes nothing.
func TestOnlyAPersonsOwnKeyDecidesAMachine(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	_, agentToken, _ := s.pair("starter")
	browser := s.browserToken(s.owner)
	code, secret := s.startMachine("alex", "alex-desktop")
	for _, token := range []string{agentToken, browser} {
		wantCode(t, s.lookupMachine(token, code), 403, "human_token_required")
		wantCode(t, s.approveMachine(token, code), 403, "human_token_required")
		wantCode(t, s.refuseMachine(token, code), 403, "human_token_required")
		mustStatus(t, s.collect(secret), nil, 202)
	}
	wantCode(t, s.approveMachine("", code), 401, "unauthorized")
	mustStatus(t, s.collect(secret), nil, 202)
}

// A refused request gives no key, and its code stops working.
func TestARefusedMachineGetsNoKey(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	code, secret := s.startMachine("alex", "alex-desktop")
	r := s.refuseMachine(s.owner, code)
	mustStatus(t, r, nil, 200)
	if r.JSON200.State != "refused" {
		t.Fatalf("refused: %s", bodyOf(r))
	}
	wantCode(t, s.approveMachine(s.owner, code), 404, "machine_request_invalid")
	wantCode(t, s.collect(secret), 403, "machine_request_refused")
}

// A request ends after five minutes, approved or not: its code stops working and its
// key can't be collected.
func TestAMachineRequestExpires(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	pending, pendingSecret := s.startMachine("alex", "one")
	approved, approvedSecret := s.startMachine("alex", "two")
	mustStatus(t, s.approveMachine(s.owner, approved), nil, 200)
	s.clock.Advance(board.MachineRequestTTL)
	wantCode(t, s.lookupMachine(s.owner, pending), 404, "machine_request_invalid")
	wantCode(t, s.approveMachine(s.owner, pending), 404, "machine_request_invalid")
	wantCode(t, s.collect(pendingSecret), 404, "machine_request_invalid")
	wantCode(t, s.collect(approvedSecret), 404, "machine_request_invalid")
}

// An approval is only as good as the key that made it: if that key is revoked before
// the machine collects, the machine gets nothing.
func TestAnApprovalEndsWithTheKeyThatMadeIt(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	_, spare := s.newKey(maya, "spare")
	code, secret := s.startMachine("maya", "maya-desktop")
	mustStatus(t, s.approveMachine(maya, code), nil, 200)
	mustStatus(t, s.revokeKey(spare, keyNamed(t, s.keys(spare, ""), "laptop").Id), nil, 200)
	wantCode(t, s.collect(secret), 404, "machine_request_invalid")
}

// A machine may ask for its key only so many times before its request ends, and an
// ended request can't be approved.
func TestCollectingIsBounded(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	code, secret := s.startMachine("alex", "alex-desktop")
	for range board.MachineRequestMaxPolls {
		mustStatus(t, s.collect(secret), nil, 202)
	}
	wantCode(t, s.collect(secret), 404, "machine_request_invalid")
	wantCode(t, s.approveMachine(s.owner, code), 404, "machine_request_invalid")
}

// Attempts with a short code are limited per client address, per person and across the
// server; starting requests and collecting keys are limited per client address and
// across the server. Each limit refuses with 429 and Retry-After, and lifts after a
// minute.
func TestMachineAttemptsAreLimited(t *testing.T) {
	t.Parallel()
	type attempt func(s *testServer, maya, sam string) response
	guess := func(who func(maya, sam string) string) attempt {
		return func(s *testServer, maya, sam string) response { return s.lookupMachine(who(maya, sam), "AAA-AAA") }
	}
	asMaya := func(maya, _ string) string { return maya }
	asSam := func(_, sam string) string { return sam }
	for _, tt := range []struct {
		name   string
		limits func(o *api.Options)
		try    attempt
		ok     int
		// other is an attempt the limit doesn't count: it still gets through.
		other attempt
	}{
		{
			"codes per address", func(o *api.Options) { o.MachineCodes = api.Limits{PerAddr: 2, PerPerson: 100, Server: 100} },
			guess(asMaya), 404, nil,
		},
		{
			"codes per person", func(o *api.Options) { o.MachineCodes = api.Limits{PerAddr: 100, PerPerson: 2, Server: 100} },
			guess(asMaya), 404, guess(asSam),
		},
		{
			"codes across the server", func(o *api.Options) { o.MachineCodes = api.Limits{PerAddr: 100, PerPerson: 100, Server: 2} },
			guess(asMaya), 404, nil,
		},
		{
			"starting", func(o *api.Options) { o.MachineRequests = api.Limits{PerAddr: 2, Server: 100} },
			func(s *testServer, _, _ string) response {
				r, err := s.client("").StartMachineRequestWithResponse(context.Background(), nil, api.StartMachineRequest{Handle: "alex", Label: "x"})
				if err != nil {
					t.Fatal(err)
				}
				return r
			}, 201, nil,
		},
		{
			"collecting", func(o *api.Options) { o.MachineCollects = api.Limits{PerAddr: 100, Server: 2} },
			func(s *testServer, _, _ string) response { return s.collect("abc_guess") }, 404, nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, tt.limits)
			maya, sam := s.addHuman("maya"), s.addHuman("sam")
			for range 2 {
				if r := tt.try(s, maya, sam); r.StatusCode() != tt.ok {
					t.Fatalf("an attempt under the limit: %d %s", r.StatusCode(), bodyOf(r))
				}
			}
			r := tt.try(s, maya, sam)
			wantCode(t, r, 429, "rate_limited")
			if tt.other != nil {
				if r := tt.other(s, maya, sam); r.StatusCode() != tt.ok {
					t.Fatalf("another person's attempt: %d %s", r.StatusCode(), bodyOf(r))
				}
			}
			s.clock.Advance(time.Minute)
			if r := tt.try(s, maya, sam); r.StatusCode() != tt.ok {
				t.Fatalf("an attempt a minute later: %d %s", r.StatusCode(), bodyOf(r))
			}
		})
	}
}
