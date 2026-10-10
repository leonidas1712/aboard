package cli

import (
	"context"
	"strings"
)

func (a *app) peopleReadClient(ctx context.Context, server string) (serverRef, bool, *client, error) {
	if a.agentSelected("") {
		a.agentServerFlag = server
		a.boardServerFlag = server
		srv, _, c, err := a.admissionClient(ctx, server, "", "")
		if err == nil {
			if known, _, e := a.knownServers(); e == nil {
				for _, k := range known {
					if k.URL == srv.URL {
						srv.Name = k.Name
						c.server.Name = k.Name
						break
					}
				}
			}
		}
		return srv, false, c, err
	}
	return a.peopleClient(ctx, server, "Listing the server's people", "aboard people")
}

func (a *app) peopleReadError(srv serverRef, status int, body []byte) *Error {
	var e *Error
	if a.agentSelected("") {
		e = apiError(status, body)
	} else {
		e = keyRejected(srv, status, body)
	}
	if srv.Name != "" && srv.Name != srv.URL {
		e.Hint = strings.ReplaceAll(e.Hint, srv.URL, srv.Name)
		if e.Next != nil {
			e.Next.Command = strings.ReplaceAll(e.Next.Command, shellWord(srv.URL), commandWord(srv.Name))
			e.Next.Command = strings.ReplaceAll(e.Next.Command, commandWord(srv.URL), commandWord(srv.Name))
			e.Next.Resume = strings.ReplaceAll(e.Next.Resume, srv.URL, srv.Name)
		}
	}
	return e
}
