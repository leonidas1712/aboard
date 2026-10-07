package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestAskCommandsUseSelectedCredential(t *testing.T) {
	var posted api.PostMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"features": []string{"asks", "tasks"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer aba_agent" {
			t.Error("used another identity")
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_ask", "board": "payments-design", "seq": 3, "body": posted.Body, "to": []string{"@person"}, "ask": map[string]any{"to": map[string]any{"name": "person", "kind": "human"}, "options": []string{"Yes", "No"}, "blocking": true, "state": "open"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{}, "members": []any{}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", Token: "aba_agent"})
	r := e.run("ask", "Proceed?", "Yes", "No", "--as", "claude", "--json")
	if r.code != 0 {
		t.Fatalf("ask: %s", r.stdout)
	}
	if posted.Ask == nil || posted.Ask.Options == nil || len(*posted.Ask.Options) != 2 || posted.Body != "Proceed?" || posted.To != nil {
		t.Fatalf("wrong request: %#v", posted)
	}
}

func TestPersonAnswersOptionWithOwnCredentialAndOptionText(t *testing.T) {
	var posted api.PostMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"features": []string{"asks"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer abh_person" {
			t.Error("answer did not use own person credential")
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_answer", "board": "payments-design", "seq": 4, "body": posted.Body, "to": []string{"@claude"}})
			return
		}
		if r.URL.Path == "/v1/messages/msg_ask" {
			_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"id": "msg_ask", "board": "payments-design", "ask": map[string]any{"options": []string{"Proceed", "Wait"}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"members": []any{}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{})
	r := e.run("say", "--board", "payments-design", "--reply", "msg_ask", "--option", "2", "--json")
	if r.code != 0 {
		t.Fatalf("answer: %s", r.stdout)
	}
	if posted.Body != "Wait" || posted.Answer == nil || posted.Answer.Option == nil || *posted.Answer.Option != 2 {
		t.Fatalf("wrong answer %#v", posted)
	}
}

func TestAskInvalidModesRefuseBeforeNetwork(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("invalid mode reached server")
		w.WriteHeader(500)
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", Token: "aba_agent"})
	for _, args := range [][]string{{"ask", "Question", "--at", "20m"}, {"ask", "--open", "Question"}, {"ask", "Q", "1", "2", "3", "4", "5"}, {"say", "--option", "1"}} {
		r := e.run(append(args, "--as", "claude", "--json")...)
		if r.code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}
