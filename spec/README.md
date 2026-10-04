# Contracts

What we want: every piece of Aboard, and every tool built on it, can rely on what the
others promise, and an upgrade on one side never silently breaks the other. Most
failures in a system with many clients and adapters come from two pieces drifting
apart, so every agreement between pieces is written down here.

How Aboard does it: each contract is a hand-written file in this folder. Code is
generated from it or checked against it, it only grows, and a change to it is
reviewed more carefully than any other change.

## The contracts

| Contract | Between | File | Versioned by | Checked by |
| --- | --- | --- | --- | --- |
| HTTP API | The server and every client: CLI, web UI, delivery daemon, SDKs, bots | [openapi.yaml](openapi.yaml) | `info.version`, reported by `GET /v1/info`; the `/v1` path prefix | Go types and handlers generated from it (`make generate-check`); the integration tests check every response, errors included, against it |
| Events | The server, the record, exports and anything reading the stream | [events.md](events.md), and the `Event` schemas in openapi.yaml | The event `type`; types are only added, never renamed or removed | `aboard audit verify`; the `events` tests for canonical hashing |
| Board file | People and the server | [aboard.schema.json](aboard.schema.json) | Its `$id`; fields only added | The board file loader validates against it |
| CLI `--json` output and exit codes | The CLI and the agents and scripts that run it | [cli.yaml](cli.yaml) | Shapes only grow | e2e tests; validating every `--json` output against its schema is to build |
| Delivery, including the `<aboard-message>` wrapper | The delivery daemon and the agents reading delivered messages | [delivery.md](delivery.md), the wrapper's shape in cli.yaml | Shapes only grow | The delivery contract suites (`delivery/deliverytest`), e2e with the fake harness, `make live` |
| Control socket | The delivery daemon, and the hooks, commands and harness extensions on the same machine | [control.md](control.md) | The protocol version on the first message of every connection (`daemon_protocol_mismatch` otherwise) | Every example on the page decodes into the daemon's own types (`TestControlSpecExamplesMatchTheProtocol`); e2e with the fake harness |
| Harness profile | The core and each harness adapter | [harness-profile.schema.json](harness-profile.schema.json) | Fields only added | The harness conformance kit (`make conformance`): every profile validates, every capability it declares has code behind it, and `aboard init` installs exactly its items |
| Launcher protocol | `aboard swarm up`, `ps` and `down`, and every launcher: the built-in tmux and headless, and `aboard-launcher-<name>` commands such as herdr's | [launcher.md](launcher.md) | `v` in every request and response (`launcher_protocol_mismatch` otherwise) | The launcher kit (`server/internal/launcher/launchertest`), run against every shipped launcher by `make test`; `make launcher-kit LAUNCHER=<name>` for any other |

One contract is written when its extension point is built: the monitor hook (HTTP
between the server and a monitor), with a public test kit.

Every error, in every contract, has the shape `{"error":{"code","message","hint"}}`.
Codes are stable: a client may branch on them, so a code is never reused for a
different meaning.

## Changes are additive

A contract only grows: new optional fields, new endpoints, new event types, new error
codes, new commands. A client written against an older contract keeps working against a
newer one, which is what lets teammates run different versions (see the skew policy in
[engineering/release.md](../engineering/release.md#version-skew)).

Not additive, and so not allowed without the deprecation path below: removing or
renaming a field, endpoint, event type or error code; changing a field's type or
meaning; making an optional field required; tightening what a request accepts.

**The deprecation path.** When something must go:

1. Add its replacement first, and mark the old one deprecated in the contract, saying
   what replaces it.
2. Keep both for at least one minor release, longer if delivery daemons or SDKs from
   older builds still read the old one.
3. Remove it in a later release, listed under "Contract changes" in the changelog as
   not additive.

A narrower exception needs a recorded decision showing that nothing reads what is
removed. For example, the CLI's `--json` messages dropped the deprecated `trust`
field once `sender` replaced it, because only delivery daemons read `trust`, and they
read it from the API, which keeps it.

## The contract-change checklist

A commit that touches this folder says, in its message:

- **What changes:** the endpoints, fields, event types, commands or codes.
- **Who is affected:** CLI scripts and agents, API clients and SDKs, delivery daemons,
  harness adapters, people editing board files.
- **Whether it is additive.** If not, which step of the deprecation path it is, and
  the decision that allows it.

And before it merges:

- [ ] The contract changed first, and the code followed it (regenerated where it is
      generated).
- [ ] The checks in the table above pass, and a test covers the new part.
- [ ] Docs that show the changed output show the new output.
- [ ] The change is listed under "Contract changes" for the next release.
