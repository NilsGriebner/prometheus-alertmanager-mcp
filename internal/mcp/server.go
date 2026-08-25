package mcp

import (
	"alertmanagermcp/internal/alertmanager"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
)

// NewServer creates a configured MCP server with all Alertmanager tools registered.
func NewServer(
	alertmanagerURL string, logger zerolog.Logger,
	clientOpts ...alertmanager.ClientOption,
) *mcpserver.MCPServer {
	client := alertmanager.NewClient(alertmanagerURL, logger, clientOpts...)

	s := mcpserver.NewMCPServer(
		"alertmanager-mcp",
		"0.1.0",
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithRecovery(),
	)

	registerTools(s, client)

	return s
}
