package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// peopleUsage is the usage of "aboard people".
var peopleUsage = usageOf("people")

// runPeople runs "aboard people", which lists the people on a server with their server
// roles, and "aboard people role|remove", which an admin uses to change a person's role
// or remove them from the server. Every form is a person's, with their own key, so it
// refuses inside a harness session or with ABOARD_AGENT set, before it reads the key.
func runPeople(ctx context.Context, a *app, args []string) error {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "":
		return runPeopleList(ctx, a, args)
	case "rename":
		return runPeopleRename(ctx, a, args)
	case "role":
		return runPeopleRole(ctx, a, args)
	case "remove":
		return runPeopleRemove(ctx, a, args)
	}
	return usageError("aboard people has no command "+strconv.Quote(sub)+".", peopleUsage)
}

// peopleClient refuses where an agent runs the command, then returns the server people
// commands act on (as personServer picks it) and a client with this machine's key for it.
func (a *app) peopleClient(ctx context.Context, serverFlag, what, command string) (serverRef, bool, *client, error) {
	if err := a.refuseInSession(what, command); err != nil {
		return serverRef{}, false, nil, err
	}
	srv, started, err := a.personServer(ctx, serverFlag)
	if err != nil {
		return serverRef{}, false, nil, err
	}
	c, err := a.keysClient(ctx, srv)
	return srv, started, c, err
}

func runPeopleList(ctx context.Context, a *app, args []string) error {
	fs := a.flags("people")
	serverFlag := fs.String("server", "", "the server, when it isn't the one this machine would pick")
	if _, err := a.parse(fs, args, peopleUsage, 0, 0); err != nil {
		return err
	}
	srv, started, c, err := a.peopleReadClient(ctx, *serverFlag)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListServerPeopleWithResponse(ctx, nil)
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	people, err := r.JSON200.AsServerPeople()
	if err != nil {
		return err
	}
	var text string
	if started {
		text = "Started local Aboard at " + srv.URL + "\n"
	}
	st := a.out()
	text += fmt.Sprintf("People on %s:\n", srv.URL)
	rows := make([][]string, 0, len(people.People))
	for _, p := range people.People {
		name := ""
		if p.DisplayName != nil {
			name = *p.DisplayName
		}
		rows = append(rows, []string{"@" + p.Handle, string(p.ServerRole), name})
	}
	text += st.table([]string{"HANDLE", "SERVER ROLE", "NAME"}, rows, func(col int, c string, _ []string) string {
		switch col {
		case 0:
			return st.name(c)
		case 1:
			if c == "admin" {
				return st.ok(c)
			}
			return c
		}
		return st.dim(c)
	})
	a.emit(map[string]any{"server": srv, "people": people.People}, text)
	return nil
}

func runPeopleRole(ctx context.Context, a *app, args []string) error {
	fs := a.flags("people")
	serverFlag := fs.String("server", "", "the server, when it isn't the one this machine would pick")
	pos, err := a.parse(fs, args, peopleUsage, 2, 2)
	if err != nil {
		return err
	}
	handle, role := handleArg(pos[0]), pos[1]
	if role != string(api.ServerRoleChangeServerRoleAdmin) && role != string(api.ServerRoleChangeServerRoleMember) {
		return usageError(fmt.Sprintf("%q is not a role a person can be given; use admin or member.", role), peopleUsage)
	}
	if a.agentSelected("") {
		a.boardServerFlag = *serverFlag
		a.agentServerFlag = *serverFlag
		return a.requestServerRole(ctx, *serverFlag, handle, role)
	}
	srv, _, c, err := a.peopleClient(ctx, *serverFlag, "Changing a person's role on the server", "aboard people role @"+handle+" "+role)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.SetServerRoleWithResponse(ctx, handle, nil, api.ServerRoleChange{ServerRole: api.ServerRoleChangeServerRole(role)})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	noun := map[string]string{"admin": "an admin", "member": "a member"}[role]
	st := a.out()
	text := fmt.Sprintf("@%s is now %s of %s.\n", st.name(handle), st.ok(noun), srv.URL)
	if !r.JSON200.Changed {
		text = fmt.Sprintf("@%s is already %s of %s.\n", st.name(handle), noun, srv.URL)
	}
	a.emit(map[string]any{"server": srv, "person": r.JSON200.Person, "changed": r.JSON200.Changed}, text)
	return nil
}

