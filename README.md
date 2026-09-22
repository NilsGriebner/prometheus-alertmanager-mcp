# alertmanager-mcp

An [MCP](https://modelcontextprotocol.io/) server that exposes [Prometheus Alertmanager](https://prometheus.io/docs/alerting/latest/alertmanager/) operations as tools, allowing LLMs to query alerts, manage silences, and inspect Alertmanager status.

## Tools

| Tool | Description |
|---|---|
| `list_alerts` | List and filter alerts (active, silenced, inhibited, unprocessed) |
| `get_alert_groups` | Get alerts grouped by routing configuration |
| `list_silences` | List all silences |
| `get_silence` | Get a single silence by ID |
| `create_silence` | Create a silence with matchers and duration |
| `delete_silence` | Expire a silence by ID |
| `get_status` | Get Alertmanager status, cluster info, and config |
| `list_receivers` | List configured receivers |

## Install and run

The server speaks two transports. **stdio** lets your MCP client own the process
— it spawns the server on demand and stops it afterwards, so nothing has to be
running in the background. **http** runs it as a long-lived server instead.

Pick whichever of the three below suits the machine. All of them run the server
natively, so the OIDC browser login works; only the Docker route cannot do it.

### With a Go toolchain

Nothing to install — Go fetches, builds and caches the module on first use:

```bash
go run github.com/NilsGriebner/prometheus-alertmanager-mcp@v0.0.3 \
  --alertmanager.url http://localhost:9093
```

Pin a version rather than using `@latest`, so the client cannot silently start
running different code.

> The first run downloads and compiles, which may exceed your MCP client's
> startup timeout. Run the command once in a terminal to warm the build cache;
> later starts are fast.

### From a release binary

For machines without Go. Download the archive for your platform from the
[releases page](https://github.com/NilsGriebner/prometheus-alertmanager-mcp/releases),
verify it against `checksums.txt`, and put the binary on your `PATH`:

```bash
tar xzf alertmanager-mcp_linux_amd64.tar.gz
sudo install alertmanager-mcp /usr/local/bin/
```

### With Docker

The image is pulled on first use, so nothing is installed either:

```bash
docker run --rm -i ghcr.io/nilsgriebner/prometheus-alertmanager-mcp \
  --mcp.transport=stdio --alertmanager.url=http://alertmanager:9093
```

The OIDC browser login **cannot** work in a container: there is no browser, and
the loopback redirect resolves inside the container's own network namespace
rather than reaching your machine. Use basic auth here, or run the server
natively with one of the routes above.

If you use `--mcp.transport=http` in Docker, also set
`MCP_LISTEN_ADDRESS=0.0.0.0:8080`, because the loopback default is not reachable
through a published port.

## Connect to Claude Code

### stdio

Claude Code starts and stops the server for you. Add it with the CLI:

```bash
claude mcp add --scope user alertmanager -- \
  go run github.com/NilsGriebner/prometheus-alertmanager-mcp@v0.0.3 \
  --mcp.transport=stdio --alertmanager.url=http://localhost:9093
```

Or write the entry directly, choosing the `command` that matches your install:

```json
{
  "mcpServers": {
    "alertmanager": {
      "command": "go",
      "args": [
        "run", "github.com/NilsGriebner/prometheus-alertmanager-mcp@v0.0.3",
        "--mcp.transport=stdio",
        "--alertmanager.url=https://alertmanager.example.com",
        "--alertmanager.oidc.issuer=https://keycloak.example.com/realms/ops",
        "--alertmanager.oidc.client-id=alertmanager-mcp",
        "--alertmanager.oidc.scopes=openid,email,offline_access"
      ]
    }
  }
}
```

A release binary instead:

```json
{
  "mcpServers": {
    "alertmanager": {
      "command": "alertmanager-mcp",
      "args": ["--mcp.transport=stdio", "--alertmanager.url=https://alertmanager.example.com"]
    }
  }
}
```

Docker, keeping credentials out of the arguments:

```json
{
  "mcpServers": {
    "alertmanager": {
      "command": "docker",
      "args": [
        "run", "--rm", "-i",
        "-e", "ALERTMANAGER_URL",
        "-e", "ALERTMANAGER_USERNAME",
        "-e", "ALERTMANAGER_PASSWORD",
        "ghcr.io/nilsgriebner/prometheus-alertmanager-mcp",
        "--mcp.transport=stdio"
      ],
      "env": {
        "ALERTMANAGER_URL": "https://alertmanager.example.com",
        "ALERTMANAGER_USERNAME": "alice",
        "ALERTMANAGER_PASSWORD": "..."
      }
    }
  }
}
```

With OIDC, the browser opens on the **first tool call**, not while the client is
connecting, so the MCP handshake is never held up by the login. The token is
cached afterwards, so it happens once rather than every session.

### http

Start the server yourself, then point Claude Code at it:

```bash
alertmanager-mcp --alertmanager.url http://localhost:9093
claude mcp add --scope user --transport http alertmanager http://127.0.0.1:8080/mcp
```

## Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `--mcp.transport` | `MCP_TRANSPORT` | `http` | Transport: `stdio` or `http` |
| `--mcp.listen.address` | `MCP_LISTEN_ADDRESS` | `127.0.0.1:8080` | Address to listen on (`http` only) |
| `--alertmanager.url` | `ALERTMANAGER_URL` | | Alertmanager base URL (required) |
| `--alertmanager.username` | `ALERTMANAGER_USERNAME` | | Basic auth username |
| `--alertmanager.password` | `ALERTMANAGER_PASSWORD` | | Basic auth password |
| `--alertmanager.oidc.issuer` | `ALERTMANAGER_OIDC_ISSUER` | | OIDC issuer URL; enables browser login |
| `--alertmanager.oidc.client-id` | `ALERTMANAGER_OIDC_CLIENT_ID` | | OIDC client ID |
| `--alertmanager.oidc.client-secret` | `ALERTMANAGER_OIDC_CLIENT_SECRET` | | Only for providers requiring a confidential client |
| `--alertmanager.oidc.scopes` | `ALERTMANAGER_OIDC_SCOPES` | `openid,email` | Scopes to request, comma-separated |
| `--alertmanager.oidc.redirect-port` | `ALERTMANAGER_OIDC_REDIRECT_PORT` | `18080` | Loopback redirect port (`0` picks a free one) |
| `--alertmanager.oidc.use-id-token` | `ALERTMANAGER_OIDC_USE_ID_TOKEN` | `false` | Send the ID token instead of the access token |
| `--alertmanager.oidc.cache-path` | `ALERTMANAGER_OIDC_CACHE_PATH` | user cache dir | Token cache file (`-` disables) |
| `--log.level` | `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

> With `--mcp.transport=http` the MCP endpoint is **unauthenticated**. Anyone who
> can reach it gets every tool, including `create_silence` and `delete_silence`,
> using whatever Alertmanager credentials the server holds. Keep it on loopback
> unless you have put your own authentication in front of it.

## Authentication

### Basic auth

```bash
alertmanager-mcp \
  --alertmanager.url https://alertmanager.example.com \
  --alertmanager.username alice \
  --alertmanager.password "$AM_PASSWORD"
```

### OIDC (browser login)

For an Alertmanager behind an OIDC-authenticating proxy. Setting an issuer and a
client ID enables it, and it takes precedence over basic auth.

```bash
alertmanager-mcp \
  --alertmanager.url https://alertmanager.example.com \
  --alertmanager.oidc.issuer https://keycloak.example.com/realms/ops \
  --alertmanager.oidc.client-id alertmanager-mcp
```

The server opens your browser for the provider's login page, receives the
redirect on a loopback listener, and exchanges the authorization code using
PKCE. The token is cached under your user cache directory with `0600`
permissions and refreshed silently, so later starts do not prompt again.

#### Registering the client

Register a **public** client (PKCE, no secret) with this loopback redirect URI:

```
http://127.0.0.1:18080/callback
```

That is the default port, so nothing else needs configuring. Pick another with
`--alertmanager.oidc.redirect-port` if 18080 is taken on your machine, and
register that one instead.

Setting the port to `0` picks a free one at random on every login. That avoids
port clashes entirely, but the provider then has to accept a wildcard port, as
RFC 8252 §7.3 recommends:

```
http://127.0.0.1:*/callback
```

#### Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `401` from Alertmanager with a token that looks valid | The proxy rejects the access token's audience. Add an audience mapper on the provider so the access token carries the proxy's expected `aud`. Failing that, `--alertmanager.oidc.use-id-token` sends the ID token instead — a workaround, not the correct fix. |
| Browser prompt on every start | No refresh token, or it expired with the SSO session. Add `offline_access` to `--alertmanager.oidc.scopes`. |
| Provider rejects the token exchange without a secret | The client is registered as confidential. Re-register it as public, or pass `--alertmanager.oidc.client-secret`. Note a secret shipped to every workstation is not secret; RFC 8252 §8.5 advises against it. |
| `invalid redirect_uri` | The loopback URI is not registered. Register `http://127.0.0.1:18080/callback`, or whichever port you set. See [Registering the client](#registering-the-client). |
| `opening loopback listener on port 18080` | Something else holds the port. Pick a free one with `--alertmanager.oidc.redirect-port` and register it too. |
| No browser opens under Docker | Expected; the login cannot run in a container. Run the server natively instead. |

## Development

```bash
make build
golangci-lint run ./...
go test ./...
```
