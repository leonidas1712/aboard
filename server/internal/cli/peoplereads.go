package cli

import (
	"context"
	"strings"
)

func (a *app) peopleReadClient(ctx context.Context, server, board, as string) (srv serverRef, started bool, selectedBoard string, c *client, err error) {
	if a.agentSelected(as) {
		a.agentServerFlag = server
		a.boardServerFlag = server
		srv, selectedBoard, c, err := a.admissionClient(ctx, server, board, as)
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
		return srv, false, selectedBoard, c, err
	}
	srv, started, c, err = a.peopleClient(ctx, server, "Listing the server's people", "aboard people")
	return srv, started, "", c, err
}

func (a *app) peopleReadError(srv serverRef, status int, body []byte, asFlag ...string) *Error {
	var e *Error
	as := ""
	if len(asFlag) > 0 {
		as = asFlag[0]
	}
	if a.agentSelected(as) {
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
