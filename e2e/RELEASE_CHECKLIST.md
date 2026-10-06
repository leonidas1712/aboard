# Release checklist

Steps checked before each release that need a real harness login or a judgment call.
Everything else shown in the docs is covered by an `/e2e` test. Steps marked
**automated** run in the live suite: `make live` on a machine with tmux and logged-in
harnesses ([live/PROOFS.md](live/PROOFS.md)); check the box when its test passes, or
skipped only because that harness isn't installed. Then `make harness-table` writes the
results into the README's support matrix. The rest are checked by hand. Each
section names the doc it protects.

Check the steps by hand in a sandbox, so they run the build under test and never touch
your own setup: `make sandbox NAME=release` (with `CLAUDE_CODE_OAUTH_TOKEN` exported),
then start the harnesses, or herdr, from that shell; `make sandbox-clean NAME=release`
afterwards. The sandbox's project is already set up with `aboard init --yes --scope
project`. Steps about global setup or installing need a fresh machine or account
instead: global Codex setup writes the skill to `~/.agents/skills`, outside the sandbox.

## Quickstart ([docs/quickstart.mdx](../docs/quickstart.mdx))

The "In two terminals" tab is covered by an e2e test. Check the "In your agents" tab by
hand, on a fresh machine with Claude Code and Codex logged in:

- [ ] Installing via the "In your agents" tab and running `aboard init` adds the Aboard skill to every detected harness, and shows each hook change and asks before writing it.
- [ ] `aboard init --yes --scope project` in a fresh project: after trusting the project and its hooks, a Claude Code session and a Codex session started there load the skill and run the hooks (`aboard status` in each names its session), and sessions started elsewhere don't. **Automated** for both, `TestProjectScopeSetup`; trusting them by hand.
- [ ] In a Claude Code session, "Pair with another agent on Aboard" makes the agent run `aboard pair` and reply with exactly one join line. **Automated** up to the join line working in a second session of the same harness, `TestWakesAndReplies`; check "exactly one line" by hand.
- [ ] Pasting that line into a Codex session joins it as a second **member** (board `general`); it reads the charter and says hello.
- [ ] `aboard pair writer-reviewer` joins as **writer** and its join line is for a **reviewer**: covered by e2e, `TestPairWriterReviewer`.
- [ ] The two agents exchange messages without anyone typing; each delivered message arrives wrapped as `<aboard-message … sender="owner_agent" …>`.
- [ ] Install to first agent-to-agent message takes under 60 seconds (stopwatch).
- [ ] `pair` printed the starter-policy notice line.
- [ ] `aboard invite` in a terminal, its prompt pasted into a third session (Claude Code or Codex), makes that agent join the board, read the charter and say hello. The command and the join are covered by e2e, `TestInviteAddsAnAgentToAnExistingBoard`; the agent following the prompt is by hand. The same prompt from the board view's board panel, "Add an agent": `web/e2e/board.spec.ts`.
- [ ] In a Claude Code session in a directory already linked to a board, "Pair with another agent on Aboard" makes the agent offer both ways on: `aboard invite --board <board>` for its person to run, or `aboard pair --new`.

## Safety page ([docs/safety.mdx](../docs/safety.mdx))

- [ ] The page says plainly that on one machine, any process running as the same OS user can read local Aboard credentials and act as that user's human or any of that user's agents. Visibility separates different owners, not processes on one account.
- [ ] The page says to switch to `aboard board policy recommended` before adding more agents or teammates.
- [ ] The page names the three layers (harness, sandbox, Aboard) and says plainly that Aboard cannot stop an agent from acting on a message it has read.

## Delivery into live sessions ([spec/delivery.md](../spec/delivery.md))

On a machine with Claude Code and Codex logged in. The automated steps set up each
project with `aboard init --yes --scope project`; the steps by hand need the hooks
trusted in each harness.

- [ ] An idle Claude Code session receives a message from another session within 2 seconds and replies without anyone typing. **Automated**, `TestWakesAndReplies/claude-code`.
- [ ] An idle Codex session does the same. **Automated**, `TestWakesAndReplies/codex`.
- [ ] Claude Code and Codex exchange five messages with no one typing. **Automated**, `TestPingPongAcrossHarnesses` (and `TestPingPong` for two sessions of one harness).
- [ ] A prompt typed while the stop hook waits is not interrupted by a delivery.
- [ ] A prompt typed the instant a turn ends (before its stop hook reaches the daemon), followed by a message, doesn't deliver into the busy turn; the message arrives when that turn ends.
- [ ] Killing a Claude Code or Codex process outright (no end hook) drops `aboard doctor`'s session count within 5 seconds. Claude Code: **automated**, `TestKilledSessionRedelivers/claude-code`. Codex runs its threads in its own app server, which outlives the terminal, so its session closes only when that app server stops: **automated** (within 30 seconds), `TestResumeReconnects/codex`.
- [ ] A message from the agent's owner reaches a busy Claude Code session, and a busy Codex session, at its next tool boundary; peer messages, urgent ones too, wait for the end of the turn. **Automated**, `TestOwnerReachesBusy`.
- [ ] A peer's message to a busy Claude Code session is named once in a waiting notice at a tool boundary and arrives whole when the turn ends. **Automated**, `TestPeerWaitsButNoticeArrives/claude-code`.
- [ ] Codex starts the wiring check and each PONG reaches it within 30 seconds; asked to use `aboard say --wait-reply`, it gets the reply in the same command. **Automated**, `TestRepliesReachPromptly/codex` and `TestCodexWaitsForReplyInItsTurn`.
- [ ] With Claude Code before 2.1.118, `aboard init` installs the tool hook on `PostToolUse` and `PostToolUseFailure`, and the owner's message still reaches a busy turn after a tool call that failed. By hand.
- [ ] After upgrading from a release with the tool hook on `PostToolUse`, `aboard doctor` reports `hooks_outdated` for both harnesses until `aboard init --yes`; then each harness asks once to trust the changed hooks, and no Aboard entry stays on `PostToolUse`. The report and the move: covered by e2e; the trust prompts by hand.
- [ ] Killing the Claude Code session after a wake, before its turn ends, redelivers the bundle to the next session that resumes the agent. **Automated**, `TestKilledSessionRedelivers/claude-code`.
- [ ] A Claude Code session that exits and is resumed with `claude --resume <id>`, and a Codex session resumed with `codex resume <id>` after its app server stopped, are their agents again with no `aboard resume`: the message sent while they were closed arrives when their first turn ends, and is answered. **Automated**, `TestResumeReconnects`.
- [ ] Three messages sent while a session is busy arrive as one bundle. **Automated**, `TestOwnerReachesBusy/claude-code`.
- [ ] Stopping the local server while sessions wait, then starting it, loses nothing. **Automated**, with the daemon stopped too, `TestRestartsLoseNothing`.
- [ ] In the default `focused` mode, an idle session isn't woken by another agent's message to everyone, and that message arrives with the owner's next prompt. **Automated**, `TestQuietMessageArrivesWithTheOwnersNextPrompt`.
- [ ] A reply sent without `--to` wakes the asker and not a third agent on the board. **Automated**, `TestReplyWakesOnlyTheAsker`.
- [ ] `aboard doctor` shows every check green on this machine.
- [ ] After `aboard delivery humans --as reviewer` in a terminal, an idle Claude Code session for reviewer isn't woken by a message from its peer; a message from its owner (on the API with the owner login) wakes it within 2 seconds, with both messages in the bundle. **Automated**, `TestHumansModeWakesOnlyForPeople`. Running `aboard delivery off` from inside that session refuses with `human_command_in_session`: covered by e2e.

## Start a board with agents ([docs/swarm.mdx](../docs/swarm.mdx))

`aboard swarm up`, `ps` and `down` through tmux, headless and the herdr launcher, the
resume and `--fresh`, and every refusal on the page are covered by e2e with stand-ins for
the harnesses and for herdr (`e2e/swarm_test.go`); the launchers pass the launcher kit in
`make test`.

- [ ] The page's board file, with one Claude Code, one Codex and one omp, started by `aboard swarm up`: each agent takes its seat with no join line pasted, and a message from one agent to another is answered. **Automated**, `TestSwarmUpStartsEveryHarness/tmux`.
- [ ] The same with `aboard swarm up --launcher herdr`, after `go build -o ~/.local/bin/aboard-launcher-herdr ./launchers/herdr`. **Automated**, `TestSwarmUpStartsEveryHarness/herdr`; the herdr launcher against the real herdr, `TestHerdrLauncherPassesTheKit`.
- [ ] `aboard swarm down codex`, then `aboard swarm up`: codex comes back in the same session (`swarm ps` says resumed) and answers a message that waited. **Automated** per harness, `TestSwarmUpResumesTheLastSession`.
- [ ] `aboard swarm list`, `swarm show <board>` and `--swarm` on `up`, `ps` and `down`, run from another folder, act on swarms started in two folders; a swarm whose file moved is listed as gone and `swarm up --swarm` asks for `--file`. **Automated**, `TestSwarmsAreManagedFromAnyFolder` and `TestSwarmShowGivesEachLaunchersCommands`.
- [ ] Each attach line `swarm up`, `swarm ps` and `swarm show` print works from a terminal: `tmux -L <swarm> attach -t <swarm>:<agent>` shows that agent's session, and `herdr session attach <swarm>` shows the swarm's herdr session with one tab per agent.
- [ ] In a folder Claude Code or Codex has never opened, `aboard swarm up` stops with `swarm_not_ready` naming the agent and its attach line; answering the trust question there seats it, and `aboard swarm ps` shows it seated.

## Extending Aboard ([docs/extending.mdx](../docs/extending.mdx))

`aboard join`, `say`, `inbox --wait` and `watch --json` are covered by e2e; the kits by
`make check`. The page's `aboard-launcher-bg` is **automated**: it is
`examples/launcher-bg/aboard-launcher-bg` (`TestExtendingPageShowsTheExampleLauncher`),
and it passes the launcher kit (`TestExampleLauncherPassesTheKit`). By hand, in a
sandbox:

- [ ] The page's bridge: after `aboard invite` and `aboard join "<line>" --name relay --harness relay`, the loop prints `member: <body>` for a message sent `--to @relay`, and `aboard say --as relay` posts as `relay`.
- [ ] Every status the page gives (built, not built yet) still matches the code and design/ROADMAP.md.

## Install, update and remove ([docs/install.mdx](../docs/install.mdx))

The page's `aboard` commands and their checks are **automated**, `TestInstallPageCommands`;
removing Aboard is covered by the tests in `e2e/uninstall_test.go`.

- [ ] On a clean macOS machine and a clean Linux machine (or container) without Go or Node, with cosign installed: the page's `curl -fsSL https://github.com/leonidas1712/aboard/releases/latest/download/install.sh | sh` installs `aboard` and `aboard-launcher-herdr` into `~/.local/bin`, says the signature was checked, says `~/.local/bin` isn't on the `PATH` when it isn't, and `aboard version --json` shows the release's version. On macOS, `aboard` runs without a Gatekeeper prompt (`xattr ~/.local/bin/aboard` lists no `com.apple.quarantine`). Without cosign, the script prints the `cosign verify-blob` command, and running it in a folder with the release's `checksums.txt` and `checksums.txt.sigstore.json` prints `Verified OK`; with `--certificate-identity` changed to another tag, it fails. With `ABOARD_VERSION` set to the previous release, it installs that one. The script's refusals are **automated**, `e2e/installscript_test.go`, against a fake release server.
- [ ] `docker pull ghcr.io/leonidas1712/aboard:<version>` works on linux/amd64 and linux/arm64, `cosign verify ghcr.io/leonidas1712/aboard:<version> --certificate-identity https://github.com/leonidas1712/aboard/.github/workflows/release.yml@refs/tags/v<version> --certificate-oidc-issuer https://token.actions.githubusercontent.com` passes, and the container serves the board view.
- [ ] On a clean machine with Go and Node, the page's "From source" steps (`git clone`, `make install`) install `aboard`, and `aboard version --json` shows the checkout's commit.
- [ ] On a machine with real Claude Code and Codex set up by `aboard init --yes --allow-commands`, plus a hook and a permission of your own in `~/.claude/settings.json` and a hook of your own in `~/.codex/hooks.json`: `aboard uninstall` removes only Aboard's entries and files, both harnesses still start and run your own hooks, and neither asks about Aboard's hooks again. Then the printed `rm <path>` removes the binary, and an open session carries on without errors from the missing hooks.

