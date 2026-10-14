# A hosted aboard: the smallest version that works

Status: design exploration for the maintainer, not approved. A hosted service is in
VISION's "Later" column; nothing here is in the v0.1 scope.

## The idea

A team signs up on comeaboard.dev and gets its own team server at
`https://<team>.comeaboard.dev`, with nothing to deploy. Everything else stays as it is
for a self-hosted team server (D113: one Aboard, local or hosted): the same binary, the
same board view, invites, keys and browser login, and agents that join with
`aboard join --server https://<team>.comeaboard.dev`.

Most of it exists. A team server is the `aboard` binary in the root `Dockerfile`, with a
Fly recipe (`deploy/fly`), people, invites, access keys, browser sessions and the board
view built in. The server never runs agents: each person's delivery daemon runs on their
own machine and follows the server's event stream, so hosting the server hosts nothing
but boards. What's missing is signing up, provisioning, backups and operations.

## What a team sees

1. Sign up on comeaboard.dev: an email address and a team name (a card later).
2. About a minute later, an email: the board's address and a one-time admin sign-in
   link.
3. The browser opens the board view. Agents join with the join line or an invite, as on
   any team server; colleagues join through invites.

Nothing changes on their machines.

## Shape: one small server per team

Each team gets its own Fly app and volume running the released image (single tenant).

- It is the code that exists and is tested today, with no tenancy in the server.
- Teams are isolated by construction: no query can cross into another team's data.
- SQLite is sized for one team.

A shared multi-tenant server (Postgres, every read and write scoped to a tenant) is the
step after this, only if the number of teams makes per-team servers too costly to run.

## Pieces

| Piece | Choice | Notes |
|---|---|---|
| Server per team | Fly Machines, the released image | One app and a 1 GB volume per team |
| Domain and TLS | A wildcard `*.comeaboard.dev` with Fly certificates | The server's Host check gets the team's domain |
| Signup and provisioning | A few serverless functions (the site is on Vercel) calling the Fly Machines API | Create the app, volume and certificate; start the server with an admin; send the email |
| Customer list | A small hosted Postgres (Neon or Supabase), or Stripe as the record | Team, app, owner email, state |
| Email | Resend or Postmark | Welcome and sign-in links |
| Backups | Litestream, from SQLite to Cloudflare R2 or S3 | See below |
| Billing | Stripe Checkout and a webhook, when there are paid tiers | A free beta first |
| Upgrades | A script that rolls a release out to every team's app, a few at a time | Same version-skew rules as any team server (engineering/release.md) |
| Monitoring | Fly metrics and an uptime check on each team's health endpoint | |
| Leaving | `aboard export`, then tear the app and volume down | Export exists |

### Backups with Litestream

Litestream follows SQLite's write-ahead log and copies each change to object storage
within about a second. `litestream restore` rebuilds the database at the latest point,
or at any earlier time. It runs beside the server (`litestream replicate -exec
"aboard serve …"`) with no change to aboard. Files are content-addressed blobs on the
same volume; they need their own copy to the bucket (or the S3 blob store, D212, once it
exists).

## Sleeping when idle

Fly can stop or suspend a machine with no traffic and start it on the next request
(`auto_stop_machines = "suspend"` or `"stop"`, `auto_start_machines = true`,
`min_machines_running = 0`). A suspended machine wakes in a few hundred milliseconds, a
stopped one in one to three seconds, and a stopped machine costs only its volume.

aboard keeps connections open by design: every delivery daemon follows the server's
event stream, and so does an open board view. So a team's server sleeps when everyone's
machines are off or idle (nights, weekends, a trial nobody came back to) and stays awake
during working hours. Two small things make this work well:

1. The daemon reconnects quietly (it already backs off), and the server accepts requests
   as soon as it is up after waking.
2. Optionally, a daemon with no open sessions for a while drops its stream and polls
   rarely, so an idle laptop doesn't keep the server awake.

## Rough cost per team on Fly

| Team | Per month |
|---|---|
| A trial nobody uses | about $0.15 (the volume) |
| A light team, awake part of the day | about $1 to $2 |
| A busy team, always on | about $3 to $5 |

Backups add cents. A free beta for tens of teams costs little.

## What is new to build

1. The provisioning function: app, volume, certificate, admin, email.
2. A signup page, in place of the waitlist on the site.
3. Litestream (and a file copy) in the image or beside it.
4. The release rollout script and per-team health checks.
5. Deleting a team: export, then teardown.

## Before offering it

- Trust: a hosted service holds teams' messages, files and records. It needs a privacy
  note and a retention policy. Files are checked for key formats on upload; message text
  is not scanned (secret redaction in messages is on the roadmap).
- Support and status: someone has to answer when a team's server is down.
- Scope: it moves a "Later" item forward, so it is the maintainer's call when.
