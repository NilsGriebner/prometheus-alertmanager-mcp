package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/NilsGriebner/prometheus-alertmanager-mcp/internal/alertmanager"
	internalmcp "github.com/NilsGriebner/prometheus-alertmanager-mcp/internal/mcp"

	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Supported MCP transports.
const (
	transportStdio = "stdio"
	transportHTTP  = "http"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alertmanager-mcp",
		Short: "MCP server for Prometheus Alertmanager",
		Long: "An MCP (Model Context Protocol) server that " +
			"exposes Prometheus Alertmanager operations as tools.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServer(cmd.Context())
		},
	}

	cmd.Flags().String(
		"mcp.transport", transportHTTP,
		"MCP transport: stdio (client owns the process) or http",
	)
	cmd.Flags().String(
		"mcp.listen.address", "127.0.0.1:8080",
		"Address to listen on; loopback by default because the MCP endpoint "+
			"is unauthenticated (set 0.0.0.0:8080 to expose it deliberately)",
	)
	cmd.Flags().String(
		"alertmanager.url", "",
		"Alertmanager base URL (e.g. http://localhost:9093)",
	)
	cmd.Flags().String("alertmanager.username", "", "Basic auth username")
	cmd.Flags().String("alertmanager.password", "", "Basic auth password")
	cmd.Flags().String(
		"alertmanager.oidc.issuer", "",
		"OIDC issuer URL; enables browser login when set with a client ID",
	)
	cmd.Flags().String(
		"alertmanager.oidc.client-id", "", "OIDC client ID",
	)
	cmd.Flags().String(
		"alertmanager.oidc.client-secret", "",
		"OIDC client secret; leave unset unless the provider requires a "+
			"confidential client, as PKCE already protects the browser flow",
	)
	cmd.Flags().StringSlice(
		"alertmanager.oidc.scopes", []string{"openid", "email"},
		"OIDC scopes to request (add offline_access for longer-lived refresh)",
	)
	cmd.Flags().Int(
		"alertmanager.oidc.redirect-port", 0,
		"Fixed loopback port for the redirect URI (0 picks a free port)",
	)
	cmd.Flags().Bool(
		"alertmanager.oidc.use-id-token", false,
		"Send the ID token instead of the access token as the bearer; a "+
			"workaround for proxies rejecting the access token audience, "+
			"preferably fixed by an audience mapper on the provider",
	)
	cmd.Flags().String(
		"alertmanager.oidc.cache-path", "",
		"Token cache file (defaults to the user cache dir; \"-\" disables)",
	)
	cmd.Flags().String(
		"log.level", "info",
		"Log level (debug, info, warn, error)",
	)

	mustBindPFlag("mcp.transport", cmd)
	mustBindPFlag("mcp.listen.address", cmd)
	mustBindPFlag("alertmanager.url", cmd)
	mustBindPFlag("alertmanager.username", cmd)
	mustBindPFlag("alertmanager.password", cmd)
	mustBindPFlag("alertmanager.oidc.issuer", cmd)
	mustBindPFlag("alertmanager.oidc.client-id", cmd)
	mustBindPFlag("alertmanager.oidc.client-secret", cmd)
	mustBindPFlag("alertmanager.oidc.scopes", cmd)
	mustBindPFlag("alertmanager.oidc.redirect-port", cmd)
	mustBindPFlag("alertmanager.oidc.use-id-token", cmd)
	mustBindPFlag("alertmanager.oidc.cache-path", cmd)
	mustBindPFlag("log.level", cmd)

	return cmd
}

// runServer builds the Alertmanager client and serves MCP over HTTP.
func runServer(ctx context.Context) error {
	configureLogging()

	alertmanagerURL := viper.GetString("alertmanager.url")
	if alertmanagerURL == "" {
		return errors.New(
			"alertmanager URL is required " +
				"(set --alertmanager.url or ALERTMANAGER_URL)",
		)
	}

	clientOpts, err := clientOptsFromViper(ctx)
	if err != nil {
		return err
	}

	s := internalmcp.NewServer(alertmanagerURL, clientOpts...)

	switch transport := viper.GetString("mcp.transport"); transport {
	case transportStdio:
		// Logs go to stderr, leaving stdout free for the JSON-RPC stream.
		log.Info().
			Str("alertmanager_url", alertmanagerURL).
			Msg("serving alertmanager-mcp over stdio")

		if err := server.ServeStdio(s); err != nil {
			return fmt.Errorf("stdio server error: %w", err)
		}
	case transportHTTP:
		addr := viper.GetString("mcp.listen.address")

		log.Info().
			Str("alertmanager_url", alertmanagerURL).
			Str("listen_address", addr).
			Msg("starting alertmanager-mcp server")

		if err := server.NewStreamableHTTPServer(s).Start(addr); err != nil {
			return fmt.Errorf("server error: %w", err)
		}
	default:
		return fmt.Errorf(
			"unknown transport %q (want %q or %q)",
			transport, transportStdio, transportHTTP,
		)
	}

	return nil
}

