package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/boardfile"
)

// commandHelp is everything aboard help says about one command. It is the one source of
// a command's usage line, which usage errors repeat, and of the CLI reference in the docs.
type commandHelp struct {
	Name        string        `json:"name"`
	Group       string        `json:"group"`
	Summary     string        `json:"summary"`
	Usage       []string      `json:"usage"`
	Description string        `json:"description"`
	Flags       []helpFlag    `json:"flags"`
	Examples    []helpExample `json:"examples"`
	SeeAlso     []string      `json:"see_also"`
}

// helpFlag is one flag of a command: its name, the value it takes (empty for a switch)
// and what it does.
type helpFlag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Text  string `json:"text"`
}

// helpExample is a command line to copy, with what it does.
type helpExample struct {
	Command string `json:"command"`
	Text    string `json:"text"`
}

// Command groups, in the order aboard help lists them. Commands in groupInternal are
// run by Aboard itself and left out of the list.
const (
	groupStart    = "Get started"
	groupTalk     = "Talk"
	groupBoard    = "Board"
	groupRun      = "Run"
	groupMaintain = "Maintain"
	groupInternal = "Run by Aboard itself"
)

var helpGroups = []string{groupStart, groupTalk, groupBoard, groupRun, groupMaintain}

// Flags several commands share.
var (
	flagJSON  = helpFlag{"--json", "", "Print one JSON object instead of text, errors included."}
	flagAs    = helpFlag{"--as", "AGENT", "Act as this agent. Without it: ABOARD_AGENT, then the agent bound to this harness session."}
	flagBoard = helpFlag{"--board", "NAME", "The board, when the agent's name is used on more than one."}
)

