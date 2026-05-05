# AGENTS.md

Instructions for AI coding agents (Claude Code, GitHub Copilot, Codex, Cursor, Aider, Devin) working **on** this repository.

If you are an agent looking to **use** Hush from another project, install [`skills/hush/SKILL.md`](./skills/hush/SKILL.md) into your runtime's skill directory and follow that file instead.

## What this repo is

Hush is a credential broker. Single Go binary (`cmd/hush`) plus a Python SDK (`sdk/python`) that mirrors a subset of the Go client. The broker stores secrets in an AES-256-GCM vault, resolves named hosts to a transport + auth pair, and dispatches commands. Audit log is append-only and hash-chained.

```
internal/
  apikey/        scoped API tokens
  audit/         hash-chained log
  broker/        single + parallel exec
  clientlib/     thin HTTP client used by CLI
  config/        env-first config loader
  inventory/     named-host resolver
  mcpserver/     MCP wrapper around CLI
  server/        chi-based HTTP API
  store/         SQLite layer
  transport/     ssh / sdjz-relay backends
  vault/         envelope encryption (DEK / KEK)
cmd/hush/        cobra commands
sdk/python/      Python client + paramiko_compat shim
skills/hush/     SKILL.md the broker ships with
```

## Commands you will run often

```bash
go build ./...                                # build the binary
go test ./...                                 # full test suite
go vet ./...                                  # static analysis
gofmt -l .                                    # formatting check (must be empty)

# end-to-end smoke
HUSH_MASTER_KEY=$(openssl rand -base64 32) \
  go run ./cmd/hush bootstrap                 # mints admin token
HUSH_MASTER_KEY=… go run ./cmd/hush server &  # starts on 127.0.0.1:8443

cd sdk/python && pip install -e .             # editable install of SDK
cd sdk/python && pytest                       # SDK tests (uses requests-mock)
```

CI runs `go test`, `go vet`, `gofmt`, and the Python SDK tests on every push to `main` and every pull request. Do not merge if any of these are red.

## Boundaries

- **Never** add a hardcoded credential, IP, or hostname to the codebase, tests, examples, or commit messages. README and tests use the placeholder ranges `10.0.0.0/24` and `10.0.1.0/24` only.
- **Never** add a Go dependency that requires cgo. The binary must remain statically linkable for the release matrix (linux/darwin/windows × amd64/arm64).
- **Never** weaken the audit chain. Every state-mutating handler in `internal/server` must call `audit.Append(...)` before returning success.
- **Never** print, log, or return the plaintext of a vault entry from anywhere except `cmd/hush/cmd_secret.go` (`hush get`), `internal/clientlib` (HTTP response body), and `internal/transport` (passed into the transport at connect time).
- **Never** introduce a transport that buffers an entire stdout in memory; transports must stream.

## Conventions

- Cobra commands live one per file in `cmd/hush/`, named `cmd_<verb>.go`; each file exports a `new<Verb>Cmd()` constructor.
- HTTP handlers follow the `func (s *Server) handle<Resource><Action>(w http.ResponseWriter, r *http.Request)` pattern.
- Errors returned to the CLI surface verbatim; do not wrap with `fmt.Errorf("foo: %w", err)` unless adding genuine context.
- New transports implement `transport.Transport` and register themselves via `transport.Register(name, factory)`.
- New audit categories use the dotted form `<noun>.<verb>` (`secret.read`, `host.exec`, `apikey.mint`).

## Tests

- Unit tests for every package are in `*_test.go` next to the source.
- Tests must not depend on the network or on `/tmp` permissions; use `t.TempDir()`.
- The transport package has an integration test that requires `HUSH_TEST_SSHD=1`; CI does not run it. Never make new tests depend on a live SSH daemon by default.

## When in doubt

The repository's two source-of-truth documents are this file and [`README.md`](./README.md). If they disagree, fix one of them in the same PR — do not silently work around the inconsistency.
