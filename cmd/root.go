package cmd

import (
	"errors"
	"fmt"
	"os"

	"alertmanagermcp/internal/alertmanager"
	internalmcp "alertmanagermcp/internal/mcp"

	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alertmanager-mcp",
		Short: "MCP server for Prometheus Alertmanager",
		Long: "An MCP (Model Context Protocol) server that " +
			"exposes Prometheus Alertmanager operations as tools.",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := setupLogger()

			alertmanagerURL := viper.GetString("alertmanager.url")
			if alertmanagerURL == "" {
				return errors.New(
					"alertmanager URL is required " +
						"(set --alertmanager.url or ALERTMANAGER_URL)",
				)
			}

			var clientOpts []alertmanager.ClientOption
			if u := viper.GetString("alertmanager.username"); u != "" {
				clientOpts = append(clientOpts,
					alertmanager.WithBasicAuth(
						u, viper.GetString("alertmanager.password"),
					),
				)
			}

			s := internalmcp.NewServer(
				alertmanagerURL, logger, clientOpts...,
			)

			addr := viper.GetString("mcp.listen.address")
			httpServer := server.NewStreamableHTTPServer(s)

			logger.Info().
				Str("alertmanager_url", alertmanagerURL).
				Str("listen_address", addr).
				Msg("starting alertmanager-mcp server")

			if err := httpServer.Start(addr); err != nil {
				return fmt.Errorf("server error: %w", err)
			}

			return nil
		},
	}

	cmd.Flags().String(
		"mcp.listen.address", ":8080",
		"Address to listen on (e.g. :8080, 0.0.0.0:9094)",
	)
	cmd.Flags().String(
		"alertmanager.url", "",
		"Alertmanager base URL (e.g. http://localhost:9093)",
	)
	cmd.Flags().String("alertmanager.username", "", "Basic auth username")
	cmd.Flags().String("alertmanager.password", "", "Basic auth password")
	cmd.Flags().String(
		"log.level", "info",
		"Log level (debug, info, warn, error)",
	)

	mustBindPFlag("mcp.listen.address", cmd)
	mustBindPFlag("alertmanager.url", cmd)
	mustBindPFlag("alertmanager.username", cmd)
	mustBindPFlag("alertmanager.password", cmd)
	mustBindPFlag("log.level", cmd)

	return cmd
}

func Execute() {
	cobra.OnInitialize(initConfig)

	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func initConfig() {
	viper.AutomaticEnv()

	mustBindEnv("mcp.listen.address", "MCP_LISTEN_ADDRESS")
	mustBindEnv("alertmanager.url", "ALERTMANAGER_URL")
	mustBindEnv("alertmanager.username", "ALERTMANAGER_USERNAME")
	mustBindEnv("alertmanager.password", "ALERTMANAGER_PASSWORD")
	mustBindEnv("log.level", "LOG_LEVEL")
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

func setupLogger() zerolog.Logger {
	level, err := zerolog.ParseLevel(viper.GetString("log.level"))
	if err != nil {
		level = zerolog.InfoLevel
	}

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(level)
	log.Logger = logger //nolint:reassign // intentionally setting global logger

	return logger
}