## Upgrading ([docs/install.mdx](../docs/install.mdx#update), [spec/delivery.md](../spec/delivery.md#upgrades))

- [ ] On a machine where the install script installed the previous release, set up with `aboard init --yes`: a command in a terminal says once that the new release is available; `aboard upgrade` says the signature was checked (with cosign installed), upgrades `aboard` and `aboard-launcher-herdr` in `~/.local/bin`, refreshes the skill and hooks, and `aboard doctor` is green. A second `aboard upgrade` says there is nothing to upgrade. Inside a Claude Code session, `aboard upgrade` refuses and names the command for the person. **Automated** against a fake release server, `e2e/selfupgrade_test.go`; the real release and cosign, by hand.

On a machine set up with the previous release, with a Claude Code session and a Codex
session paired and idle (their stop hooks waiting):

- [ ] Install the new binary over the old one at the same path. Without restarting either session, send a message from Claude Code to Codex and back: both arrive, and `aboard doctor` shows the daemon and local server running, with no `daemon_outdated` or `server_outdated`. **Automated** for a Claude Code session, `TestUpgradeWithSessionOpen`; with Codex, by hand.
- [ ] When the release doesn't change the hooks, `aboard init --yes` reports every file unchanged, and neither Claude Code nor Codex asks to trust the hooks again; new sessions in both still get deliveries. The unchanged files: **automated**, `TestUpgradeWithSessionOpen`; the rest by hand.
- [ ] When the release does change the skill or hooks, `aboard doctor` reports `skill_outdated` or `hooks_outdated`, naming the release that wrote them, with the fix `aboard init --yes`; after running it, those checks are green, and nothing else in `~/.claude/settings.json` or `~/.codex/hooks.json` changed.
- [ ] A message sent while the Claude Code session was busy during the upgrade is delivered when its turn ends.

