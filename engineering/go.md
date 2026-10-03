# How we write Go

One Go module at the repo root. The binary is `server/cmd/aboard`, its packages are under
`server/internal/`, and end-to-end tests live in `/e2e` behind the `e2e` build tag, so
`go test ./...` stays fast and `make e2e` runs them.

## Dependencies

Standard library first: `net/http`, `log/slog`, `database/sql`, `encoding/json`,
`crypto/*`. Add a module only when it saves real work, and say why in the PR. No web
frameworks or routers: `http.ServeMux` handles method and path patterns.

Acceptable because they replace real work: the SQLite driver, `oapi-codegen`,
`golang.org/x/sync/errgroup`. Not acceptable: an assertion library, a logging library,
a DI container, a "utils" module.

## Packages

Shape follows [architecture.md](architecture.md): the domain in `board`, `rules` and
`events`; adapters around it; dependencies pointing inward. In Go terms:

- Organise by what a package does (`board`, `sqlite`, `delivery`), not by layer
  (`models`, `services`, `helpers`).
- Define an interface in the package that **uses** it, with only the methods it calls.
- Constructors take every dependency as an argument. No package-level mutable state,
  no `init()` side effects, no globals for config, clock or logger.

```go
// Don't: a global the tests can't replace.
var db *sql.DB
```

## Errors

- Wrap with `%w` and add what was being done: `fmt.Errorf("append event to board %s: %w", id, err)`.
- Compare with `errors.Is` / `errors.As`, never `==`.
- No `panic` on a request path or in a background job. Panics are for impossible
  states at startup.
- Errors that reach an API client are created as the error type that carries a stable
  code, message and hint. Exactly one function, in `api`, turns an error into an HTTP
  response; anything without a code becomes `500 internal` and is logged.

```go
// Do
return apierr.New("broadcast_not_allowed",
	"You don't have permission to message everyone on this board.",
	"Use --to @name or --to role:R.")

// Don't: the handler picks a status and a string on its own.
http.Error(w, "forbidden", 403)
```

## Context, goroutines, shutdown

- Every request, store call and background job takes a `context.Context` as its first
  argument. Outbound calls (harness processes, monitors, HTTP) get a timeout.
- Every goroutine has an owner that waits for it. Start groups with `errgroup`; the
  owner cancels the context on shutdown and waits.
- No `go func()` without a way to stop it and a place that waits for it.

```go
g, ctx := errgroup.WithContext(ctx)
g.Go(func() error { return srv.Serve(ctx) })
g.Go(func() error { return daemon.Run(ctx) })
return g.Wait()
```

## Extensions run in their own processes

A broken or hostile extension must never stop a board or read its database. Launchers,
monitors, CLI extensions and harness hooks run as separate processes and talk to Aboard
over a small protocol (the public API, JSON on standard input and output, or the
monitor hook); nothing is loaded into the server or the daemon.

- Every call out to an extension or a harness has a timeout, taken from the context.
- Failure degrades, never stops. A monitor that times out or crashes means "no check":
  the message is posted, the failure is logged, and the board view and `aboard doctor`
  flag the monitor as down. A launcher that fails reports the session it couldn't
  start; the board carries on.
- Hook commands stay thin: they call `aboard` and hold no logic of their own, so a
  change to the core needs no change in each harness.

## Logging and request ids

Use `log/slog`, passed in, never the global logger. Log with keys, not formatted
strings: `log.Info("message posted", "board", b, "seq", seq)`.

Never log tokens, join codes, credentials, message bodies, note text or file contents.
Log ids and sizes instead.

One request id follows a command through every process it touches, so an agent
debugging "my message never arrived" can follow it from the log alone:

- The CLI makes one id per command (`req_` and a ULID) and sends it as `X-Request-Id`
  on every request that command makes.
- The server takes a well-formed id from the header or makes one, puts it on the
  request's logger, returns it in the `X-Request-Id` response header and in error
  bodies, and logs it with the board and sequence number of any event the request
  appended.
- The delivery daemon logs each delivery with the board and sequence numbers it
  delivered and the ids of its own requests, so a message's sequence number joins the
  write that made it to the session that received it.
- The CLI's error output and `--json` errors include the request id.

(Request ids are not built yet.)

## Debugging

Every failure leaves a trail an agent can read and act on:

- **Errors carry a stable code, a message and a hint** naming the next step (see
  Errors above). Agents branch on the code, never on the message.
- **`aboard doctor --json`** checks the install, the servers, versions, harness files,
  delivery and presence, each with a level, a code and a fix.
- **Logs are in documented places**: the local server's log in Aboard's data folder,
  the daemon's in its state folder.
- **`aboard debug bundle`** writes one archive with the logs, versions, `doctor`
  output and config, with tokens and credentials removed and message bodies left out,
  for bug reports from people and agents. (Not built yet.)

## Time and randomness

Anything whose behaviour depends on the time (expiry, rate limits, long-poll waits) takes
a `clock.Clock`. Anything that generates ids, tokens or join codes takes an `io.Reader`
for randomness (`crypto/rand.Reader` in production). Tests pass a fake clock and a seeded
reader, so results are exact.

```go
// Do
func (c *JoinCodes) Expired(code JoinCode) bool { return !c.clock.Now().Before(code.ExpiresAt) }

// Don't
func (c *JoinCodes) Expired(code JoinCode) bool { return time.Now().After(code.ExpiresAt) }
```

## SQL

Use `database/sql` with `QueryContext` / `ExecContext`. Close rows, check `rows.Err()`.
A write and its event append share one transaction. Migrations are numbered SQL files
embedded in the binary.

## Generated code

Types and handler interfaces are generated from `spec/openapi.yaml` with `oapi-codegen`
through `go generate`. Generated files are committed and never edited by hand. CI
regenerates and fails if anything changes. To change the API, edit the spec, regenerate,
then implement.

## `make check`

One command runs everything CI runs, in this order:

1. formatting (`golangci-lint fmt --diff`: gofumpt, goimports)
2. `golangci-lint run` (config in `/.golangci.yml`)
3. `go vet ./...`
4. generated code is up to date
5. the core is within its size budget, and the README's harness table matches the
   profiles and the live kit's results
6. `go test -race ./...`
7. the e2e tests in `/e2e` (`go test -race -tags e2e ./e2e/...`), the harness
   conformance kit among them
8. the tests of the code Aboard installs inside harnesses (`make extension-test`: omp's
   extension, with Bun)
9. `govulncheck ./...`

Tools are pinned in the Makefile and installed into `.bin/` on first use; nothing global
is needed beyond Go and Bun (omp's extension is TypeScript that omp's Bun runs, so its
tests run with Bun; `make` says how to install it when it's missing). CI (`.github/workflows/check.yml`) runs `make check` on Linux and
macOS, because some code, such as the socket peer check, differs per system.

A change isn't done until `make check` passes locally. Don't add lint exclusions to get
there; fix the code. If an exclusion is truly needed, add it to `.golangci.yml` with a
comment saying why.
