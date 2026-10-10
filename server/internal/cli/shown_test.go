package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

type failedReadOutput struct{}

func (failedReadOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestReadReportsOnlySuccessfullyEmittedOwnSessionMessages(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		json, markdown, fail, inbox, multi bool
	}{{name: "text"}, {name: "json", json: true}, {name: "markdown", markdown: true}, {name: "failed-output", fail: true}, {name: "inbox-text", inbox: true}, {name: "inbox-json", inbox: true, json: true}, {name: "inbox-failed-output", inbox: true, fail: true}, {name: "multi-inbox-text", inbox: true, multi: true}, {name: "multi-inbox-json", inbox: true, multi: true, json: true}, {name: "multi-inbox-failed-output", inbox: true, multi: true, fail: true}} {
		t.Run(tc.name, func(t *testing.T) {
			issuer, owner := testServer(t)
			a := inboxApp(t)
			getenv := a.env.Getenv
			a.env.Getenv = func(key string) string {
				switch key {
				case "ABOARD_SESSION":
					return "codex:shown-cli"
				case "ABOARD_BOOT":
					return "b1"
				default:
					return getenv(key)
				}
			}
			var stdout bytes.Buffer
			a.env.Stdout, a.env.Stderr, a.env.Executable = &stdout, io.Discard, os.Executable
			if tc.fail {
				a.env.Stdout = failedReadOutput{}
			}
			a.json = tc.json
			cred := seatOn(t, issuer, owner)
			if err := a.saveCredential(cred); err != nil {
				t.Fatal(err)
			}
			if _, err := a.saveLogin(serverRef{Name: "scratch", URL: issuer}, false, serverLogin{URL: issuer, Key: owner}); err != nil {
				t.Fatal(err)
			}
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(p.state, 0o700); err != nil {
				t.Fatal(err)
			}
			j, err := sqlitejournal.Open(t.Context(), p.deliveryDB())
			if err != nil {
				t.Fatal(err)
			}
			ctl, err := control.Listen(p.socket())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() {
				done <- delivery.Run(ctx, delivery.Config{Journal: j, Control: ctl, Clock: clock.Real{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Build: currentBuild(), Adapters: []delivery.Adapter{deliverytest.NewFakeAdapter("codex", false)}, ResolveAgent: (daemonTokens{a: a}).ResolveAgent, Connect: func(url string) delivery.Server { return apiserver.New(url, daemonTokens{a: a}, a.env.Rand) }})
			}()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
				if err := j.Close(); err != nil {
					t.Error(err)
				}
			})
			ref := delivery.AgentRef{Server: issuer, Board: cred.Board, Name: cred.Name, MemberID: cred.MemberID}
			call := func(op string) delivery.Response {
				t.Helper()
				r, e := a.callDaemon(t.Context(), delivery.Request{Op: op, Harness: "codex", Session: "shown-cli", Boot: "b1", Agent: &ref})
				if e != nil {
					t.Fatal(e)
				}
				return r
			}
			call(delivery.OpRegister)
			call(delivery.OpBind)
			call(delivery.OpPrompt)
			probe := call(delivery.OpAgents)
			if probe.Boot != "b1" || len(probe.Agents) != 1 {
				t.Fatalf("probe: %+v", probe)
			}
			hold := a.startInboxRead(t.Context(), ref, false)
			if hold.conn == nil || hold.boot != "b1" || hold.generation == 0 {
				t.Fatalf("hold: %+v", hold)
			}
			hold.done()
			_, before := do(t, "GET", issuer+"/v1/me/inbox", cred.Token, nil)
			status, msg := do(t, "POST", issuer+"/v1/boards/"+cred.Board+"/messages", owner, map[string]any{"to": []string{"@" + cred.Name}, "body": "successfully emitted exact record"})
			if status != 201 {
				t.Fatal(status, msg)
			}
			firstID, ok := msg["id"].(string)
			if !ok {
				t.Fatal(msg)
			}
			expected := map[string]string{firstID: issuer}
			if tc.multi {
				secondIssuer, secondOwner := testServer(t)
				second := seatOn(t, secondIssuer, secondOwner)
				if err := a.saveCredential(second); err != nil {
					t.Fatal(err)
				}
				original := ref
				ref = delivery.AgentRef{Server: secondIssuer, Board: second.Board, Name: second.Name, MemberID: second.MemberID}
				call(delivery.OpBind)
				ref = original
				status, message := do(t, "POST", secondIssuer+"/v1/boards/"+second.Board+"/messages", secondOwner, map[string]any{"to": []string{"@" + second.Name}, "body": "second issuer exact record"})
				if status != 201 {
					t.Fatal(status, message)
				}
				secondID, ok := message["id"].(string)
				if !ok {
					t.Fatal(message)
				}
				expected[secondID] = secondIssuer
			}
			args := []string{"--board", cred.Board, "--as", cred.Name}
			if tc.multi {
				args = nil
			}
			if tc.markdown {
				args = append(args, "--markdown")
			}
			if tc.inbox {
				err = runInbox(t.Context(), a, args)
			} else {
				err = runRead(t.Context(), a, args)
			}
			if tc.fail && err == nil {
				t.Fatal("failed stdout was ignored")
			}
			if !tc.fail && err != nil {
				t.Fatal(err)
			}
			observed, err := j.ShownMessages(t.Context(), delivery.SessionKey{Harness: "codex", ID: "shown-cli"}, "b1")
			if err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				if len(observed) != 0 {
					t.Fatal("failed output recorded shown messages")
				}
			} else {
				if len(observed) != len(expected) {
					t.Fatalf("shown observations:%+v", observed)
				}
				for _, row := range observed {
					if expected[row.Message.MessageID] != row.Agent.Server {
						t.Fatalf("wrong issuer observation:%+v", row)
					}
				}
			}
			_, in := do(t, "GET", issuer+"/v1/me/inbox", cred.Token, nil)
			if !tc.inbox && in["cursor"] != before["cursor"] {
				t.Fatalf("read moved cursor: %v", in)
			}
		})
	}
}