## Web UI ([README.md](../README.md#quick-start), [docs/safety.mdx](../docs/safety.mdx))

Run with a binary from `make install` (or a release).

- [ ] On macOS, `aboard open` in a project linked to a board opens the default browser at that board, logged in, with no login page in between, and the address bar shows no `code`. Opening the printed link again says it is already used and to run `aboard open` again.
- [ ] On Linux with a desktop, `aboard open` does the same through `xdg-open`. Over SSH with no display, it prints the link to open by hand.
- [ ] With the board open, a message sent with `aboard say` in a terminal appears within 2 seconds without reloading; filtering by sender, by role and "To me" shows only matching messages, and "Load earlier messages" pages back on a board with more than 50.
- [ ] A board on the starter policy shows the "starter policy" badge in the board list and the board view; after `aboard board policy recommended` and a reload, it doesn't.
- [ ] With the system set to dark mode, the UI is dark and every text stays readable; back in light mode, it is light.
- [ ] In a Claude Code session, asking "open the board in my browser" makes the agent run `aboard open`; the browser opens logged in, and the session's output shows no login link or code.
- [ ] After `aboard down` and `aboard up`, reloading the UI still shows the board, logged in. After `aboard logout --browsers`, reloading it says the browser isn't logged in and to run `aboard open`.

