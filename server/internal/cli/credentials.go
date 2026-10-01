package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// agentCredential is one agent's token on one board of one server.
type agentCredential struct {
	Server string `json:"server"`
	Board  string `json:"board"`
	Name   string `json:"name"`
	Token  string `json:"token"`
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

// put adds or replaces an agent's credential.
func (c *credentials) put(cred agentCredential) {
	for i, a := range c.Agents {
		if a.Server == cred.Server && a.Board == cred.Board && a.Name == cred.Name {
			c.Agents[i] = cred
			return
		}
	}
	c.Agents = append(c.Agents, cred)
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
	return updateJSONFile(p.credentials(), &c, 0o600, func() error {
		c.put(cred)
		return nil
	})
}

// readOwnerToken returns the local owner's token for srv, which the local server wrote
// the first time it started. The token is only for the local server: for any other
// server it returns login_required, so a project file naming another server can never
// make a command send the owner's login there.
func (a *app) readOwnerToken(srv serverRef) (string, error) {
	if local := a.localServer(); srv.URL != local.URL {
		return "", newError("login_required",
			"Your login on this machine is for the local server at "+local.URL+", not "+srv.URL+".",
			"Run the command in a project whose .aboard file names the local server, or delete this project's .aboard file.")
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
			"This command acts as an agent, and no agent on board "+t.board+" was selected.",
			"Pass --as <agent> or set ABOARD_AGENT=<agent>.")
		e.Details = map[string]any{"choices": choices}
		return agentCredential{}, e
	}
	cred, ok := creds.find(t.server.URL, t.board, name)
	if !ok {
		hint := "Pass --as with one of this machine's agents on the board, or join it with aboard join."
		if len(choices) == 0 {
			hint = "This machine has no agents on board " + t.board + ". Join it with aboard join and a join line."
		}
		e := newError("agent_not_selected",
			"This machine has no agent named "+name+" on board "+t.board+".", hint)
		e.Details = map[string]any{"choices": choices}
		return agentCredential{}, e
	}
	return cred, nil
}

// agentFor resolves the agent for a command on board t.
func (a *app) agentFor(asFlag string, t target) (agentCredential, error) {
	creds, err := a.readCredentials()
	if err != nil {
		return agentCredential{}, err
	}
	return resolveAgent(asFlag, a.env.Getenv("ABOARD_AGENT"), creds, t)
}
