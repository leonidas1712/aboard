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

Organise by what a package does (`store`, `events`, `rules`, `redact`, `delivery`,
`api`, `cli`), not by layer (`models`, `services`, `helpers`).

- Define an interface in the package that **uses** it, with only the methods it calls.
- Constructors take every dependency as an argument. No package-level mutable state,
  no `init()` side effects, no globals for config, clock or logger.

```go
// Do: the consumer declares what it needs.
package delivery

type inbox interface {
	Unread(ctx context.Context, agent string, limit int) ([]Message, error)
	Ack(ctx context.Context, agent string, upTo int64) error
}

func NewWorker(in inbox, adapters map[string]Adapter, clk clock.Clock, log *slog.Logger) *Worker
```

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

## Logging

Use `log/slog`, passed in, never the global logger. Log with keys, not formatted
strings: `log.Info("message posted", "board", b, "seq", seq)`.

Never log tokens, join codes, credentials, message bodies, note text or file contents.
Log ids and sizes instead.

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
5. `go test -race ./...`
6. the e2e tests in `/e2e` (`go test -race -tags e2e ./e2e/...`)
7. `govulncheck ./...`

Tools are pinned in the Makefile and installed into `.bin/` on first use; nothing global
is needed beyond Go.

A change isn't done until `make check` passes locally. Don't add lint exclusions to get
there; fix the code. If an exclusion is truly needed, add it to `.golangci.yml` with a
comment saying why.