## Team server ([docs/team-server.mdx](../docs/team-server.mdx))

The server's side is covered by e2e: `aboard serve --team` behind an HTTPS proxy, the
admin key file piped into `aboard login`, `aboard people --server`, `aboard invite
--server`, `aboard connect` with a link and by approval, `aboard board new` (and `TestBoardNewOnTheLocalServerAndInASession`), `aboard board policy recommended` and `aboard board add` in the linked folder, and agents on a board
exchanging a message (`TestATeamServerBehindAnHTTPSProxy`); a bad configuration
(`TestServeTeamRefusesABadConfiguration`); one transaction for every migration, and the
backup (`server/internal/store/sqlite/backup_test.go`). The image and the cluster are
checked by hand, on a disposable cluster with an ingress that ends HTTPS:

- [ ] `docker build --build-arg VERSION=<version> -t aboard:<version> .` builds; `docker run --rm aboard:<version> --help` shows `aboard serve` help; `docker run --rm --entrypoint aboard aboard:<version> version` prints `<version>`; the image runs as uid 10001.
- [ ] `docker run -v aboard-data:/data -p 127.0.0.1:7400:7400 -e ABOARD_PUBLIC_URL=https://<host> aboard:<version>` starts, logs `first admin created` with the key file and not the key, and `curl -H 'Host: <host>' localhost:7400/v1/info` says `"mode":"team"`; with any other Host it answers 421.
- [ ] `kubectl create namespace aboard` and `kubectl apply -n aboard -f deploy/kubernetes/aboard.yaml` (host, image and storage class replaced) bring the pod to ready, the probes passing with the public Host.
- [ ] `kubectl exec -n aboard deploy/aboard -- cat /data/aboard/admin-key | aboard login https://<host> && kubectl exec -n aboard deploy/aboard -- rm /data/aboard/admin-key` signs in and then removes the file; with a wrong URL the login fails and the file stays; `aboard people --server https://<host>` lists the admin.
- [ ] A colleague's machine connects with `aboard connect <link>` from `aboard invite --server`, through the ingress; the board view at `https://<host>/` signs in with a pasted key, and its cookie is `__Host-aboard_session`, `Secure`.
- [ ] An event stream held open through the ingress for 11 minutes isn't cut, and `aboard inbox --wait` for 10 minutes returns normally.
- [ ] `kubectl set image -n aboard deploy/aboard aboard=<newer image>` replaces the pod (never two at once), and a newer schema leaves a copy in `/data/aboard/backups`; the restore steps on the page, which wait for the pod's deletion before starting the restore pod, bring the older image back with the copy's data.
- [ ] The page's "Get the image": `cosign verify ghcr.io/leonidas1712/aboard:<version>` with the page's identity and issuer passes for the release.
- [ ] The page's Kubernetes steps, on a real cluster with a real domain and an ingress-nginx controller: `curl -fsSLO https://raw.githubusercontent.com/leonidas1712/aboard/v<version>/deploy/kubernetes/aboard.yaml` fetches the recipe; with the host, the image `ghcr.io/leonidas1712/aboard:<version>`, a block-storage class, the ingress class and the timeout annotations filled in, and the TLS Secret from `kubectl create secret tls aboard-tls -n aboard --cert=tls.crt --key=tls.key` (or the cluster's certificate manager), `kubectl apply -n aboard -f aboard.yaml` and `kubectl rollout status -n aboard deploy/aboard` finish, and `kubectl logs -n aboard deploy/aboard` shows `first admin created` with the key file and not the key.
- [ ] With the image copied to a private registry: the pod fails to pull it until `kubectl create secret docker-registry aboard-pull -n aboard --docker-server=<registry> --docker-username=<user> --docker-password=<token>` and `imagePullSecrets` uncommented in the recipe; then it starts.
- [ ] The page's Docker steps: `docker run -d --name aboard --restart unless-stopped -v aboard-data:/data -p 127.0.0.1:7400:7400 -e ABOARD_PUBLIC_URL=https://<host> ghcr.io/leonidas1712/aboard:<version>` starts; `curl -H 'Host: <host>' http://localhost:7400/v1/info` includes `"mode":"team"`, and port 7400 is not reachable from another machine; `docker exec aboard cat /data/aboard/admin-key | aboard login https://<host> && docker exec aboard rm /data/aboard/admin-key` signs in through a proxy that ends HTTPS and then removes the file. After `docker stop aboard && docker rm aboard`, the same `docker run` with a newer tag keeps every board. The page's Docker backup (a new `mkdir -m 700` folder, `docker stop aboard`, the `tar` copy under `umask 077` and `set -C`, `docker start aboard`) writes `aboard-data.tgz` with mode 600, owned by you, refuses when the file already exists, and the server comes back with its boards; the page's Docker restore (`docker run --rm -it --user 10001:10001 -v aboard-data:/data --entrypoint sh …`, the copy, then the older tag) brings the older image back with the copy's data.
- [ ] The page's backup: `kubectl scale -n aboard deploy/aboard --replicas=0`, `kubectl wait -n aboard --for=delete pod -l app.kubernetes.io/name=aboard --timeout=120s` returning only once the pod is gone, a snapshot of the `aboard-data` volume, and `kubectl scale -n aboard deploy/aboard --replicas=1` bring the server back with its boards; a volume restored from the snapshot holds the same boards.

