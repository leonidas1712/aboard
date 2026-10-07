package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// agentCredential is one agent's token on one board of one server.
type agentCredential struct {
	Server   string                    `json:"server"`
	MemberID string                    `json:"member_id,omitempty"`
	Board    string                    `json:"board"`
	Name     string                    `json:"name"`
	Token    string                    `json:"token"`
	Legacy   *legacyCredentialIdentity `json:"legacy_identity,omitempty"`
}

// legacyCredentialIdentity records which name-based entry this token resolved. It
// survives a restart between saving credentials and promoting the delivery journal.
type legacyCredentialIdentity struct {
	Board string `json:"board"`
	Name  string `json:"name"`
}

// credentials is the content of credentials.json.
type credentials struct {
	Agents []agentCredential `json:"agents"`
}

func (c credentials) find(server, board, name string) (agentCredential, bool) {
	for _, a := range c.Agents {
		if a.Server == server && a.Board == board && a.Name == name {
			return a, true
		}
	}
	return agentCredential{}, false
}

// names returns the sorted names of the agents saved for a board.
func (c credentials) names(server, board string) []string {
	names := []string{}
	for _, a := range c.Agents {
		if a.Server == server && a.Board == board {
			names = append(names, a.Name)
		}
	}
	slices.Sort(names)
	return names
}

// put replaces a known seat by its id. A legacy record is promoted only when its
// own token proved the id, so a reused name cannot inherit an older seat's state.
func (c *credentials) put(cred agentCredential) {
	for i, a := range c.Agents {
		if a.Server != cred.Server {
			continue
		}
		sameSeat := cred.MemberID != "" && a.MemberID == cred.MemberID
		sameLegacy := a.MemberID == "" && a.Board == cred.Board && a.Name == cred.Name &&
			a.Token == cred.Token
		if sameSeat || sameLegacy {
			if sameSeat && cred.Legacy == nil {
				cred.Legacy = a.Legacy
			}
			c.Agents[i] = cred
			return
		}
	}
	c.Agents = append(c.Agents, cred)
}

func (c credentials) forSeat(agent delivery.AgentRef) (agentCredential, bool) {
	var found agentCredential
	count := 0
	for _, cred := range c.Agents {
		if cred.Server != agent.Server {
			continue
		}
		if agent.MemberID != "" {
			if cred.MemberID == agent.MemberID {
				return cred, true
			}
		} else if (cred.MemberID == "" && cred.Board == agent.Board && cred.Name == agent.Name) ||
			(cred.Legacy != nil && cred.Legacy.Board == agent.Board && cred.Legacy.Name == agent.Name) {
			found = cred
			count++
		}
	}
	return found, count == 1
}

func (a *app) readCredentials() (credentials, error) {
	p, err := a.paths()
	if err != nil {
		return credentials{}, err
	}
	var c credentials
	_, err = readJSONFile(p.credentials(), &c)
	return c, err
}

// saveCredential stores an agent token in credentials.json, readable only by its owner.
func (a *app) saveCredential(cred agentCredential) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	var c credentials
	return updateJSONFile(p.credentials(), &c, func() error {
		c.put(cred)
		return nil
	})
}

// readOwnerToken returns this machine's person's access key for srv: for the local
// server, the key it wrote the first time it started; for another server, the key aboard
// connect saved for exactly that server's URL. Each key goes only to the server that
// issued it: for any other server it returns login_required, so a project file naming
// another server can never make a command send a key there.
func (a *app) readOwnerToken(srv serverRef) (string, error) {
	if local := a.localServer(); srv.URL != local.URL {
		logins, err := a.readServerLogins()
		if err != nil {
			return "", err
		}
		if l, ok := logins.find(srv.URL); ok && l.Key != "" {
			return l.Key, nil
		}
		return "", newError("login_required",
			"Your login on this machine is for the local server at "+local.URL+", not "+srv.URL+".",
			"Run the command in a project whose .aboard file names the local server, or delete this project's .aboard file; "+
				"to use "+srv.URL+", ask one of its admins for an invite link and run aboard connect <link>.")
	}
	p, err := a.paths()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Clean(p.ownerToken()))
	if err != nil {
		return "", &Error{
			Code:    "login_required",
			Message: "There is no local owner login at " + p.ownerToken() + ".",
			Hint:    "Run aboard up to start the local server; it creates the login the first time it starts.",
			Err:     err,
		}
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", newError("login_required", "The local owner login at "+p.ownerToken()+" is empty.",
			"Delete the file and the local server's data directory to start over, or restore the file from a backup.")
	}
	return token, nil
}

// resolveAgent picks the agent a command acts as: --as, then ABOARD_AGENT. It never
// falls back to the human login.
func resolveAgent(asFlag, envAgent string, creds credentials, t target) (agentCredential, error) {
	name := asFlag
	if name == "" {
		name = envAgent
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	choices := creds.names(t.server.URL, t.board)
	if name == "" {
		e := newError("agent_not_selected",
			"This command acts as an agent, and no agent on board "+t.board+" ("+sourceText(t.source)+") was selected.",
			"Pass --as <agent> or set ABOARD_AGENT=<agent>.")
		e.Details = map[string]any{"choices": choices, "board_source": t.source}
		return agentCredential{}, e
	}
	cred, ok := creds.find(t.server.URL, t.board, name)
	if !ok {
		hint := "Pass --as with one of this machine's agents on the board, or join it with aboard join."
		if len(choices) == 0 {
			hint = "This machine has no agents on board " + t.board + ". Join it with aboard join and a join line."
		}
		e := newError("agent_not_selected",
			"This machine has no agent named "+name+" on board "+t.board+" ("+sourceText(t.source)+").", hint)
		e.Details = map[string]any{"choices": choices, "board_source": t.source}
		return agentCredential{}, e
	}
	return cred, nil
}
