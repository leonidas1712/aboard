package cli

import "context"

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