func runPeopleRemove(ctx context.Context, a *app, args []string) error {
	fs := a.flags("people")
	serverFlag := fs.String("server", "", "the server, when it isn't the one this machine would pick")
	yes := fs.Bool("yes", false, "remove the person without asking")
	pos, err := a.parse(fs, args, peopleUsage, 1, 1)
	if err != nil {
		return err
	}
	handle := handleArg(pos[0])
	command := "aboard people remove @" + handle
	srv, _, c, err := a.peopleClient(ctx, *serverFlag, "Removing a person from the server", command)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	remove := func(dry bool) (*api.PersonRemoval, error) {
		r, err := c.api.RemoveFromServerWithResponse(ctx, handle, &api.RemoveFromServerParams{DryRun: &dry})
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, keyRejected(srv, r.StatusCode(), r.Body)
		}
		return r.JSON200, nil
	}
	preview, err := remove(true)
	if err != nil {
		return err
	}
	if !*yes {
		what := strings.Join(append([]string{fmt.Sprintf("Removing %s from %s ends %s.", handle, srv.URL, removalCounts(preview))},
			removalNotes(preview)...), " ")
		if !a.interactive() {
			return newError("confirmation_required", what+" It needs a yes first.",
				"Run "+command+" --yes to remove them.")
		}
		_, _ = io.WriteString(a.env.Stdout, what+"\n")
		ok, err := a.asker().confirm(fmt.Sprintf("Remove %s from %s?", handle, srv.URL), "", false)
		if errors.Is(err, errAborted) || (err == nil && !ok) {
			_, _ = io.WriteString(a.env.Stdout, "Nothing changed.\n")
			return nil
		}
		if err != nil {
			return err
		}
	}
	done, err := remove(false)
	if err != nil {
		return err
	}
	st := a.out()
	text := fmt.Sprintf("%s %s from %s: %s stopped, and %s left %s.\n", st.bad("Removed"), st.name("@"+handle), srv.URL,
		removalCounts(done), "@"+handle, counted(done.BoardsLeft, "board"))
	for _, note := range removalNotes(done) {
		text += note + "\n"
	}
	a.emit(map[string]any{"server": srv, "removal": done}, text)
	return nil
}

// removalCounts says what stops with a removed person: "2 keys, 1 browser session and 3
// agents".
func removalCounts(r *api.PersonRemoval) string {
	return fmt.Sprintf("%s, %s and %s", counted(r.KeysRevoked, "key"), counted(r.BrowserSessionsEnded, "browser session"),
		counted(r.AgentsRemoved, "agent"))
}

// removalNotes says, a sentence each, where ownership passes and which private boards no
// one can read again.
func removalNotes(r *api.PersonRemoval) []string {
	var notes []string
	if r.OwnersPassed > 0 {
		notes = append(notes, fmt.Sprintf("Ownership of %s passes to the person on it longest.", counted(r.OwnersPassed, "board")))
	}
	if n := len(r.UnreachableBoards); n > 0 {
		notes = append(notes, fmt.Sprintf("%s %s no one left on it, so no one can read %s again (%s).",
			counted(n, "private board"), plural(n, "has", "have"), plural(n, "it", "them"), strings.Join(r.UnreachableBoards, ", ")))
	}
	return notes
}

func runPeopleRename(ctx context.Context, a *app, args []string) error {
	fs := a.flags("people")
	server := fs.String("server", "", "the server to act on")
	pos, err := a.parse(fs, args, usageOf("people"), 2, 2)
	if err != nil {
		return err
	}
	old, name := handleArg(pos[0]), handleArg(pos[1])
	srv, _, c, err := a.peopleClient(ctx, *server, "Renaming a person", "aboard people rename @"+old+" "+name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.RenamePersonWithResponse(ctx, old, nil, api.PersonRename{Handle: name})
	if err != nil {
		return c.unreachable(err)
	}
	if r.JSON200 == nil {
		return keyRejected(srv, r.StatusCode(), r.Body)
	}
	a.emit(map[string]any{"server": srv, "person": r.JSON200.Person, "changed": r.JSON200.Changed}, fmt.Sprintf("@%s is now @%s on %s. Their identity, boards and agents stay.\n", old, r.JSON200.Person.Handle, srv.URL))
	return nil
}