// clientOptsFromViper selects the configured authentication method, preferring
// OIDC over basic auth when both are set.
func clientOptsFromViper(
	ctx context.Context,
) ([]alertmanager.ClientOption, error) {
	var clientOpts []alertmanager.ClientOption

	oidcCfg := oidcConfigFromViper()

	switch {
	case oidcCfg.Enabled():
		httpClient, err := alertmanager.NewOIDCHTTPClient(ctx, oidcCfg)
		if err != nil {
			return nil, fmt.Errorf("oidc login: %w", err)
		}

		clientOpts = append(
			clientOpts, alertmanager.WithHTTPClient(httpClient),
		)
	case viper.GetString("alertmanager.username") != "":
		clientOpts = append(clientOpts,
			alertmanager.WithBasicAuth(
				viper.GetString("alertmanager.username"),
				viper.GetString("alertmanager.password"),
			),
		)
	}

	return clientOpts, nil
}

func Execute() {
	cobra.OnInitialize(initConfig)

	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func initConfig() {
	viper.AutomaticEnv()

	mustBindEnv("mcp.transport", "MCP_TRANSPORT")
	mustBindEnv("mcp.listen.address", "MCP_LISTEN_ADDRESS")
	mustBindEnv("alertmanager.url", "ALERTMANAGER_URL")
	mustBindEnv("alertmanager.username", "ALERTMANAGER_USERNAME")
	mustBindEnv("alertmanager.password", "ALERTMANAGER_PASSWORD")
	mustBindEnv("alertmanager.oidc.issuer", "ALERTMANAGER_OIDC_ISSUER")
	mustBindEnv("alertmanager.oidc.client-id", "ALERTMANAGER_OIDC_CLIENT_ID")
	mustBindEnv(
		"alertmanager.oidc.client-secret", "ALERTMANAGER_OIDC_CLIENT_SECRET",
	)
	mustBindEnv("alertmanager.oidc.scopes", "ALERTMANAGER_OIDC_SCOPES")
	mustBindEnv(
		"alertmanager.oidc.redirect-port", "ALERTMANAGER_OIDC_REDIRECT_PORT",
	)
	mustBindEnv(
		"alertmanager.oidc.use-id-token", "ALERTMANAGER_OIDC_USE_ID_TOKEN",
	)
	mustBindEnv("alertmanager.oidc.cache-path", "ALERTMANAGER_OIDC_CACHE_PATH")
	mustBindEnv("log.level", "LOG_LEVEL")
}

// normalizeScopes flattens comma-separated scope values. Viper splits a slice
// flag for us, but an environment variable arrives as one string and is split
// on whitespace only, which would send the whole list as a single scope.
func normalizeScopes(values []string) []string {
	scopes := make([]string, 0, len(values))

	for _, value := range values {
		for _, scope := range strings.Split(value, ",") {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
	}

	return scopes
}

// oidcConfigFromViper assembles the OIDC settings, resolving the default token
// cache location when none was configured.
func oidcConfigFromViper() alertmanager.OIDCConfig {
	cfg := alertmanager.OIDCConfig{
		Issuer:       viper.GetString("alertmanager.oidc.issuer"),
		ClientID:     viper.GetString("alertmanager.oidc.client-id"),
		ClientSecret: viper.GetString("alertmanager.oidc.client-secret"),
		Scopes:       normalizeScopes(viper.GetStringSlice("alertmanager.oidc.scopes")),
		RedirectPort: viper.GetInt("alertmanager.oidc.redirect-port"),
		UseIDToken:   viper.GetBool("alertmanager.oidc.use-id-token"),
	}

	if !cfg.Enabled() {
		return cfg
	}

	switch path := viper.GetString("alertmanager.oidc.cache-path"); path {
	case "-":
		// Caching explicitly disabled.
	case "":
		defaultPath, err := alertmanager.DefaultCachePath(
			cfg.Issuer, cfg.ClientID,
		)
		if err != nil {
			log.Warn().Err(err).Msg("token caching disabled")
		}

		cfg.CachePath = defaultPath
	default:
		cfg.CachePath = path
	}

	return cfg
}

func mustBindPFlag(key string, cmd *cobra.Command) {
	if err := viper.BindPFlag(key, cmd.Flags().Lookup(key)); err != nil {
		panic(fmt.Sprintf("binding pflag %q: %v", key, err))
	}
}

func mustBindEnv(key, env string) {
	if err := viper.BindEnv(key, env); err != nil {
		panic(fmt.Sprintf("binding env %q: %v", key, err))
	}
}

func configureLogging() {
	level, err := zerolog.ParseLevel(viper.GetString("log.level"))
	if err != nil {
		log.Fatal().Err(err).Msg("unable to parse log level")
	}
	zerolog.SetGlobalLevel(level)
}
