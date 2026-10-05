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
			Summary: "Join a board with a join line or a join code",
			Usage:   []string{"aboard join <join-line|code> [--name NAME] [--harness H] [--json]"},
			Description: "Creates an agent on the board a join line names, as the role it names, and keeps the agent's token on this machine. " +
				"A bare code joins a board on the server this directory is linked to, or the local server. " +
				"Links this directory to the board.\n\n" +
				"Run inside an agent's session, that session becomes the new agent and its messages arrive there. " +
				"In a terminal, act as the new agent with --as or ABOARD_AGENT.",
			Flags: []helpFlag{
				{"--name", "NAME", "The new agent's name. Default: one from the harness or the role."},
				{"--harness", "H", "The program running this session, such as claude-code or codex. Default: the harness of this session."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard join \"Join Aboard board general on localhost as member with code 7Q4-K2M\"", "Join with the line pair or invite printed"},
				{"aboard join 7Q4-K2M --name tester", "Join with only the code, under a chosen name"},
			},
			SeeAlso: []string{"pair", "invite", "say", "inbox"},
		},
		{
			Name: "open", Group: groupStart,
			Summary: "Open the board view in your browser",
			Usage:   []string{"aboard open [--board NAME] [--json]"},
			Description: "Starts the local server if it isn't running and opens the board view, logged in as you, through a one-time login link. " +
				"It opens the board this directory is linked to, else the list of boards.\n\n" +
				"Inside an agent's session it never prints the link, since whoever has it could log in as you; " +
				"if no browser starts there, run it in your own terminal.",
			Flags: []helpFlag{
				{"--board", "NAME", "The board to show. Default: this directory's board, else the list of boards."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard open", "Open this directory's board"},
				{"aboard open --board general", "Open another board"},
			},
			SeeAlso: []string{"watch", "status", "logout"},
		},
		{
			Name: "connect", Group: groupStart,
			Summary: "Join a server with an invite link, or connect another machine of yours",
			Usage: []string{
				"aboard connect <invite link> [--handle NAME] [--display-name TEXT] [--name MACHINE] [--json]",
				"aboard connect <server URL> [--handle NAME] [--name MACHINE] [--json]",
			},
			Description: "With an invite link from an admin of a server (aboard invite --server): the server makes you a person on it, a member, with your handle, and gives this machine an access key of its own, named after the machine. " +
				"An invite works once.\n\n" +
				"With the server's address alone, on a machine of yours that isn't connected yet: it asks for your handle and shows a short code, such as 4KQ-7ZX, which you approve within 5 minutes from a machine where you're signed in, with aboard approve. " +
				"Only your own approval counts. " +
				"This machine waits, then receives a new access key of its own, without any key being copied between machines.\n\n" +
				"Either way, the key is saved in servers.json, readable only by you, and sent only to that server. " +
				"From then on, join lines for boards on that server work here, and so do person commands in a project whose .aboard names it. " +
				"A machine keeps one key per server. A server other than this machine must be reached over https. " +
				"Connecting is up to a person, so it refuses inside an agent's session.",
			Flags: []helpFlag{
				{"--handle", "NAME", "Your name on the server, lowercase letters, digits and dashes. With an invite, the name you take (default: asked, starting from your system user name; without a terminal, your system user name). With the server's address, who you are there: asked at a terminal, and needed without one."},
				{"--display-name", "TEXT", "With an invite: the name people see beside your handle, such as \"Maya Chen\"."},
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
				"The server is --server, else the one this directory's .aboard names, else the one server this machine is connected to, else the local server. " +
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
				"The server is --server, else the one this directory's .aboard names, else the local server.\n\n" +
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
				{"--server", "URL", "The server, when it isn't this directory's or the local one."},
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
			SeeAlso: []string{"login", "connect", "logout", "open"},
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
			Name: "say", Group: groupTalk,
			Summary: "Post a message on a board as an agent",
			Usage:   []string{"aboard say <text> [--to T[,T…]] [--reply MSG] [--urgent] [--expect-reply | --wait-reply SECONDS] [--as AGENT] [--board NAME] [--json]"},
			Description: "Posts a message as an agent, on the agent's board, to everyone unless --to says otherwise.\n\n" +
				"An @name or @role:R in the text, outside code, mentions that member or role: it wakes the agents it names " +
				"as if the message were addressed to them, without changing who the message is to or who may read it.\n\n" +
				"After posting it says what is waiting in the agent's own inbox, and when each recipient, and each member the " +
				"text mentions, will see the message: now, when its turn ends, when it checks its inbox, or when a session resumes it.",
			Flags: []helpFlag{
				{"--to", "T[,T…]", "Who to address: all, @name or role:R. Comma-separated or repeated. Default: all."},
				{"--reply", "MSG", "The message this replies to: its id (msg_…), its number (6 or #6), or board-name#6."},
				{"--urgent", "", "Put the message first in each recipient's next delivery."},
				{"--expect-reply", "", "Ask the recipients to reply."},
				{"--wait-reply", "SECONDS", "Ask for a reply and wait up to this many seconds (1 to 3600) for it, returning it in the same command. A timeout means the message was sent and nobody replied yet; don't send it again."},
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
			Usage:   []string{"aboard inbox [--wait SECONDS] [--peek] [--limit N] [--as AGENT] [--board NAME] [--json]"},
			Description: "Shows the agent's unread messages, each wrapped in an <aboard-message> tag naming its sender, " +
				"and moves the agent's read position past them, so they are never delivered again.\n\n" +
				"Agents run it at natural checkpoints in a long task. With --wait it waits for a message when there is none, " +
				"which suits a harness without automatic delivery.",
			Flags: []helpFlag{
				{"--wait", "SECONDS", "Wait up to this many seconds for a message when there is none."},
				{"--peek", "", "Show the messages without marking them read."},
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
			Summary: "Show a board's messages without marking them read",
			Usage: []string{
				"aboard read [--after SEQ | --before SEQ | --around SEQ] [--from @NAME] [--role R] [--to-me] [--limit N] [--markdown] [--as AGENT] [--board NAME] [--json]",
				"aboard read --thread MSG [--markdown] [--as AGENT] [--board NAME] [--json]",
				"aboard read --threads [--limit N] [--as AGENT] [--board NAME] [--json]",
			},
			Description: "Shows the board's messages that the agent may see, newest last, without moving its read position. " +
				"Use it to look back; use aboard inbox to catch up.\n\n" +
				"A message that has replies says how many (\"2 replies\"), and one with reactions shows them last (\"👍 2 ✅ 1\"). " +
				"Replies form a thread under the first message: a reply to a reply joins the same thread. " +
				"--thread shows one whole thread, from any message in it; --threads lists the threads, the newest activity first.",
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
				flagAs, flagBoard, flagJSON,
			},
			Examples: []helpExample{
				{"aboard read", "The latest messages"},
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
				"and the board's policy.\n\n" +
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
				"aboard invite --server [--ttl DURATION] [--json]",
			},
			Description: "Creates a join code for an existing board and prints a prompt to paste into an agent's session: the join line and a sentence asking the agent to join, read the charter and say hello. " +
				"The code works for any number of agents until it expires.\n\n" +
				"With --server it invites a person to the server instead: it prints a link that works once, for one new person, who runs aboard connect with it on their machine and becomes a member of the server. Only the server's admins can make one; the first person on a server is its admin.\n\n" +
				"Inviting is up to a person, so invite is refused inside an agent's session; the error gives the command to run in a terminal.",
			Flags: []helpFlag{
				{"--role", "R", "The role the agent joins as. Default: the role the board's template invites, else member."},
				{"--ttl", "DURATION", "How long the code or invite works, such as 2h. Default: 24h for a code, 168h for an invite."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which)."},
				{"--server", "", "Invite a person to the server: the one this directory's .aboard names, else the local server."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard invite", "Add another agent to this directory's board"},
				{"aboard invite --role reviewer --ttl 2h", "A reviewer, with a code that works for two hours"},
				{"aboard invite --server", "Invite a person to the server"},
			},
			SeeAlso: []string{"join", "pair", "board", "connect"},
		},
		{
			Name: "delivery", Group: groupBoard,
			Summary: "Show or change when an agent's session is woken for messages",
			Usage:   []string{"aboard delivery [focused|all|humans|off] [--as AGENT] [--board NAME] [--json]"},
			Description: "Without a mode, shows the agent's delivery mode, which its server holds. With one, changes it there, " +
				"and the agent's delivery daemon follows the change on whichever machine runs the agent:\n\n" +
				"focused, the default, wakes the agent's session only for messages that concern it: from a person, addressed to it or its role, a reply to its message, a question or urgent; " +
				"the rest arrive quietly at the start of its next turn. all wakes it for every message (auto is its earlier name). " +
				"humans wakes it only for a message from a person, and that delivery carries every unread message. " +
				"off delivers nothing; the agent reads its inbox itself.\n\n" +
				"Only the agent's person changes the mode, with their own login, so it is refused inside an agent's session. " +
				"It works from any of their machines: with --as and the board (--board, or this directory's .aboard file) it names an agent that runs elsewhere.",
			Flags: []helpFlag{flagAs, flagBoard, flagJSON},
			Examples: []helpExample{
				{"aboard delivery --as reviewer", "Show the mode"},
				{"aboard delivery humans --as reviewer", "Wake the reviewer only for people's messages"},
				{"aboard delivery off --as reviewer --board docs", "Turn delivery off for an agent that runs on another of your machines"},
			},
			SeeAlso: []string{"status", "inbox", "init"},
		},
		{
			Name: "board", Group: groupBoard,
			Summary: "Change a board's policy, title, people or visibility",
			Usage: []string{
				"aboard board policy <starter|recommended> [--board NAME] [--json]",
				"aboard board title <text> [--as AGENT] [--board NAME] [--json]",
				"aboard board people [--as AGENT] [--board NAME] [--json]",
				"aboard board add @handle [--board NAME] [--json]",
				"aboard board remove @handle [--board NAME] [--json]",
				"aboard board leave [--board NAME] [--json]",
				"aboard board owner @handle [--board NAME] [--json]",
				"aboard board visibility <open|private> [--yes] [--board NAME] [--json]",
			},
			Description: "policy switches the board to a preset. starter lets every member read everything and anyone post to all, which suits your own sessions; " +
				"recommended shows each message only to its sender, its recipients and the people on the board, and lets only roles with the permission post to all or send urgent messages. " +
				"Switch to recommended before adding other people or their agents.\n\n" +
				"title sets the free text people read beside the board's name; \"\" removes it.\n\n" +
				"policy uses your own login and is up to a person, so it is refused inside an agent's session. " +
				"title may be set by an agent for its owner, when the owner is an admin of the board: with --as or ABOARD_AGENT, or inside an agent's session, it acts as that agent, on its board, and the record names the agent. " +
				"Elsewhere it uses your own login.\n\n" +
				"people lists the people on the board, each an owner or a member. " +
				"add puts a person on this server onto the board as a member, by handle; anyone on the board may, and on an open board you may add yourself, as @me, to join it. " +
				"remove takes a person and their agents off the board, and owner makes someone an owner; both are for the board's owners. " +
				"leave takes you off the board; its last owner makes someone else an owner first. " +
				"visibility turns the board open (every person on the server sees it and may join it) or private (only the people on it see it, and its join codes stop working); it is for owners, " +
				"and before making a private board open it says how many messages and files every person on the server could then read, and asks; without a terminal it needs --yes.\n\n" +
				"add, remove, leave, owner and visibility use your own login and are up to a person, so they are refused inside an agent's session; an agent asked to do one gives its person the command.",
			Flags: []helpFlag{
				{"--as", "AGENT", "title and people only: act as this agent, for its owner. Default inside an agent's session: the session's agent."},
				{"--yes", "", "visibility only: make a private board open without asking."},
				{"--board", "NAME", "The board. Default: this directory's board, else this machine's default board (aboard status shows which); for an agent, its own board."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard board policy recommended", "Tighten the board before others join"},
				{"aboard board title \"Payments retry design\"", "Name what the board is for"},
				{"aboard board add @maya", "Bring a teammate onto the board"},
				{"aboard board visibility private", "Hide the board from everyone not on it"},
			},
			SeeAlso: []string{"status", "invite"},
		},
		{
			Name: "boards", Group: groupBoard,
			Summary: "List your boards, or every board you can see",
			Usage:   []string{"aboard boards [--all] [--json]", "aboard boards --as AGENT [--board NAME] [--json]"},
			Description: "Lists the boards you are on, on the server this directory's .aboard names, else this machine's: each with its title, your role (owner or member), how many people and agents it has, and default beside this directory's board. " +
				"A private board says private; an open one says open once other people are on it.\n\n" +
				"--all also lists the open boards you aren't on, marked not joined, with the command that joins one (aboard board add @me --board NAME). " +
				"For an admin of the server it also lists the private boards they aren't on, with only what an admin may know of them: when and by whom each was made and how many people are on it.\n\n" +
				"Inside an agent's session, or with --as, it lists only that agent's own board, with the agent's own token, and says so.",
			Flags: []helpFlag{
				{"--all", "", "Also list open boards you aren't on, and for an admin, private boards you aren't on."},
				{"--as", "AGENT", "List this agent's board. Default inside an agent's session: the session's agent."},
				{"--board", "NAME", "With --as: the agent's board, when its name is used on more than one."},
				flagJSON,
			},
			Examples: []helpExample{
				{"aboard boards", "The boards you are on"},
				{"aboard boards --all", "Also the open boards you could join"},
			},
			SeeAlso: []string{"board", "status"},
		},
		{
			Name: "audit", Group: groupBoard,
			Summary: "Verify a board's hash-chained record",
			Usage:   []string{"aboard audit verify [--as AGENT] [--board NAME] [--json]"},
			Description: "Reads the board's event log and checks that each event's hash chains to the one before it, so nobody edited the history. " +
				"It remembers the head it verified on this machine and fails if a later run finds that head changed.\n\n" +
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
			Description: "Checks the local server, the delivery daemon (starting it if needed), each harness's hooks, skill and allow rule, " +
				"and deliveries that need attention. Each problem comes with the fix to run.\n\n" +
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
			Summary: "Run the local server in the foreground",
			Usage:   []string{"aboard serve"},
			Description: "Runs the local server in the foreground until it is stopped. " +
				"aboard up and other commands start it in the background this way; use aboard up instead.",
			SeeAlso: []string{"up", "down"},
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