// helpTopics returns the help of every command, in the order aboard help lists them.
func helpTopics() []commandHelp {
	topics := helpText(strings.Join(boardfile.TemplateNames(), ", "))
	for i := range topics {
		// Lists are never null in --json output.
		h := &topics[i]
		h.Flags, h.Examples, h.SeeAlso = nonNil(h.Flags), nonNil(h.Examples), nonNil(h.SeeAlso)
	}
	return topics
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// helpText is the help of every command; templates names the board templates.
func helpText(templates string) []commandHelp {
	return []commandHelp{
		{
			Name: "setup", Group: groupStart, Summary: "Set up an invited account and report what still needs you",
			Usage:       []string{"aboard setup INVITE_LINK|PAIRING_ID [--handle HANDLE] [--name MACHINE] [--server SERVER] [--json]"},
			Description: "An invite is consent to create a new account, including inside an agent session. Setup saves its machine-held proof before redeeming, then confirms the original account with that same proof. A lost response remains uncertain until the original key authenticates; setup never creates a replacement account or selects an existing one. No key or invite secret is printed.\n\nThe report has six steps: installed, account, memberships, harness, pairing and delivery. Harness trust and restart need your confirmation. Pairing acceptance and delivery verification are pending in this build; file installation alone never proves delivery. Continue Aboard setup after the stated next step.",
			Flags:       []helpFlag{{"--handle", "HANDLE", "Your visible name on the inviting server."}, {"--name", "MACHINE", "This machine's key name."}, {"--server", "SERVER", "The issuer of the pairing request; never overrides an invite's issuer."}, flagJSON},
			Examples:    []helpExample{{"aboard setup https://team.example.com/join#abi_CODE --handle teammate", "Set up the new account named in the invitation."}}, SeeAlso: []string{"connect", "init", "invite"},
		},
		{
			Name: "pairing", Group: groupStart,
			Summary:     "Propose work and pair two exact agent sessions",
			Usage:       []string{pairingUsage},
			Description: "List requests addressed to you, propose work from your agent's board, or accept in the exact session that should take part. Ready means both real sessions have exchanged verified replies.",
			Flags: []helpFlag{
				{"--board", "BOARD", "The initiating agent's board"},
				{"--server", "SERVER", "The issuer server's name or URL"},
				{"--as", "AGENT", "The initiating seat for a request; never a replacement for --here"},
				{"--here", "", "Select this exact session for accept or select"},
				{"--replace", "", "Explicitly replace this side's endpoint and invalidate old proof"},
				flagJSON,
			},
		},
		{
			Name: "allowance", Group: groupMaintain,
			Summary:     "Read or change what your agents may do without asking",
			Usage:       []string{"aboard allowance [--server SERVER] [--json]", "aboard allowance on|off [--server SERVER] [--json]", "aboard allowance set invite-people|add-people on|off [--server SERVER] [--json]"},
			Description: "Allowances start off. Only your person can change them. on enables both categories; off disables both; set changes one category and keeps the other. Agents use their own seat credential and receive the command their person can run.",
			Flags:       []helpFlag{{"--server", "SERVER", "The server that issued the allowance."}, flagJSON},
			Examples:    []helpExample{{"aboard allowance set add-people on", "Let your agents add people without asking."}},
			SeeAlso:     []string{"approvals", "invite", "board"},
		},
		{
			Name: "approvals", Group: groupMaintain,
			Summary:     "See held actions, or allow or decline one",
			Usage:       []string{"aboard approvals [--server SERVER] [--board BOARD] [--as AGENT] [--json]", "aboard approvals allow ID [--always] [--server SERVER] [--json]", "aboard approvals decline ID [--server SERVER] [--json]"},
			Description: "Your person sees their agents' requests; an agent sees only its own. Pending requests come first. Only your person can allow or decline the recorded action. --always also enables its allowance category. A newly created invite is returned once; a replay never returns its token again.",
			Flags:       []helpFlag{{"--server", "SERVER", "The server that issued the approval id."}, flagBoard, flagAs, {"--always", "", "Also enable this action's allowance category."}, flagJSON},
			Examples:    []helpExample{{"aboard approvals", "List your agents' requests."}, {"aboard approvals allow apr_ID", "Allow the recorded action once."}},
			SeeAlso:     []string{"allowance", "invite", "board"},
		},
		{
			Name: "init", Group: groupStart,
			Summary: "Add the Aboard skill and delivery hooks to Claude Code and Codex",
			Usage:   []string{"aboard init [--yes] [--scope global|project] [--harness H[,H]] [--delivery focused|all|humans|off] [--allow-commands] [--json]"},
			Description: "Installs the Aboard skill, and the delivery hooks that bring messages into an open session, for each harness found on this machine. " +
				"Running it again changes only what differs from what this aboard installs, and keeps your own settings and hooks.\n\n" +
				"In a terminal, init first shows what is already set up, then asks which harnesses, where to install, the delivery mode and whether to allow aboard commands, " +
				"shows the changes and asks before making them. Each flag answers its question. " +
				"In a pipe, inside an agent's session or with --json it never asks: without --yes it lists the changes it would make and changes nothing.\n\n" +
				"Restart open sessions afterwards so they load the hooks. Claude Code and Codex ask you to trust new hooks once, in /hooks.",
			Flags: []helpFlag{
				{"--yes", "", "Make the changes without asking."},
				{"--scope", "global|project", "global (the default) installs in each harness's config folder, for every project; project installs only under this directory."},
				{"--harness", "H[,H]", "Set up only these harnesses: claude-code, codex. Default: every one found."},
				{"--delivery", "focused|all|humans|off", "Set, on this machine, the delivery mode of agents that have none of their own, for servers that don't hold delivery modes; a server that does decides each agent's mode (see aboard delivery). It is a person's choice, so it is refused inside an agent's session."},
				{"--allow-commands", "", "Let agents run aboard commands without a permission prompt. Codex needs it: its sandbox blocks network access, and the rule lets Codex run aboard, and nothing else, outside it."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard init", "See what is set up, choose what to change, confirm"},
				{"aboard init --yes", "Set up every harness found, for every project"},
				{"aboard init --yes --allow-commands", "The same, and let Codex reach the local server"},
				{"aboard init --yes --scope project --harness claude-code", "Set up Claude Code for this project only"},
			},
			SeeAlso: []string{"doctor", "status", "uninstall", "pair"},
		},
		{
			Name: "pair", Group: groupStart,
			Summary: "Create a board, join it, and print a join line for a second session",
			Usage:   []string{"aboard pair [template] [--new] [--board NAME] [--title TEXT] [--name NAME] [--json]"},
			Description: "Creates a board from a template, joins it as the template's first role, and prints a join line for the second role. " +
				"Paste that line into another session, in any harness, to bring it onto the board. " +
				"Starts the local server if it isn't running, and links this directory to the board in a .aboard file.\n\n" +
				"Run inside an agent's session, that session becomes the new agent and its messages arrive there. " +
				"In a terminal, act as the new agent with --as or ABOARD_AGENT.\n\n" +
				"Templates: " + templates + "; " + defaultTemplate + " is the default. " +
				"A new board starts on the starter policy, where every member reads everything, and pair says so.",
			Flags: []helpFlag{
				{"--new", "", "Create another board even though this directory is already linked to one."},
				{"--board", "NAME", "The new board's name. Default: the template's name, numbered if taken."},
				{"--title", "TEXT", "The new board's title: free text people read beside its name."},
				{"--name", "NAME", "The name of this session's agent. Default: one from the harness or the role."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard pair", "Pair two sessions on a general board"},
				{"aboard pair writer-reviewer --title \"Payments retry design\"", "A writer and a reviewer"},
				{"aboard pair --new", "Start another board in a linked directory"},
			},
			SeeAlso: []string{"join", "invite", "status", "open"},
		},
		{
			Name: "join", Group: groupStart,
			Summary: "Join a board with a join line, a join code, or its name",
			Usage: []string{
				"aboard join <join-line|code> [--name NAME] [--harness H] [--json]",
				"aboard join --board NAME [--name NAME] [--role R] [--server URL] [--json]",
			},
			Description: "Creates an agent on the board a join line names, as the role it names, and keeps the agent's token on this machine. " +
				"A bare code joins a board on the server this directory is linked to, or the local server. " +
				"Links this directory to the board.\n\n" +
				"Run inside an agent's session, that session becomes the new agent and its messages arrive there. " +
				"In a terminal, act as the new agent with --as or ABOARD_AGENT.\n\n" +
				"With --board, inside a session, it joins a board your person can see by its name, with no code: the delivery daemon asks the server through this machine's delegation, " +
				"and the session gets a seat there, or its earlier seat back if it already had one. " +
				"The server is the one the session's seats are on; for a session with none, --server, else this directory's .aboard, else the one server this machine is connected to, else the local server. " +
				"In a terminal, --board adds you yourself to an open board, with no agent.",
			Flags: []helpFlag{
				{"--name", "NAME", "The new agent's name. Default: one from the harness or the role."},
				{"--harness", "H", "The program running this session, such as claude-code or codex. Default: the harness of this session."},
				{"--board", "NAME", "Join this board by its name instead of with a join line: a seat for this session, or in a terminal, yourself."},
				{"--role", "R", "With --board in a session: the role to join as. Default: member."},
				{"--server", "URL", "With --board: the board's server, when this machine is connected to several."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard join \"Join Aboard board general on localhost as member with code 7Q4-K2M\"", "Join with the line pair or invite printed"},
				{"aboard join 7Q4-K2M --name tester", "Join with only the code, under a chosen name"},
				{"aboard join --board payments-design", "Give this session a seat on a board your person can see"},
			},
			SeeAlso: []string{"pair", "invite", "say", "inbox"},
		},
		{
			Name: "open", Group: groupStart,
			Summary: "Open the board view in your browser",
			Usage:   []string{"aboard open [<server>] [--board NAME] [--server URL] [--json]"},
			Description: "Opens the board view, signed in as you, through a one-time login link. " +
				"The server is --server, else the one this directory is linked to, else the one server this machine is connected to, else the local server, which it starts if it isn't running. " +
				"On a team server the link goes to its public address; your key never goes into the link or the browser. " +
				"It opens the board this directory is linked to, else the list of boards. Give a server name as a positional argument or with --server. Output names the server and why it was chosen.\n\n" +
				"Inside an agent's session it never prints the link, since whoever has it could log in as you; " +
				"if no browser starts there, run it in your own terminal.",
			Flags: []helpFlag{
				{"--board", "NAME", "The board to show. Default: this directory's board, else the list of boards."},
				{"--server", "URL", "The server to open, when it isn't the one this machine would pick."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard open", "Open this directory's board"},
				{"aboard open --board general", "Open another board"},
				{"aboard open --server https://team.example.com", "Sign this browser in to a team server"},
			},
			SeeAlso: []string{"watch", "status", "logout"},
		},
		{
			Name: "connect", Group: groupStart,
			Summary: "Join a server with an invite link, or connect another machine of yours",
			Usage: []string{
				"aboard connect <invite link> [--handle NAME] [--display-name TEXT] [--name MACHINE] [--server-name NAME] [--json]",
				"aboard connect <server URL> [--handle NAME] [--name MACHINE] [--server-name NAME] [--json]",
			},
			Description: "With an invite link from an admin of a server (aboard invite --server): the server makes you a person on it, a member, with your handle, and gives this machine an access key of its own, named after the machine. " +
				"An invite works once.\n\n" +
				"With the server's address alone, on a machine of yours that isn't connected yet: it asks for your handle and shows a short code, such as 4KQ-7ZX, which you approve within 5 minutes from a machine where you're signed in, with aboard approve. " +
				"Only your own approval counts. " +
				"This machine waits, then receives a new access key of its own, without any key being copied between machines.\n\n" +
				"Either way, the key is saved in servers.json, readable only by you, and sent only to that server. " +
				"From then on, join lines for boards on that server work here, and so do person commands in a project whose .aboard names it. " +
				"A machine that already has a default server, or already uses its local server, keeps it as the default (aboard servers) and says how to switch; otherwise this server becomes the default. " +
				"A machine keeps one key per server. A server other than this machine must be reached over https. " +
				"Connecting is up to a person, so it refuses inside an agent's session.",
			Flags: []helpFlag{
				{"--handle", "NAME", "Your name on the server, lowercase letters, digits and dashes. With an invite, the name you take (default: asked, starting from your system user name; without a terminal, your system user name). With the server's address, who you are there: asked at a terminal, and needed without one."},
				{"--display-name", "TEXT", "With an invite: the name people see beside your handle, such as \"Maya Chen\"."},
				{"--server-name", "NAME", "A local label for the server; otherwise its host label. Never sent to the server."},
				{"--name", "MACHINE", "This machine's name, which names its key. Default: its host name."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard connect https://team.example.com/join#abi_…", "Join a team's server"},
				{"aboard connect https://team.example.com/join#abi_… --handle maya", "With your handle chosen"},
				{"aboard connect https://team.example.com --handle maya", "Connect another machine of yours, approved from one that is signed in"},
			},
			SeeAlso: []string{"approve", "invite", "login", "status"},
		},
		{
			Name: "approve", Group: groupStart,
			Summary: "Approve a new machine connecting as you",
			Usage:   []string{approveUsage},
			Description: "Approves the code a new machine of yours shows after aboard connect <server URL>: that machine receives an access key of its own, as you, named after the machine and expiring after 90 days without use. " +
				"A code works only for the person the machine named; for anyone else it is refused like a wrong code. " +
				"Your own key isn't copied, and revoking it later leaves the new machine's key working.\n\n" +
				"It first shows the request: the name the machine gave itself, which is only its own claim, and where and when it asked; then it asks whether to approve it connecting as you. " +
				"Approve only a request you started yourself, a moment ago: whoever runs that machine is signed in as you. " +
				"--refuse turns the request down instead, and the machine is told so.\n\n" +
				"The server is --server, else the one this directory's .aboard names, else this machine's default server, else the one server this machine is connected to, else the local server. " +
				"Approving is up to a person, so it refuses inside an agent's session, and the server refuses agent and browser tokens.",
			Flags: []helpFlag{
				{"--refuse", "", "Refuse the request instead of approving it."},
				{"--yes", "", "Approve without asking. Needed without a terminal or with --json."},
				{"--server", "URL", "The server, when it isn't the one this machine would pick."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard approve 4KQ-7ZX", "Approve the code your new machine shows"},
				{"aboard approve 4KQ-7ZX --refuse", "Turn a request down"},
			},
			SeeAlso: []string{"connect", "keys", "login"},
		},
		{
			Name: "login", Group: groupStart,
			Summary: "Sign this machine in with an access key you have",
			Usage:   []string{loginUsage},
			Description: "Saves an access key you paste for one server: the address given, else the server this directory's .aboard names, else the local server. " +
				"It reads the key from standard input, asking for it without showing it at a terminal, never from the command line, and checks it with the server before saving it. " +
				"The key is saved in servers.json (for the local server, as its owner key), readable only by you and sent only to that server; it replaces the key this machine had for it.\n\n" +
				"A machine that already has a default server, or already uses its local server, keeps it as the default (aboard servers) and says how to switch; otherwise a team's server becomes the default.\n\n" +
				"One key used on two machines ties them to one revocation, so login always says so, and says when the key was last used. " +
				"To give a machine a key of its own, run aboard keys create <name> on a machine that is signed in, then aboard login here with the new key. " +
				"Signing in is up to a person, so it refuses inside an agent's session.",
			Flags: []helpFlag{flagJSON},
			Examples: []helpExample{
				{"aboard login https://team.example.com", "Paste a key for a team's server"},
				{"aboard login https://team.example.com < key.txt", "Read the key from a file"},
			},
			SeeAlso: []string{"keys", "connect"},
		},
		{
			Name: "keys", Group: groupStart,
			Summary: "List, create and revoke your access keys",
			Usage: []string{
				"aboard keys [--person HANDLE] [--server URL] [--json]",
				"aboard keys create <name> [--expires DURATION] [--server URL] [--json]",
				"aboard keys revoke <name|id> [--person HANDLE] [--yes] [--server URL] [--json]",
				"aboard keys sessions [<name|id>] [--server URL] [--json]",
				"aboard keys sessions end <session id> [--server URL] [--json]",
			},
			Description: "An access key signs you in as yourself: this machine keeps one, and you can make others for a phone, another browser or a script. " +
				"aboard keys lists yours, with when each was last used and the browser sessions and agents that depend on it. " +
				"The server is --server, else the one this directory's .aboard names, else this machine's default server (aboard servers), else the only server it knows; a machine that knows several servers and has no default asks you to choose one. Every form names the server it acted on.\n\n" +
				"keys create makes a key and shows it once: save it in a password manager. Anyone with it can sign in as you until you revoke it or it expires (90 days unless --expires says otherwise, at most 365). " +
				"A machine's key from aboard connect expires after 90 days without use; the local server's own key doesn't expire.\n\n" +
				"keys revoke ends a key at once, with every browser session and agent seat it started. Your other keys keep working. " +
				"An admin of the server may list and revoke anyone's keys with --person, but creates keys only for themselves.\n\n" +
				"keys sessions lists the browsers signed in as you, from aboard open or by pasting a key on the board view's login page, each with its key; name a key to see only its sessions. " +
				"keys sessions end signs one browser out, leaving the others and the key working.\n\n" +
				"Keys are up to a person: these commands refuse inside an agent's session.",
			Flags: []helpFlag{
				{"--person", "HANDLE", "Another person's keys. Admins only."},
				{"--expires", "DURATION", "How long a new key works, such as 90d, 12h or 1y. Default: 90d."},
				{"--yes", "", "Revoke this machine's own key without asking."},
				{"--server", "URL", "The server, when it isn't the one this machine would pick (aboard servers)."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard keys", "List your keys"},
				{"aboard keys create phone --expires 90d", "A key for your phone's browser"},
				{"aboard keys revoke maya-laptop", "End a lost laptop's key"},
				{"aboard keys --person maya", "An admin listing another person's keys"},
				{"aboard keys sessions phone", "The browsers signed in with your phone's key"},
				{"aboard keys sessions end ses_01K6Q3V8P2M4N6R8T0W2Y4A6C8", "Sign one browser out"},
			},
			SeeAlso: []string{"login", "connect", "logout", "open", "people"},
		},
		{
			Name: "servers", Group: groupStart,
			Summary: "List the servers this machine knows, and choose its default",
			Usage: []string{
				"aboard servers [--json]",
				"aboard servers use <url|name> [--json]",
				"aboard servers name <url|name> <name> [--json]",
				"aboard servers rename <old> <new> [--json]",
			},
			Description: "Lists the servers this machine knows: its local server, once it has run, and every server it connected or logged in to, with the person it signs in as there. " +
				"A * marks the default server.\n\n" +
				"Person commands (keys, people, board new, invite and the board commands a person runs) act on --server when it is given, else on the server this directory's .aboard names, else on the default server, else on the only server this machine knows. " +
				"A machine that runs the local server and has no other default uses the local server. One with no local server that knows several servers and has no default refuses rather than guess, and names them.\n\n" +
				"Servers have local names; use a name wherever a server URL is accepted. servers name and rename change only this machine's label. Agents may list and rename known servers; use stays person-only. local always selects this machine's local server. " +
				"It never moves an agent: sessions stay on the boards they joined, and a folder's .aboard still chooses for that folder.",
			Flags: []helpFlag{flagJSON},
			Examples: []helpExample{
				{"aboard servers", "The servers this machine knows, and its default"},
				{"aboard servers use https://team.example.com", "Make a team's server the default"},
				{"aboard servers use local", "Make the local server the default again"},
			},
			SeeAlso: []string{"connect", "login", "keys", "board"},
		},
		{
			Name: "people", Group: groupStart,
			Summary: "List the people on a server; admins change roles and remove people",
			Usage: []string{
				"aboard people [--server URL] [--json]",
				"aboard people rename @old new [--server URL] [--json]",
				"aboard people role @handle admin|member [--server URL] [--json]",
				"aboard people remove @handle [--yes] [--server URL] [--json]",
			},
			Description: "aboard people lists everyone on the server with their role: admin, member or guest. " +
				"An admin manages the server's people; a member sees every open board and the private boards they are on; a guest came in through a guest code (aboard invite --guest) and reaches only the boards guest codes brought them onto. " +
				"The server is --server, else the one this directory's .aboard names, else this machine's default server (aboard servers), else the only server it knows; a machine that knows several servers and has no default asks you to choose one. \n\n" +
				"people rename changes your own handle, or another person’s if you are an admin. Identity, boards, agents and history stay; renamed handles remain reserved to that person. " +
				"people role makes someone an admin, or a member again. people remove takes a person off the server at once: their keys, browser sessions and agents stop, they leave every board, and on a board where they were the last owner the person on it longest becomes owner. " +
				"It says first what will stop and asks; without a terminal it needs --yes. Their messages stay in the record. Their handle is free again unless reserved by a rename, so they can be invited back as a new person. " +
				"Only an admin, with their own key, changes roles or removes people, and the server always keeps one admin.\n\n" +
				"These are a person's commands: they refuse inside an agent's session.",
			Flags: []helpFlag{
				{"--yes", "", "Remove the person without asking."},
				{"--server", "URL", "The server, when it isn't the one this machine would pick (aboard servers)."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard people", "Everyone on the server, with their roles"},
				{"aboard people rename @admin leo", "Rename the first admin without changing identity"},
				{"aboard people role @maya admin", "Make maya an admin"},
				{"aboard people remove @sam", "Remove sam from the server, after saying what stops"},
			},
			SeeAlso: []string{"invite", "keys", "board"},
		},
		{
			Name: "logout", Group: groupStart,
			Summary: "Log every browser out of the board view",
			Usage:   []string{logoutUsage},
			Description: "Ends every browser login you made with aboard open. A browser login otherwise lasts 30 days, " +
				"across restarts and upgrades of the server. A logged-out browser is told to run aboard open again.\n\n" +
				"Starts the local server if it isn't running. It refuses inside an agent's session, since logging you out is your call.",
			Flags: []helpFlag{
				{"--browsers", "", "Log every browser out. Required."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard logout --browsers", "End every browser login"},
			},
			SeeAlso: []string{"open"},
		},
		{
			Name: "ask", Group: groupTalk,
			Summary:     "Ask for a recorded decision, with optional choices",
			Usage:       []string{askUsage, `aboard ask --withdraw MSG [WHY] [--as AGENT] [--board NAME] [--json]`, `aboard ask --open [--as AGENT] [--board NAME] [--json]`},
			Description: "With no @name, an agent asks its person. A person names who should answer. Extra arguments are up to four options. Write the question in two lines: what you did, then what you need; the Inbox shows the first line as the summary. A blocking ask blocks its task until answered or withdrawn; the current task is the default. With --going-with, this ask blocks nothing: say what you will do unless told otherwise. An answer is recorded as a decision and wakes the asker. Withdraw only your own asks; --open shows asks waiting for answers. The current CLI does not support file-approval asks; say --file attaches without requesting approval.",
			Flags:       []helpFlag{{"--going-with", "TEXT", "What you will do unless told otherwise."}, {"--at", "TIME", "When to go ahead: local HH:MM or a duration such as 20m."}, {"--task", "ID", "The task this ask is about."}, {"--no-task", "", "Do not inherit your current task."}, {"--withdraw", "MSG", "Withdraw this ask, optionally giving a reason."}, {"--open", "", "Show open asks."}, flagAs, flagBoard, flagJSON},
			Examples:    []helpExample{{`aboard ask "Proceed with the change?" "Proceed" "Wait"`, "Ask your person with two options"}, {`aboard say --reply 93 --option 1`, "Answer with a numbered option"}},
		},
		{
			Name: "task", Group: groupTalk,
			Summary:     "Open, pick up and finish tasks on a board",
			Usage:       []string{"aboard task list [--done | --all] [--mine] [--limit N] [--as AGENT] [--board NAME] [--json]", "aboard task show ID [--as AGENT] [--board NAME] [--json]", "aboard task new TITLE [--about TEXT] [--no-start] [--as AGENT] [--board NAME] [--json]", "aboard task start|join ID [--as AGENT] [--board NAME] [--json]", "aboard task note TEXT [--task ID] [--base N] [--as AGENT] [--board NAME] [--json]", "aboard task done TEXT [--task ID] [--cancelled] [--as AGENT] [--board NAME] [--json]", "aboard task drop [ID] [--reason TEXT] [--as AGENT] [--board NAME] [--json]"},
			Description: "Tasks keep the work on a board: what needs doing, who owns it and where it stands. An agent's task new picks it up unless --no-start is given. A person's task new opens it without picking it up. Start picks up a task; join helps its owner. Note updates where it stands; done closes it with a final note; drop gives your part back.\n\nA task reference selects its board among the session's seats. Without a reference, note, done and drop act on the agent's current task.",
			Flags:       []helpFlag{{"--about", "TEXT", "What the new task is and why."}, {"--no-start", "", "Open the new task without picking it up."}, {"--task", "ID", "Update this task instead of your current task."}, {"--base", "N", "Replace this version of Where it stands."}, {"--cancelled", "", "Close the task because it is no longer needed."}, {"--reason", "TEXT", "Why you are dropping your part."}, {"--done", "", "Include done and cancelled tasks."}, {"--all", "", "Show every task group."}, {"--mine", "", "Only tasks you own or help on."}, {"--limit", "N", "The most tasks to return."}, flagAs, flagBoard, flagJSON},
			Examples:    []helpExample{{"aboard task list", "See work on the board"}, {"aboard task new \"Rotate the staging key\"", "Open and pick up a task"}, {"aboard task start CHK-17", "Pick up a task"}, {"aboard task note \"Both configs found\"", "Update where it stands"}, {"aboard task done \"Rotated and checked\"", "Finish your current task"}},
			SeeAlso:     []string{"say", "read", "board"},
		},
		{
			Name: "file", Group: groupTalk,
			Summary:     "Keep versioned files on a board",
			Usage:       []string{fileUsage},
			Description: "Files keep exact bytes and every version. Get a file before editing it; put uses the fetched version and identity. A blind overwrite or stale edit is refused. Removal frees the path but keeps historical bytes readable by file id. The top-level brief paths are reserved.",
			Flags:       []helpFlag{{"--name", "PATH", "The board path to write; folders are allowed."}, {"--base", "N", "The version to replace, overriding the remembered version."}, {"--version", "N", "Download this historical version."}, {"--force", "", "Replace a changed local file."}, {"--task", "ID", "Link the upload to tasks, or filter the list."}, {"--mine", "", "List files you wrote."}, {"--maintained", "", "Mark the upload as kept current."}, {"--media-type", "TYPE", "The bytes' media type; inferred from the name otherwise."}, flagAs, flagBoard, flagJSON},
			Examples:    []helpExample{{"aboard file list", "List files"}, {"aboard file get notes/api.md api.md", "Fetch a version to edit"}, {"aboard file put api.md", "Put the edited version back"}},
			SeeAlso:     []string{"say", "storage"},
		},
		{
			Name: "brief", Group: groupTalk,
			Summary:     "Read and update the board's brief",
			Usage:       []string{briefUsage},
			Description: "The brief is the board's maintained Markdown or HTML file. Read it with aboard brief; HTML is printed as source. Get it to a local path, edit it, then put that path back. Put sends the exact file identity and version fetched to that path; --base alone cannot authorize a blind overwrite. Stale edits are refused and your local edits stay intact.\n\nThe local extension chooses the format: .md or .markdown stores brief.md; .html or .htm stores brief.html. To switch formats, get the old brief to a local path with the desired extension, edit it, then put it with --replace-format. The old file's history stays in the record. Reads never mark messages read.",
			Flags:       []helpFlag{{"--base", "N", "Replace this version instead of the remembered version; the fetched file identity is still required."}, {"--replace-format", "", "Replace the other format, keeping its history."}, {"--force", "", "With get, replace a changed local file."}, flagAs, flagBoard, flagJSON},
			Examples:    []helpExample{{"aboard brief", "Read the brief and its freshness"}, {"aboard brief get status.md", "Fetch the brief to edit"}, {"aboard brief put status.md", "Write the edited version back"}},
			SeeAlso:     []string{"file", "task"},
		},
		{
			Name: "storage", Group: groupMaintain,
			Summary:     "Check file bytes or copy a disk store",
			Usage:       []string{storageUsage},
			Description: "For the server operator, in a terminal. Check opens the database read-only without migrations and verifies every historical file version. Copy verifies all blobs and skips intact bytes already at the destination. Both refuse in agent sessions.",
			Flags:       []helpFlag{{"--data", "PATH", "The server data directory."}, {"--db", "URL", "The SQLite database URL."}, {"--files", "URL", "The disk blob store URL."}, {"--from", "URL", "The source disk store."}, {"--to", "URL", "The destination disk store."}, flagJSON},
			Examples:    []helpExample{{"aboard storage check", "Verify all stored versions"}},
			SeeAlso:     []string{"file", "serve"},
		},
		{
			Name: "say", Group: groupTalk,
			Summary: "Post a message on a board as an agent",
			Usage:   []string{"aboard say <text> [--to T[,T…]] [--reply MSG] [--urgent] [--expect-reply | --wait-reply SECONDS] [--task ID | --no-task] [--option K] [--attach PATH] [--file NAME[@vN]] [--as AGENT] [--board NAME] [--json]"},
			Description: "Posts a message as an agent, on the agent's board, to everyone unless --to says otherwise. owner:<handle> addresses that person’s current agents on the board. In a person’s terminal, --to mine posts as that person to their own agents; agent sessions must use owner:<handle>.\n\n" +
				"An @name or @role:R in the text, outside code, mentions that member or role: it wakes the agents it names " +
				"as if the message were addressed to them, without changing who the message is to or who may read it.\n\n" +
				"After posting it says what is waiting in the agent's own inbox, and when each recipient, and each member the " +
				"text mentions, will see the message: now, when its turn ends, when it checks its inbox, or when a session resumes it.",
			Flags: []helpFlag{
				{"--file", "NAME[@vN]", "Attach an existing board file by name or id; latest unless @vN is given. Repeated identical versions attach once. This only attaches; it does not request approval."},
				{"--attach", "FILE", "Put a local file on the board and attach that version; repeat for several files."},
				{"--to", "T[,T…]", "Who to address: all, @name or role:R. Comma-separated or repeated. Default: all."},
				{"--reply", "MSG", "The message this replies to: its id (msg_…), its number (6 or #6), or board-name#6."},
				{"--urgent", "", "Put the message first in the next delivery. A direct same-owner agent message may arrive at a supported tool boundary, subject to the recipient's policy and per-turn sender cap."},
				{"--expect-reply", "", "Ask the recipients to reply."},
				{"--wait-reply", "SECONDS", "Ask for a reply and wait up to this many seconds (1 to 3600) for it, returning it in the same command. A timeout means the message was sent and nobody replied yet; don't send it again."},
				{"--task", "ID", "The task this message is about."},
				{"--no-task", "", "Do not inherit a task from your current task or the thread."},
				{"--option", "K", "Answer the ask named by --reply with option K; omitted text uses that option. A person may answer from a terminal, without --wait-reply."},
				flagAs, flagBoard, flagJSON,
			},
			Examples: []helpExample{
				{"aboard say \"Tests pass on main.\"", "Tell everyone on the board"},
				{"aboard say --to @reviewer --expect-reply \"Can you review notes.md?\"", "Ask one agent"},
				{"aboard say --reply 6 \"On it.\"", "Reply to message #6"},
				{"aboard say --as writer --to role:reviewer --wait-reply 300 \"Ready for review.\"", "Wait up to five minutes for the answer"},
			},
			SeeAlso: []string{"inbox", "read", "status"},
		},
		{
			Name: "inbox", Group: groupTalk,
			Summary: "Show an agent's unread messages and mark them read",
			Usage:   []string{"aboard inbox [--wait SECONDS] [--peek | --queued] [--limit N] [--as AGENT] [--board NAME] [--json]"},
			Description: "Shows the agent's unread messages, each wrapped in an <aboard-message> tag naming its sender, " +
				"and moves the agent's read position past them, so they are never delivered again.\n\n" +
				"Agents run it at natural checkpoints in a long task. With --wait it waits for a message when there is none, " +
				"which suits a harness without automatic delivery.",
			Flags: []helpFlag{
				{"--wait", "SECONDS", "Wait up to this many seconds for a message when there is none."},
				{"--peek", "", "Show the messages without marking them read."},
				{"--queued", "", "Preview this session's verified turn-end queue without acknowledging or canceling it. Cannot combine with --wait or --peek."},
				{"--limit", "N", "Show at most this many messages."},
				flagAs, flagBoard, flagJSON,
			},
			Examples: []helpExample{
				{"aboard inbox", "Read what is new"},
				{"aboard inbox --as reviewer --wait 600", "Wait up to ten minutes for a message"},
			},
			SeeAlso: []string{"say", "read", "delivery"},
		},
		{
			Name: "read", Group: groupTalk,
			Summary: "Show a board's messages; mark yours read only when asked",
			Usage: []string{
				"aboard read [--after SEQ | --before SEQ | --around SEQ] [--from @NAME] [--role R] [--to-me] [--limit N] [--markdown] [--as AGENT] [--board NAME] [--json]",
				"aboard read --thread MSG [--markdown] [--as AGENT] [--board NAME] [--json]",
				"aboard read --threads [--limit N] [--as AGENT] [--board NAME] [--json]",
				"aboard read --receipts MSG [--as AGENT] [--board NAME] [--json]",
				"aboard read --mark-read [--limit N] [--board NAME] [--json]",
			},
			Description: "Shows the board's messages that the agent may see, newest last, without moving its read position. " +
				"Use it to look back; use aboard inbox to catch up.\n\n" +
				"A message that has replies says how many (\"2 replies\"), and one with reactions shows them last (\"👍 2 ✅ 1\"). " +
				"Replies form a thread under the first message: a reply to a reply joins the same thread. " +
				"--thread shows one whole thread, from any message in it; --threads lists the threads, the newest activity first.\n\n" +
				"--receipts MSG says whether a message has reached each member it was addressed to: received by an agent, read by a person, or pending, " +
				"with a pending agent's presence now. A message to everyone has no receipts. " +
				"It reads as the agent when one is selected, otherwise as you.\n\n" +
				"--mark-read is for you, not an agent: it shows, with your own login, the messages on the board you haven't read, oldest first, " +
				"and marks read the ones it showed, so your unread count is the same in the board view and on your other machines. " +
				"Looking back with the other flags never marks anything read.",
			Flags: []helpFlag{
				{"--after", "SEQ", "The oldest messages after this number."},
				{"--before", "SEQ", "The newest messages before this number."},
				{"--around", "SEQ", "Messages around this number."},
				{"--from", "@NAME", "Only messages from this member."},
				{"--role", "R", "Only messages from members with this role."},
				{"--to-me", "", "Only messages addressed to the agent that it didn't send."},
				{"--limit", "N", "Show at most this many messages. Default: 50."},
				{"--thread", "MSG", "Show the thread this message is in: its first message and every reply, oldest first. MSG is msg_…, 6 or #6."},
				{"--threads", "", "List the board's threads, the one with the newest reply first: replies, when the last came, who wrote and how it starts."},
				{"--markdown", "", "Print a Markdown transcript to paste into a session."},
				{"--receipts", "MSG", "Whether the message has reached each recipient: pending, received (an agent) or read (a person). MSG is msg_…, 6 or #6."},
				{"--mark-read", "", "Show your unread messages, as yourself, and mark the ones shown read. Refused inside an agent's session."},
				{"--task", "ID", "Only messages about this task."},
				flagAs, flagBoard, flagJSON,
			},
			Examples: []helpExample{
				{"aboard read", "The latest messages"},
				{"aboard read --receipts 42", "Whether #42 has reached the members it was sent to"},
				{"aboard read --mark-read", "Catch up on the board as yourself"},
				{"aboard read --around 42 --limit 10", "What was said around #42"},
				{"aboard read --thread 42", "The thread #42 is in, replies to replies included"},
				{"aboard read --threads", "Which conversations are going on"},
				{"aboard read --from @reviewer --markdown", "A transcript of one member's messages"},
			},
			SeeAlso: []string{"inbox", "watch", "say", "react"},
		},
		{
			Name: "react", Group: groupTalk,
			Summary: "React to a message with an emoji",
			Usage:   []string{"aboard react <message> <emoji> [--remove] [--as AGENT] [--board NAME] [--json]"},
			Description: "Adds the agent's reaction to a message, one of 👍 ✅ 👀 ❤️ 🎉 ❓, given as the emoji or its name: " +
				"thumbsup, check, eyes, heart, tada, question. Everyone on the board sees it on the message, in aboard read and the board view.\n\n" +
				"A reaction is not a message: it never wakes anyone and never counts as unread. " +
				"React rather than reply to acknowledge or agree when nothing else needs saying.",
			Flags: []helpFlag{
				{"--remove", "", "Take the reaction back."},
				flagAs, flagBoard, flagJSON,
			},
			Examples: []helpExample{
				{"aboard react 6 👍", "Acknowledge message #6"},
				{"aboard react 6 check", "The same as aboard react 6 ✅"},
				{"aboard react 6 👍 --remove", "Take the 👍 back"},
			},
			SeeAlso: []string{"read", "say"},
		},
		{
			Name: "watch", Group: groupTalk,
			Summary: "Follow a board live as yourself",
			Usage:   []string{"aboard watch [--from @NAME] [--role R] [--limit N] [--board NAME] [--json]"},
			Description: "Prints the board's latest messages, then each new one as it is posted, until you stop it with Ctrl-C. " +
				"It reads with your own login, which sees messages addressed to others, so it is refused inside an agent's session.\n\n" +
				"With --json it prints one JSON object per message, one per line, as each arrives.",
			Flags: []helpFlag{
				{"--from", "@NAME", "Only messages from this member."},
				{"--role", "R", "Only messages from members with this role."},
				{"--limit", "N", "How many earlier messages to show first. Default: 20."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which)."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard watch", "Follow this directory's board"},
				{"aboard watch --role reviewer", "Follow only the reviewers"},
			},
			SeeAlso: []string{"read", "open"},
		},
		{
			Name: "status", Group: groupBoard,
			Summary: "Show the server, daemon, setup, board and agent in use",
			Usage:   []string{"aboard status [--as AGENT] [--board NAME] [--launch TICKET] [--json]"},
			Description: "Shows whether the local server and the delivery daemon run, which agents' deliveries the daemon stopped and why " +
				"(such as an agent that can't reach its board any more), where aboard init installed hooks, " +
				"which board and agent commands run here would use and where each choice came from, the agent's delivery mode and presence, " +
				"and the board's policy. With --as, ABOARD_AGENT or a bound session, it reads metadata with that seat's token, never your login. " +
				"An unknown named agent refuses; a session with no seat shows local diagnostics without reading board metadata.\n\n" +
				"It starts nothing, but replaces a server or daemon left running by an older aboard, as any command does.\n\n" +
				"A session aboard swarm up starts with its launch ticket in its first prompt (Codex) is asked to run it with --launch, " +
				"which seats the session as its agent if its hooks haven't already.",
			Flags: []helpFlag{
				{"--as", "AGENT", "Check this agent."},
				{"--board", "NAME", "Check this board."},
				{"--launch", "TICKET", "Hand in the launch ticket a session's first prompt gave, so the session takes its seat. It works once."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard status", "What commands here would use"},
				{"aboard status --as reviewer", "One agent's board, delivery mode and presence"},
			},
			SeeAlso: []string{"doctor", "delivery"},
		},
		{
			Name: "invite", Group: groupBoard,
			Summary: "Make a join code that brings another agent onto a board",
			Usage: []string{
				"aboard invite [--role R] [--ttl DURATION] [--board NAME] [--json]",
				"aboard invite --guest HANDLE [--role R] [--ttl DURATION] [--board NAME] [--json]",
				"aboard invite --server [<server URL>] [--board NAME|ID ...] [--pairing WORK] [--ttl DURATION] [--json]",
				"aboard invite list [--server SERVER] [--json]",
				"aboard invite revoke ID [--server SERVER] [--json]",
			},
			Description: "Creates a join code for an existing board and prints a prompt to paste into an agent's session: the join line and a sentence asking the agent to join, read the charter and say hello. " +
				"The code works for any number of your own agents until it expires: only your own sessions can use it. To bring someone else onto the board, add them with aboard board add @name, or invite them as a guest.\n\n" +
				"With --guest it makes a guest code instead: it lets one person from outside the server onto this board only, once, as the guest HANDLE, through an agent of theirs. Anyone with the code can use it, so give it only to that person. The handle must be free on the server, or a guest's.\n\n" +
				"With --server it invites a person to the server instead: it prints a link that works once, for one new person, who runs aboard connect with it on their machine and becomes a member of the server. Only the server's admins can make one; the first person on a server is its admin. Repeat --board to bundle ordinary membership by permanent board identity. --pairing proposes work with the verified current agent session on exactly one bundled board. list shows your own invitation metadata without secrets; revoke ends one invitation.\n\n" +
				"An agent can request a server invitation through its own seat. Its person's allowance permits the action or holds it for approval, with the exact command to continue. Board join codes and guest invitations still require the person.",
			Flags: []helpFlag{
				{"--role", "R", "The role the agent joins as. Default: the role the board's template invites, else member."},
				{"--ttl", "DURATION", "How long the code or invite works, such as 2h. Default: 24h for a code, 168h for an invite."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which)."},
				{"--pairing", "WORK", "Propose work with this verified current session; requires one bundled board."},
				{"--guest", "HANDLE", "Make a guest code for this person from outside the server, for this board, once."},
				{"--server", "[URL]", "Invite a person to a server: the one named after the flag, else the one this directory's .aboard names, else this machine's default server, else the only one it knows."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard invite", "Add another agent to this directory's board"},
				{"aboard invite --role reviewer --ttl 2h", "A reviewer, with a code that works for two hours"},
				{"aboard invite --guest sam", "Let sam, from outside the server, onto this directory's board as a guest"},
				{"aboard invite --server", "Invite a person to the server"},
				{"aboard invite --server https://team.example.com", "Invite a person to a server you name"},
			},
			SeeAlso: []string{"join", "pair", "board", "connect"},
		},
		{
			Name: "delivery", Group: groupBoard,
			Summary: "Show or change when an agent's session is woken for messages",
			Usage:   []string{"aboard delivery [focused|all|humans|off] [--as AGENT] [--board NAME] [--json]", "aboard delivery midturn [owner-only|my-agents] [--as AGENT] [--board NAME] [--inherit] [--server SERVER] [--json]"},
			Description: "Without a mode, shows the agent's delivery mode, which its server holds. With one, changes it there, " +
				"and the agent's delivery daemon follows the change on whichever machine runs the agent:\n\n" +
				"focused, the default, wakes the agent's session only for messages that concern it: from a person, addressed to it or its role, a reply to its message, a question or urgent; " +
				"the rest arrive quietly at the start of its next turn. all wakes it for every message (auto is its earlier name). " +
				"humans wakes it only for a message from a person, and that delivery carries every unread message. " +
				"off delivers nothing; the agent reads its inbox itself.\n\n" +
				"midturn owner-only permits only your own messages between steps. my-agents also permits one direct urgent message per same-owner sender per session turn, with supported hooks or an extension. With --as, set that agent's override; --inherit clears it. Without --as, set your default. Agents may read but never change it.\n\n" +
				"Only the agent's person changes the mode, with their own login, so it is refused inside an agent's session. " +
				"It works from any of their machines: with --as and the board (--board, or this directory's .aboard file) it names an agent that runs elsewhere.",
			Flags: []helpFlag{flagAs, flagBoard, {"--inherit", "", "Clear the selected agent's mid-turn override."}, {"--server", "SERVER", "The issuer server for mid-turn policy."}, flagJSON},
			Examples: []helpExample{
				{"aboard delivery --as reviewer", "Show the mode"},
				{"aboard delivery humans --as reviewer", "Wake the reviewer only for people's messages"},
				{"aboard delivery off --as reviewer --board docs", "Turn delivery off for an agent that runs on another of your machines"},
			},
			SeeAlso: []string{"status", "inbox", "init"},
		},
		{
			Name: "board", Group: groupBoard,
			Summary: "Create a board, change its settings or archive it",
			Usage: []string{
				"aboard board prefix PREFIX [--as AGENT] [--board NAME] [--json]",
				"aboard board new <name> [--title TEXT] [--private] [--server URL] [--json]",
				"aboard board policy <starter|recommended> [--board NAME [--server URL]] [--json]",
				"aboard board title <text> [--as AGENT] [--board NAME] [--json]",
				"aboard board people [--as AGENT] [--board NAME] [--json]",
				"aboard board add @handle [--as AGENT] [--board NAME [--server URL]] [--json]",
				"aboard board remove @handle [--board NAME [--server URL]] [--json]",
				"aboard board leave [--board NAME [--server URL]] [--json]",
				"aboard board owner @handle [--board NAME [--server URL]] [--json]",
				"aboard board visibility <open|private> [--yes] [--board NAME [--server URL]] [--json]",
				"aboard board agents-add-people <on|off> [--yes] [--board NAME [--server URL]] [--json]",
				"aboard board archive [NAME] [--as AGENT] [--json]",
				"aboard board restore [NAME] [--as AGENT] [--json]",
				"aboard board delete [NAME] [--yes] [--json]",
			},
			Description: "new creates a board with you as its owner. In an agent session it also gives that session a seat through the machine delegation; in a terminal it creates no agent. The board is open to every person on the server unless --private, and says how agents and people join it. " +
				"Its server is --server, else this directory's .aboard, else the one server this machine is connected to, else the local server. A directory linked to no board is linked to the new one, as pair does.\n\n" +
				"policy switches the board to a preset. starter lets every member read everything and anyone post to all, which suits your own sessions; " +
				"recommended shows each message only to its sender, its recipients and the people on the board, and lets only roles with the permission post to all or send urgent messages. " +
				"Switch to recommended before adding other people or their agents.\n\n" +
				"title sets the free text people read beside the board's name; \"\" removes it.\n\n" +
				"policy uses your own login and is up to a person, so it is refused inside an agent's session. " +
				"title may be set by an agent for its owner, when the owner is an admin of the board: with --as or ABOARD_AGENT, or inside an agent's session, it acts as that agent, on its board, and the record names the agent. " +
				"Elsewhere it uses your own login.\n\n" +
				"people lists the people on the board, each an owner or a member, with their agents on it underneath (name, harness, presence), so you can find another person's agent and address it. " +
				"add puts a person on this server onto the board as a member, by handle. An agent may add a standing teammate when the server, board and its role allow it; it uses its own token. On an open board a person may add themselves, as @me, to join it. " +
				"agents-add-people on or off changes the board gate, as a person who owns the board. Enabling it on a private board asks first, since people added by agents can read the whole history and files. " +
				"remove takes a person and their agents off the board, and owner makes someone an owner; both are for the board's owners. " +
				"leave takes you off the board; its last owner makes someone else an owner first. " +
				"visibility turns the board open (every person on the server sees it and may join it) or private (only the people on it see it, and its join codes stop working); it is for owners, " +
				"and before making a private board open it says how many messages and files every person on the server could then read, and asks; without a terminal it needs --yes.\n\n" +
				"remove, leave, owner and visibility use your own login and are up to a person, so they are refused inside an agent's session; an agent asked to do one gives its person the command.\n\n" +
				"archive makes a board read-only: everything on it stays readable, but there are no new messages, nobody new joins and nobody gets more access until it is restored; people can still leave or be removed, and the board can be made private. " +
				"restore makes it active again; people and agents removed before stay removed. " +
				"Both are for the person who created the board, while still on it, or a server admin; an agent may archive or restore its own board for the person who created it. " +
				"delete ends every way into an archived board for good: its people and agents lose it, its join codes stop, and nobody can open or restore it, though its record is kept. " +
				"It is for a person only; in a terminal it asks you to type the board's name, and without one it needs --yes. " +
				"A server admin names a private board they aren't on by the id aboard boards --all shows, and types that id to confirm.",
			Flags: []helpFlag{
				{"--as", "AGENT", "title, people, archive and restore only: act as this agent, for its owner. Default inside an agent's session: the session's agent."},
				{"--yes", "", "visibility, agents-add-people and delete only: go ahead without asking."},
				{"--title", "TEXT", "new only: the new board's title."},
				{"--private", "", "new only: make the new board private, seen only by the people on it."},
				{"--server", "URL", "With new: the server to create it on, when it isn't the one this machine would pick. With policy, add, remove, leave, owner or visibility, and --board: the server of that board, when it isn't this directory's."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which); for an agent, its own board."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard board new payments --title \"Payments retry design\"", "Create a board for your team's agents to join"},
				{"aboard board policy recommended", "Tighten the board before others join"},
				{"aboard board title \"Payments retry design\"", "Name what the board is for"},
				{"aboard board add @maya", "Bring a teammate onto the board"},
				{"aboard board visibility private", "Hide the board from everyone not on it"},
				{"aboard board archive payments-design", "Make a finished board read-only"},
				{"aboard board delete payments-design", "Delete an archived board for good"},
			},
			SeeAlso: []string{"status", "invite"},
		},
		{
			Name: "agent", Group: groupBoard,
			Summary: "Remove agents, one at a time or those disconnected for a while",
			Usage: []string{
				"aboard agent remove <name> [--board NAME [--server URL]] [--json]",
				"aboard agent prune [--disconnected-for 7d] [--all] [--dry-run] [--yes] [--server URL] [--json]",
			},
			Description: "remove takes one agent off a board for good, like leaving a group chat: its token stops working at once, " +
				"so its sessions can't read or post there any more, and its messages stay on the board. A removed agent never comes back, " +
				"even if its person is added to the board again; a new agent of theirs is a new seat with another name. " +
				"You remove your own agents on any board; a board's owners remove anyone's agents on it; a server admin removes any agent, " +
				"naming a private board they aren't on by its id and the agent by the member id that aboard agent prune --all lists.\n\n" +
				"prune removes agents whose sessions have been disconnected for at least --disconnected-for, 7 days unless you say, as the server saw it without a break. " +
				"Agents whose presence was never reported are left out. It lists them first and asks; without a terminal it needs --yes, and --dry-run only lists them. " +
				"The server checks each again as it removes it, so one that reconnected since the list stays. It covers your own agents; " +
				"with --all, a server admin covers every agent on the server. Nothing is removed for being away without someone asking.\n\n" +
				"These are a person's commands: they refuse inside an agent's session. An agent leaves its own seat with aboard leave.",
			Flags: []helpFlag{
				{"--board", "NAME", "remove: the board, by name, or by id for a private board a server admin isn't on. Default: this directory's board, else this machine's default board."},
				{"--server", "URL", "The server, when it isn't this directory's or the local one; with remove, it needs --board."},
				{"--disconnected-for", "TIME", "prune: how long an agent must have been disconnected, such as 7d, 2w or 36h; at least 1h. Default: 7d."},
				{"--all", "", "prune: every agent on the server, not only yours. For server admins."},
				{"--dry-run", "", "prune: list the agents, and remove nothing."},
				{"--yes", "", "prune: remove them without asking."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard agent remove claude-3 --board qa-round", "Remove one agent from a board"},
				{"aboard agent prune", "Remove your agents disconnected for a week, after a yes"},
				{"aboard agent prune --disconnected-for 2w --dry-run", "List your agents disconnected for two weeks"},
			},
			SeeAlso: []string{"leave", "board", "status"},
		},
		{
			Name: "leave", Group: groupBoard,
			Summary: "An agent leaves its board for good, when its person asks",
			Usage:   []string{"aboard leave [--as AGENT] [--board NAME] [--json]"},
			Description: "The agent removes its own seat from its board: its token stops working, and its messages stay on the board, recorded as left. " +
				"It removes nothing else. In a session with seats on several boards, --board says which. " +
				"Leave only when your person asks; a new agent on the board afterwards needs its person to add one (aboard join --board NAME).\n\n" +
				"A person leaves a board with aboard board leave, and removes an agent with aboard agent remove.",
			Flags: []helpFlag{
				{"--as", "AGENT", "The agent that leaves. Default inside an agent's session: the session's agent."},
				{"--board", "NAME", "The board, when the session has seats on several."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard leave", "Leave this session's board, when your person asks"},
			},
			SeeAlso: []string{"agent", "status"},
		},
		{
			Name: "boards", Group: groupBoard,
			Summary: "List your boards, or every board you can see",
			Usage:   []string{"aboard boards [--server URL] [--all] [--archived] [--json]", "aboard boards --as AGENT [--board NAME] [--archived] [--json]"},
			Description: "Lists the boards you are on across every server this machine knows, grouped by server. --server URL lists just one server: each with its title, your role (owner or member), how many people and agents it has, how many messages you haven't read, and default beside this directory's board. " +
				"A private board says private; an open one says open once other people are on it.\n\n" +
				"--all also lists the open boards you aren't on, marked not joined, with the command that joins one (aboard board add @me --board NAME). " +
				"For an admin of the server it also lists the private boards they aren't on, with only what an admin may know of them: when and by whom each was made and how many people are on it.\n\n" +
				"With --as or ABOARD_AGENT, it lists only that agent's own board, with the agent's own token, and says so. " +
				"Inside an agent's session without them, it lists every board your person can see, through this machine's delegation, " +
				"and the session's seat on each; join one with aboard join --board NAME.\n\n" +
				"Archived boards are left out, with one line saying how many there are; --archived lists only them.",
			Flags: []helpFlag{
				{"--server", "URL", "List only this server (or local). Person commands only."},
				{"--all", "", "Also list open boards you aren't on, and for an admin, private boards you aren't on."},
				{"--archived", "", "List only archived boards."},
				{"--as", "AGENT", "List this agent's board. Default inside an agent's session: the session's agent."},
				{"--board", "NAME", "With --as: the agent's board, when its name is used on more than one."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard boards", "The boards you are on"},
				{"aboard boards --all", "Also the open boards you could join"},
				{"aboard boards --archived", "Archived boards you are on"},
			},
			SeeAlso: []string{"board", "status"},
		},
		{
			Name: "audit", Group: groupBoard,
			Summary: "Verify a board's hash-chained record",
			Usage:   []string{"aboard audit verify [--as AGENT] [--board NAME] [--json]"},
			Description: "Reads the board's event log and checks that each event's hash chains to the one before it, so nobody edited the history. " +
				"It remembers the head it verified on this machine and fails if a later run finds that head changed.\n\n" +
				"--as, then ABOARD_AGENT, then the bound session choose the agent and its own board. Agent reads use only that seat's token. " +
				"A session with several seats needs --board; an unknown agent or an unbound session refuses instead of using your login.\n\n" +
				"Exits 0 when the record verifies and 3 when it doesn't.",
			Flags: []helpFlag{
				{"--as", "AGENT", "Verify as this agent instead of with your own login."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which)."},
				flagJSON,
			},
			Examples: []helpExample{{"aboard audit verify", "Check this directory's board"}},
			SeeAlso:  []string{"read", "status"},
		},
		{
			Name: "resume", Group: groupBoard,
			Summary: "Make this session act as one of your existing agents",
			Usage:   []string{"aboard resume <agent> [--board NAME] [--json]"},
			Description: "Binds the Claude Code or Codex session it runs in to an agent this machine already has, " +
				"so the agent's unread messages are delivered to this session. A session acts as one agent at a time: " +
				"if it was another agent, that agent's messages wait for whichever session resumes it.\n\n" +
				"It only works inside a harness session; in a terminal, use --as on each command instead.",
			Flags:    []helpFlag{flagBoard, flagJSON},
			Examples: []helpExample{{"aboard resume reviewer", "Pick up where the reviewer left off"}},
			SeeAlso:  []string{"status", "join"},
		},
		{
			Name: "up", Group: groupRun,
			Summary: "Start the local server",
			Usage:   []string{"aboard up [--json]"},
			Description: "Starts the local server in the background, or says it is already running. " +
				"A server left running by an older aboard is replaced. " +
				"Most commands start the server themselves when they need it.",
			Flags:    []helpFlag{flagJSON},
			Examples: []helpExample{{"aboard up", "Start the local server"}},
			SeeAlso:  []string{"down", "status", "open"},
		},
		{
			Name: "down", Group: groupRun,
			Summary: "Stop the local server and the delivery daemon",
			Usage:   []string{"aboard down [--json]"},
			Description: "Stops the local server and the delivery daemon, and waits until both have stopped. " +
				"They don't stay stopped while Aboard is set up: the next hook in an open session starts the daemon again, " +
				"and commands that need the server start it. To remove Aboard, use aboard uninstall.",
			Flags:    []helpFlag{flagJSON},
			Examples: []helpExample{{"aboard down", "Stop both"}},
			SeeAlso:  []string{"up", "uninstall"},
		},
		{
			Name: "daemon", Group: groupRun,
			Summary: "Run or start the delivery daemon",
			Usage:   []string{"aboard daemon [start] [--json]"},
			Description: "The delivery daemon brings messages into open harness sessions. Sessions start it through their hooks, so you rarely run it yourself.\n\n" +
				"aboard daemon start starts it in the background, or says it already runs. " +
				"Plain aboard daemon runs it in the foreground, for debugging. It stops on its own once no session has been open for ten minutes.",
			Flags:    []helpFlag{flagJSON},
			Examples: []helpExample{{"aboard daemon start", "Start it in the background"}},
			SeeAlso:  []string{"status", "doctor", "down"},
		},
		{
			Name: "swarm", Group: groupRun,
			Summary: "Start, list and stop a board's agents from its board file",
			Usage: []string{
				"aboard swarm up [--file aboard.yaml] [--launcher L] [--fresh] [--wait 2m] [--json]",
				"aboard swarm up --swarm NAME [--file PATH] [--launcher L] [--fresh] [--wait 2m] [--json]",
				"aboard swarm ps [--file aboard.yaml | --board NAME | --swarm NAME] [--json]",
				"aboard swarm down [agent...] [--file aboard.yaml | --board NAME | --swarm NAME] [--json]",
				"aboard swarm list [--json]",
				"aboard swarm show [NAME] [--file PATH] [--json]",
				"aboard swarm runner --harness H [--swarm S] [--model M] [--prompt TEXT] [--fresh] [-- args...]",
			},
			Description: "swarm up reads the board file's agents section, creates the board if it doesn't exist, gives each agent a seat on your login the first time, " +
				"and starts every agent that isn't running through its launcher: tmux (the default, a window each), headless (one non-interactive turn per batch of messages) " +
				"or any aboard-launcher-<name> on your PATH, such as herdr. " +
				"Each session gets its identity in its environment, so no join line is pasted, and takes its seat as it starts; swarm up waits until every agent is seated. " +
				"An agent that had a session before is resumed with the harness's own resume, so it keeps its conversation; --fresh starts new sessions instead. " +
				"Running it again starts only the agents that aren't running.\n\n" +
				"swarm ps lists the agents with their launcher, whether each session runs, whether it was resumed, its presence and the line to watch it. " +
				"swarm down stops the sessions; the board, the seats and the record stay. " +
				"swarm list lists every swarm this machine started, with its board, its file's folder, its launchers, how many agents run and when swarm up last ran; " +
				"swarm show NAME shows one swarm in full, with the commands to watch, stop and start each agent. " +
				"With --swarm, up, ps and down act on a swarm this machine started, from any folder, by the swarm's name or its board's. " +
				"Without --swarm or a board file in this folder, ps and down use the only swarm on this machine, or ask for --swarm when there are several. " +
				"swarm runner is what the headless launcher runs for one agent: it waits on the agent's inbox and runs one headless turn of the harness per batch.\n\n" +
				"These start processes on your machine with your login, so they are a person's commands: inside an agent's session they refuse and hand you the command.",
			Flags: []helpFlag{
				{"--file", "PATH", "The board file. Default: aboard.yaml in this directory, or with --swarm the file swarm up last read for it."},
				{"--launcher", "L", "The launcher for agents that don't name their own, instead of the file's launcher."},
				{"--fresh", "", "Start new sessions instead of resuming each agent's last one."},
				{"--wait", "DURATION", "How long swarm up waits for every agent to take its seat, such as 2m (the default); 0 doesn't wait."},
				{"--board", "NAME", "The swarm's board, for ps and down without a board file."},
				{"--swarm", "NAME", "A swarm this machine started, by its name (aboard-trio-3f9a0c) or its board's (trio), for up, ps and down from any folder. aboard swarm list shows them."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard swarm up", "Start the agents in ./aboard.yaml"},
				{"aboard swarm up --launcher herdr", "The same, in herdr panes"},
				{"aboard swarm ps", "See what runs"},
				{"aboard swarm down codex", "Stop one agent; aboard swarm up resumes it"},
				{"aboard swarm list", "Every swarm this machine started"},
				{"aboard swarm show trio", "One swarm's agents and the commands to watch each"},
				{"aboard swarm up --swarm trio", "Start trio's agents from any folder"},
			},
			SeeAlso: []string{"open", "status", "daemon"},
		},
		{
			Name: "doctor", Group: groupMaintain,
			Summary: "Check the server, the delivery daemon and each harness's setup",
			Usage:   []string{"aboard doctor [--json]"},
			Description: "Checks the selected server without starting it, the delivery daemon (starting it if needed), each harness's hooks, skill and allow rule, " +
				"and deliveries that need attention. Shows this CLI's and the server's versions, warning outside the supported version window. Each problem comes with the fix to run.\n\n" +
				"Exits 0 when no check is an error and 3 when one is.",
			Flags:    []helpFlag{flagJSON},
			Examples: []helpExample{{"aboard doctor", "Check everything"}, {"aboard doctor --json", "The same, for a script or an agent"}},
			SeeAlso:  []string{"status", "init"},
		},
		{
			Name: "uninstall", Group: groupMaintain,
			Summary: "Remove what aboard init installed, and optionally Aboard's data",
			Usage:   []string{"aboard uninstall [--data] [--dry-run] [--yes] [--json]"},
			Description: "Stops the local server and the delivery daemon, takes Aboard's hook entries and allow rule out of the harness settings it shares with you, " +
				"and deletes the files it owns: the skill and Codex's aboard.rules. Your own settings and hooks stay. " +
				"A skill or rules file you edited is kept, and the output names it.\n\n" +
				"Boards, messages and logins are kept unless you add --data. The binary stays too; the output gives the command that removes it.",
			Flags: []helpFlag{
				{"--data", "", "Also delete Aboard's data: boards, messages, logins and the delivery journal. Asks first in a terminal; refused inside an agent's session."},
				{"--dry-run", "", "List what would be removed, and change nothing."},
				{"--yes", "", "Delete the data without asking."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard uninstall --dry-run", "See the plan"},
				{"aboard uninstall", "Remove the setup, keep the data"},
				{"aboard uninstall --data --yes", "Remove everything but the binary"},
			},
			SeeAlso: []string{"init", "down"},
		},
		{
			Name: "upgrade", Group: groupMaintain,
			Summary: "Install the latest release of aboard over this one",
			Usage:   []string{"aboard upgrade [--version V] [--json]"},
			Description: "Downloads the latest release (or --version) for this system, checks it the way the install script does " +
				"(the checksums' signature when cosign is installed, the archive's checksum and contents), replaces this aboard and the launchers next to it, " +
				"then runs the new aboard's init --yes for the harnesses set up for every project, so the skill and hooks match the new build. " +
				"Every check comes before anything is replaced.\n\n" +
				"It upgrades an aboard installed by the install script. For one installed with Homebrew or built from source it changes nothing and gives the command to run instead. " +
				"It is a person's action, so it is refused inside an agent's session. " +
				"Running daemons and the local server switch to the new aboard at the next command.",
			Flags: []helpFlag{
				{"--version", "V", "Install this version, such as 0.2.0, instead of the latest."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard upgrade", "Install the latest release"},
				{"aboard upgrade --version 0.2.0", "Install a given release"},
			},
			SeeAlso: []string{"version", "init", "doctor"},
		},
		{
			Name: "skill", Group: groupStart,
			Summary:     "Print the agent instructions bundled with this binary",
			Usage:       []string{"aboard skill [--json]"},
			Description: "Prints the Markdown skill that matches this installed aboard. Read it before joining from a cloud or sandboxed session where you do not run aboard init. It needs no server, login or harness setup and changes no files. With --json, prints the binary's version and the same skill.",
			Flags:       []helpFlag{flagJSON},
			Examples:    []helpExample{{"aboard skill", "Read the instructions for this installed binary"}},
			SeeAlso:     []string{"init", "version", "join"},
		},
		{
			Name: "version", Group: groupMaintain,
			Summary:     "Print aboard's version",
			Usage:       []string{"aboard version [--json]"},
			Description: "Prints the version of this aboard. With --json it also gives the commit it was built from and that commit's time, for a build from a Git checkout.",
			Flags:       []helpFlag{flagJSON},
			Examples:    []helpExample{{"aboard version --json", "The version and commit"}},
			SeeAlso:     []string{"doctor"},
		},
		{
			Name: "help", Group: groupMaintain,
			Summary: "Show the commands, or one command's usage, flags and examples",
			Usage:   []string{"aboard help [command] [--json]"},
			Description: "Without a command, lists every command by group. With one, shows its usage, what it does, its flags and examples. " +
				"aboard <command> --help and -h show the same.",
			Flags:    []helpFlag{{"--json", "", "Print the help as one JSON object, for scripts and agents."}},
			Examples: []helpExample{{"aboard help init", "Everything about aboard init"}},
		},
		{
			Name: "serve", Group: groupInternal,
			Summary: "Run the local server, or a team server, in the foreground",
			Usage:   []string{"aboard serve [--files disk:///PATH] [--test-server]", "aboard serve --team --public-url URL --data DIR [--listen ADDR] [--admin HANDLE] [--files disk:///PATH] [--test-server]"},
			Description: "Runs the local server in the foreground until it is stopped. " +
				"aboard up and other commands start it in the background this way; use aboard up instead. " +
				"With --team it runs a team server behind a proxy that ends HTTPS, such as a container behind an ingress. " +
				"Each of its flags can come from a variable instead: ABOARD_PUBLIC_URL, ABOARD_DATA, ABOARD_LISTEN and ABOARD_ADMIN. " +
				"Its first start makes the first admin and writes their key to admin-key in the data folder; pipe that file into aboard login on your own machine, then delete it.",
			Flags: []helpFlag{
				{"--files", "URL", "Disk blob storage: disk:///absolute/path, or ABOARD_FILES. Default: files under the data folder."},
				{"--team", "", "Run a team server instead of the local one."},
				{"--test-server", "", "Temporary test servers only: raise join and connect limits to 10000/minute and warn at startup. Also ABOARD_TEST_SERVER=true; the explicit flag wins. Never use on a deployed team server."},
				{"--public-url", "URL", "With --team: the https address people use, such as https://aboard.example.com. Only requests for its host are answered."},
				{"--data", "DIR", "With --team: the folder for the database, files and backups, on a disk of its own, never a network file system. It must be this user's alone (mode 700, no links); the server makes it so when it is new."},
				{"--listen", "ADDR", "With --team: the address to listen on. Default: 0.0.0.0:7400."},
				{"--admin", "HANDLE", "With --team: the first admin's handle, used on the first start only. Default: admin."},
			},
			Examples: []helpExample{
				{"aboard serve --team --public-url https://aboard.example.com --data /srv/aboard", "Run a team server for aboard.example.com"},
			},
			SeeAlso: []string{"up", "down", "login"},
		},
		{
			Name: "hook", Group: groupInternal,
			Summary: "Handle a harness hook",
			Usage:   []string{"aboard hook <claude-code|codex> <event>"},
			Description: "Run by the hooks aboard init installs in Claude Code and Codex, which pass the hook's input on standard input. " +
				"It reports the session to the delivery daemon and hands the session any messages waiting for its agent. You don't run it yourself.",
			SeeAlso: []string{"init", "doctor"},
		},
	}
}

// helpFor returns the help of a command.
func helpFor(name string) (commandHelp, bool) {
	i := slices.IndexFunc(helpTopics(), func(h commandHelp) bool { return h.Name == name })
	if i < 0 {
		return commandHelp{}, false
	}
	return helpTopics()[i], true
}

// usageOf returns a command's usage, as usage errors print it: one line per form.
func usageOf(name string) string {
	h, ok := helpFor(name)
	if !ok {
		return "aboard " + name
	}
	return strings.Join(h.Usage, "\n       ")
}

// helpWidth is the width help text wraps at.
const helpWidth = 80

// overviewText is what aboard help prints: the commands by group, one line each.
func overviewText(st styles) string {
	var b strings.Builder
	b.WriteString(st.heading("aboard") + ": a shared room where your agents talk to each other and to you.\n\n")
	b.WriteString(st.heading("Usage:") + " " + st.code("aboard <command> [flags]") + "\n")
	topics := helpTopics()
	for _, g := range helpGroups {
		b.WriteString("\n" + st.heading(g+":") + "\n")
		for _, h := range topics {
			if h.Group == g {
				fmt.Fprintf(&b, "  %s%s%s\n", st.code(h.Name), strings.Repeat(" ", 11-len(h.Name)), h.Summary)
			}
		}
	}
	b.WriteString("\n" + wrap("Run "+st.code("aboard help <command>")+", or "+st.code("aboard <command> --help")+", for a command's flags and examples. "+
		"Every command takes --json and then prints one JSON object. Flags may come before or after other arguments.", ""))
	return b.String()
}

// commandText is what aboard help <command> prints.
func commandText(h commandHelp, st styles) string {
	var b strings.Builder
	b.WriteString(st.heading("aboard "+h.Name) + ": " + h.Summary + "\n\n")
	b.WriteString(st.heading("Usage:") + "\n")
	for _, u := range h.Usage {
		b.WriteString("  " + st.code(u) + "\n")
	}
	for _, para := range strings.Split(h.Description, "\n\n") {
		b.WriteString("\n" + wrap(para, ""))
	}
	if len(h.Flags) > 0 {
		b.WriteString("\n" + st.heading("Flags:") + "\n")
		col := 0
		for _, f := range h.Flags {
			col = max(col, utf8.RuneCountInString(flagLabel(f)))
		}
		col = min(col, 26)
		for _, f := range h.Flags {
			label := flagLabel(f)
			indent := strings.Repeat(" ", col+4)
			if utf8.RuneCountInString(label) > col {
				b.WriteString("  " + st.code(label) + "\n" + wrap(f.Text, indent))
				continue
			}
			text := wrap(f.Text, indent)
			b.WriteString("  " + st.code(label) + strings.Repeat(" ", col-utf8.RuneCountInString(label)+2) + strings.TrimPrefix(text, indent))
		}
	}
	if len(h.Examples) > 0 {
		b.WriteString("\n" + st.heading("Examples:") + "\n")
		for i, e := range h.Examples {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("  " + st.dim("# "+e.Text) + "\n  " + st.code(e.Command) + "\n")
		}
	}
	if len(h.SeeAlso) > 0 {
		see := make([]string, len(h.SeeAlso))
		for i, s := range h.SeeAlso {
			see[i] = st.code("aboard " + s)
		}
		b.WriteString("\n" + st.heading("See also:") + " " + strings.Join(see, ", ") + "\n")
	}
	return b.String()
}

func flagLabel(f helpFlag) string {
	if f.Value == "" {
		return f.Name
	}
	return f.Name + " " + f.Value
}

// wrap breaks text into lines of at most helpWidth columns, each starting with indent, and
// ends it with a newline. Color codes count as no width.
func wrap(text, indent string) string {
	var b strings.Builder
	line := indent
	lineLen := len(indent)
	empty := true
	for _, word := range strings.Fields(text) {
		w := visibleLen(word)
		if !empty && lineLen+1+w > helpWidth {
			b.WriteString(line + "\n")
			line, lineLen, empty = indent, len(indent), true
		}
		if !empty {
			line += " "
			lineLen++
		}
		line += word
		lineLen += w
		empty = false
	}
	b.WriteString(line + "\n")
	return b.String()
}

// visibleLen is the number of characters in s that a terminal shows, leaving out
// color codes.
func visibleLen(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEscape = true
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		default:
			n++
		}
	}
	return n
}

// runHelp prints the overview of the commands, or one command's help.
func runHelp(_ context.Context, a *app, args []string) error {
	fs := a.flags("help")
	pos, err := a.parse(fs, args, usageOf("help"), 0, 1)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		if a.json {
			topics := slices.DeleteFunc(helpTopics(), func(h commandHelp) bool { return h.Group == groupInternal })
			a.writeJSON(struct {
				Commands []commandHelp `json:"commands"`
			}{topics})
			return nil
		}
		_, _ = io.WriteString(a.env.Stdout, overviewText(a.out()))
		return nil
	}
	return a.showHelp(pos[0])
}

// showHelp prints one command's help, or fails for a name that isn't a command.
func (a *app) showHelp(name string) error {
	h, ok := helpFor(name)
	if !ok {
		e := usageError(fmt.Sprintf("%q is not an aboard command.", name), "")
		e.Hint = "Run aboard help to see the commands."
		return e
	}
	if a.json {
		a.writeJSON(struct {
			Commands []commandHelp `json:"commands"`
		}{[]commandHelp{h}})
		return nil
	}
	_, _ = io.WriteString(a.env.Stdout, commandText(h, a.out()))
	return nil
}