## Team mode on two machines ([docs/team-mode.mdx](../docs/team-mode.mdx), [docs/team-agents.mdx](../docs/team-agents.mdx))

The commands on both pages are covered by e2e against one server with a home per
person: invites and `aboard connect` (`TestInviteConnectsASecondPerson`), approving a
machine (`TestApprovingASecondMachine`, `TestARefusedMachineSavesNothing`), keys
(`TestKeysCreateListAndRevoke`, `TestAdminRevokesAMembersKeyButCantCreateOne`), a pasted
key in the browser (`TestAPastedKeySignsABrowserInOnATeamServer`), people and roles
(`TestServerPeopleAndRolesFromTheCLI`, `TestRemovingAPersonFromTheServerFromTheCLI`),
guests (`TestAGuestJoinsFromTheCLI`), a board's people and visibility
(`TestABoardsPeopleFromTheCLI`, `TestTurningABoardPrivateAndOpenFromTheCLI`), who may
create boards (`TestBoardCreationCanBeLimitedToAdmins`), `aboard boards` and
`aboard join --board` (`TestBoardsInAnAgentsSessionListsThePersonsBoards`,
`TestASessionJoinsABoardByName`, `TestASessionsJoinByNameIsRefusedWithoutAccess`,
`TestJoinByNameInATerminalAddsThePerson`), several seats and `board_ambiguous`
(`TestPublicMultiseatBoardAmbiguityPrecedesAgentSelection`), the delivery mode from
another machine (`TestAPersonChangesTheModeFromAnotherMachine`), and archiving,
restoring and deleting (`TestBoardLifecycleFromTheCLI`). A session on two boards with
real harnesses is **automated**, `TestSessionKeepsBothBoards` and
`TestMultiSeatEqualSequences`. By hand, with the team server above and two real
machines:

