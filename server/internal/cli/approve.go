package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// approveUsage is the usage line of aboard approve.
const approveUsage = "aboard approve <code> [--refuse] [--yes] [--server URL] [--json]"

// runApprove approves, or with --refuse refuses, a new machine's request to connect, by
// the short code the machine shows. Approving gives that machine a new key of the
// person's, so the person sees the request and confirms first; it refuses inside a
// harness session, and the server refuses agent and browser tokens.
func runApprove(ctx context.Context, a *app, args []string) error {
	fs := a.flags("approve")
	refuse := fs.Bool("refuse", false, "refuse the request instead of approving it")
	yes := fs.Bool("yes", false, "approve without asking")
	serverFlag := fs.String("server", "", "the server, when it isn't the one this machine would pick")
	pos, err := a.parse(fs, args, approveUsage, 1, 1)
	if err != nil {
		return err
	}
	// again is the command to run again, naming the server when it was given or picked,
	// so a person on several servers can copy it as it is.
	again := "aboard approve " + shellWord(pos[0])
	if *serverFlag != "" {
		again += " --server " + shellWord(*serverFlag)
	}
	inTerminal := again
	if *refuse {
		inTerminal += " --refuse"
	}
	if err := a.refuseInSession("Approving a machine", inTerminal); err != nil {
		return err
	}
	srv, err := a.approveServer(ctx, *serverFlag)
	if err != nil {
		return err
	}
	if *serverFlag == "" && srv.URL != a.localServer().URL {
		again += " --server " + srv.URL
	}
	c, err := a.keysClient(ctx, srv)
	if err != nil {
		return err
	}
	code := api.MachineRequestCode{Code: pos[0]}
	if *refuse {
		r, err := c.api.RefuseMachineRequestWithResponse(ctx, nil, code)
		if err != nil {
			return c.unreachable(err)
		}
		if r.JSON200 == nil {
			return keyRejected(srv, r.StatusCode(), r.Body)
		}
		a.emit(map[string]any{"server": srv, "request": r.JSON200, "approved": false},
			fmt.Sprintf("Refused. %q is told so and gets no key.\n", r.JSON200.Label))
		return nil
	}
	look, err := c.api.LookupMachineRequestWithResponse(ctx, nil, code)
	if err != nil {
		return c.unreachable(err)
	}
	if look.JSON200 == nil {
		return keyRejected(srv, look.StatusCode(), look.Body)
	}
	req := look.JSON200
	question := fmt.Sprintf("Approve %q connecting to %s as %s?", req.Label, hostOf(srv), req.Person.Handle)
	about := fmt.Sprintf("A machine calling itself %q asked from %s %s. That name is only its own claim.\n",
		req.Label, req.RequestedFrom, agoText(req.CreatedAt, time.Now()))
	if !*yes {
		if !a.interactive() {
			return newError("confirmation_required",
				fmt.Sprintf("Approving gives %q a key that signs it in to %s as %s, so it needs your yes.", req.Label, srv.URL, req.Person.Handle),
				"Run "+again+" in a terminal to see the request and answer, or "+again+" --yes to approve it without asking.")
		}
		_, _ = fmt.Fprint(a.env.Stdout, about)
		ok, err := a.asker().confirm(question,
			"Approve only a request you started yourself, a moment ago: whoever runs that machine is signed in as you.", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			a.emit(nil, "Nothing changed. The request ends by itself in a few minutes, or turn it down now with: "+again+" --refuse\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	r, err := c.api.ApproveMachineRequestWithResponse(ctx, nil, code)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	a.emit(map[string]any{"server": srv, "request": r.JSON200, "approved": true},
		fmt.Sprintf("Approved. %q receives a key of its own, as %s, and aboard keys lists it once it has.\n", r.JSON200.Label, r.JSON200.Person.Handle))
	return nil
}

// approveServer is the server aboard approve acts on: --server, else the one this
// directory's .aboard names, else the one server this machine is connected to, else
// the local server. A machine connected to several servers names one with --server.
func (a *app) approveServer(ctx context.Context, flag string) (serverRef, error) {
	if flag == "" {
		p, ok, err := a.readProject()
		if err != nil {
			return serverRef{}, err
		}
		if !ok || p.Server.URL == "" {
			logins, err := a.readServerLogins()
			if err != nil {
				return serverRef{}, err
			}
			switch len(logins.Servers) {
			case 0:
			case 1:
				flag = logins.Servers[0].URL
			default:
				choices := make([]string, 0, len(logins.Servers))
				for _, s := range logins.Servers {
					choices = append(choices, s.URL)
				}
				e := newError("server_not_selected", "This machine is connected to several servers, and the code names a request on one of them.",
					"Name the server the new machine connects to with --server, such as --server "+choices[0]+".")
				e.Details = map[string]any{"choices": choices}
				return serverRef{}, e
			}
		}
	}
	srv, _, err := a.keysServer(ctx, flag)
	return srv, err
}
