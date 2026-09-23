# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make build                      # go build -o alertmanager-mcp .
go test ./...
go test ./internal/alertmanager -run TestOIDCReusesCachedToken -v   # single test
golangci-lint run ./...         # CI pins golangci-lint v2.11
```

CI (`.github/workflows/ci.yaml`) runs only golangci-lint and a Docker build — `go test` is **not** in CI, so run it locally before pushing.

Releases are tag-driven (`v*`): the workflow builds cross-platform binaries, pushes a ghcr.io image, generates `CHANGELOG.md` and commits it back to `main`. Never hand-edit `CHANGELOG.md`.

## Architecture

An MCP server (mark3labs/mcp-go) that exposes the Prometheus Alertmanager v2 API as tools. Three layers, one direction of dependency:

- `cmd/root.go` — Cobra + Viper wiring. Every flag is bound twice: `mustBindPFlag` for the flag and `mustBindEnv` for the `UPPER_SNAKE` env var. Adding a flag means touching both lists plus the README flag table. `clientOptsFromViper` picks the auth method: OIDC wins over basic auth, and a half-configured OIDC pair (issuer without client-id or vice versa) is a **startup error**, deliberately, so it cannot degrade into unauthenticated requests that fail later as an opaque 401.
- `internal/mcp/` — `server.go` constructs the MCPServer; `handlers.go` holds one `registerX` function per tool, each defining the schema and a closure that calls the client and returns `jsonResult`. Tool errors are returned as `mcp.NewToolResultError(...), nil` — never as a Go error, so the LLM sees the message instead of the transport failing.
- `internal/alertmanager/` — thin wrapper over `prometheus/alertmanager/api/v2/client` (go-openapi generated), plus the OIDC login.

Transport is chosen at runtime: `stdio` (client owns the process; logs must stay on stderr because stdout carries JSON-RPC) or `http` (default, bound to loopback — the MCP endpoint is unauthenticated).

### Matcher / filter parsing

`parseSingleMatcher` in `internal/mcp/handlers.go` hand-parses `name<op>value` strings, checking `!~`, `!=`, `=~`, `=` in that order — the order matters, since `=` would otherwise swallow `=~`. Values have surrounding quotes stripped. Matchers and filters are both comma-separated, so a value containing a comma cannot be expressed.

### OIDC login

`internal/alertmanager/oidc.go` implements authorization code + PKCE with a loopback redirect. Design constraints that are easy to break:

- **Login is deferred to the first HTTP request**, not to startup. `NewOIDCHTTPClient` only validates config; `tokenManager.initLocked` runs on the first `RoundTrip`. Logging in eagerly would block the stdio MCP handshake while the browser tab is still open, and the client would time out.
- `tokenManager` serialises everything under `mu`; methods ending in `Locked` assume the mutex is held.
- The ID token is tracked separately (`lastIDToken`) because providers need not repeat it on refresh, and it lives outside `oauth2.Token`. The cache file (`tokencache.go`) stores it as a sibling field and re-attaches it via `WithExtra`.
- `isGrantRejected` detects a dead refresh token (`invalid_grant` / HTTP 400) and triggers one fresh browser login rather than failing.
- The token cache is written through a temp file + rename at `0600`, keyed by `sha256(issuer\x00clientID)` under the user cache dir.
- `openURL` is a package-level var so tests can substitute the browser launch; that is why the `gochecknoglobals` nolint is there.

Tests use `fakeProvider` in `oidc_test.go` — a full httptest OIDC provider with switches for rejecting refreshes and omitting the ID token on refresh. Extend it rather than mocking the transport.

## Conventions

- Logging is zerolog via the `log` global (`github.com/rs/zerolog/log`). `depguard` bans the stdlib `log` outside `main.go`.
- The linter set is broad and strict (`gochecknoglobals`, `mnd`, `lll`, `funlen`, `godot`, `gosec`, …). `nolintlint` requires an explanation and a specific linter on every directive. gosec taint rules G702-G704 are excluded for `internal/alertmanager/` at the config level, because nolintlint cannot see suppressions of those rules.
- Comments explain *why*, not *what* — the existing code documents non-obvious decisions (deferred login, constant-time state compare, dropped duplicate callbacks) and leaves the mechanics unannotated. Match that.