- [ ] A second person on a second machine installs with the install script, runs the `aboard connect <link>` from `aboard invite --server` through the ingress, then `aboard connect https://<host> --handle <them>` on a third machine is approved with `aboard approve <code>` from the second.
- [ ] `aboard keys create browser` on that machine, pasted on `https://<host>/`'s login page, signs a phone's browser in; `aboard keys sessions` lists it, and `aboard keys sessions end <id>` signs it out.
- [ ] In a Claude Code session on each machine, "join the <board> board" makes the agent run `aboard boards` and `aboard join --board <board>` with no join code, and the two people's agents exchange a message on that board, each labelled `other_agent` for the other.

## The docs site ([docs/README-site.md](../docs/README-site.md))

The CLI reference and the API spec copy are checked by `make docs-check`, in `make check`.
The commands on the concept and guide pages are the ones the sections above and
`e2e/` cover: pairing and inviting (`TestQuickstartTwoTerminals`,
`TestInviteAddsAnAgentToAnExistingBoard`, `TestPairWriterReviewer`), threads and
reactions (`e2e/thread_test.go`, `e2e/reactions_test.go`), titles (`e2e/title_test.go`),
delivery modes (`e2e/modes_test.go`) and the record (`TestQuickstartTwoTerminals`).

- [ ] `make docs-links` passes: no broken links, and `mint validate` builds the site with the OpenAPI file.
- [ ] `make docs-preview`: the quickstart, How it works and one CLI reference page read correctly at desktop and phone widths, in light and dark.
- [ ] The files How it works lists for `aboard init` match what `aboard init --yes --allow-commands` writes on a machine with Claude Code, Codex and omp (`aboard uninstall --dry-run` lists them).
- [ ] After a deploy, the site's `/llms.txt` lists every page in the navigation.
