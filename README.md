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

## Usage

```bash
alertmanager-mcp --alertmanager.url http://localhost:9093
```

### Flags

| Flag | Env | Default | Description |
|---|---|---|---|
| `--mcp.listen.address` | `MCP_LISTEN_ADDRESS` | `:8080` | Address to listen on |
| `--alertmanager.url` | `ALERTMANAGER_URL` | | Alertmanager base URL (required) |
| `--alertmanager.username` | `ALERTMANAGER_USERNAME` | | Basic auth username |
| `--alertmanager.password` | `ALERTMANAGER_PASSWORD` | | Basic auth password |
| `--log.level` | `LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |

### Connect to Claude Code

```bash
claude mcp add --scope user --transport http alertmanager http://localhost:8080/mcp
```

## Build

```bash
make build
```

### Docker

```bash
docker build -t alertmanager-mcp .
docker run -p 8080:8080 alertmanager-mcp --alertmanager.url http://alertmanager:9093
```

## Deploy

See [deployment/helm](deployment/helm/README.md) for the Helm chart and its documentation.

```bash
helm install alertmanager-mcp deployment/helm \
  --set alertmanager.url=http://alertmanager:9093
```

## Development

```bash
# Run linters
golangci-lint run ./...
helm lint deployment/helm --set alertmanager.url=http://localhost:9093

# Regenerate Helm chart README
make helm-docs
```