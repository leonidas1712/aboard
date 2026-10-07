package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		if r.URL.Path == "/v1/boards/payments-design/messages" {
			_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{map[string]any{"id": "msg_ask", "board": "payments-design", "ask": map[string]any{"options": []string{"Proceed", "Wait"}}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"members": []any{}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{})
	r := e.run("say", "--board", "payments-design", "--reply", "msg_ask", "--option", "2", "--to", "@claude", "--urgent", "--expect-reply", "--json")
	if r.code != 0 {
		t.Fatalf("answer: %s", r.stdout)
	}
	if posted.Body != "Wait" || posted.Answer == nil || posted.Answer.Option == nil || *posted.Answer.Option != 2 || posted.To == nil || (*posted.To)[0] != "@claude" || posted.Urgent == nil || !*posted.Urgent || posted.ExpectsReply == nil || !*posted.ExpectsReply {
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
	for _, args := range [][]string{{"ask", "Question", "--at", "20m"}, {"ask", "--open", "Question"}, {"ask", "Q", "1", "2", "3", "4", "5"}, {"say", "--option", "1"}, {"say", "text", "--reply", "msg_ask", "--option", "0"}} {
		r := e.run(append(args, "--as", "claude", "--json")...)
		if r.code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestWithdrawTargetsSelectedSelfWithoutHumanFallback(t *testing.T) {
	var posted api.PostMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"features": []string{"asks"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer aba_agent" {
			t.Error("withdrawal used human login")
		}
		if r.URL.Path == "/v1/me" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "mem_selected", "name": "claude", "kind": "agent"})
			return
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_withdraw", "board": "payments-design", "seq": 5, "reply_to_seq": 3, "body": posted.Body, "answer": map[string]any{"ask_seq": 3, "withdrawn": true}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"members": []any{}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", Token: "aba_agent"})
	r := e.run("ask", "--withdraw", "msg_ask", "No longer needed", "--as", "claude", "--json")
	if r.code != 0 {
		t.Fatalf("withdraw: %s", r.stdout)
	}
	if posted.To == nil || len(*posted.To) != 1 || (*posted.To)[0] != "@claude" {
		t.Fatalf("withdraw must address self: %#v", posted)
	}
}

func TestAgentOptionPreservesSayFlags(t *testing.T) {
	var posted api.PostMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(map[string]any{"features": []string{"asks"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer aba_agent" {
			t.Error("option used human login")
		}
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_answer", "board": "payments-design", "seq": 5, "body": posted.Body, "to": []string{"@person"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []any{}, "members": []any{}})
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{Server: srv.URL, Board: "payments-design", Name: "claude", Token: "aba_agent"})
	r := e.run("say", "My answer", "--reply", "msg_ask", "--option", "1", "--to", "@person", "--urgent", "--expect-reply", "--as", "claude", "--json")
	if r.code != 0 {
		t.Fatalf("option answer: %s", r.stdout)
	}
	if posted.Answer == nil || posted.Answer.Option == nil || *posted.Answer.Option != 1 || posted.To == nil || (*posted.To)[0] != "@person" || posted.Urgent == nil || !*posted.Urgent || posted.ExpectsReply == nil || !*posted.ExpectsReply {
		t.Fatalf("say flags lost: %#v", posted)
	}
}

func TestPersonOptionWaitRefusesBeforeAuthenticatedRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("person wait reached server")
		w.WriteHeader(500)
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{})
	r := e.run("say", "--board", "payments-design", "--reply", "msg_ask", "--option", "1", "--wait-reply", "1", "--json")
	if r.code == 0 || !strings.Contains(r.stdout, "agent_not_selected") {
		t.Fatalf("person wait: %s", r.stdout)
	}
}
