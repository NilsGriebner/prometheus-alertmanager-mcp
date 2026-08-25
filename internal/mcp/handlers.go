package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"alertmanagermcp/internal/alertmanager"

	"github.com/go-openapi/strfmt"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/rs/zerolog/log"
)

const defaultSilenceDuration = 2 * time.Hour

func registerTools(s *mcpserver.MCPServer, c *alertmanager.Client) {
	registerListAlerts(s, c)
	registerGetAlertGroups(s, c)
	registerListSilences(s, c)
	registerGetSilence(s, c)
	registerCreateSilence(s, c)
	registerDeleteSilence(s, c)
	registerGetStatus(s, c)
	registerListReceivers(s, c)
}

func registerListAlerts(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("list_alerts",
		mcp.WithDescription(
			"List alerts from Alertmanager. Returns active alerts by default. "+
				"Use filter to narrow results with label matchers.",
		),
		mcp.WithString("filter",
			mcp.Description(
				"Comma-separated list of label matchers, "+
					"e.g. alertname=\"HighMemory\",severity=\"critical\"",
			),
		),
		mcp.WithBoolean("active",
			mcp.Description("Include active alerts (default: true)"),
			mcp.DefaultBool(true),
		),
		mcp.WithBoolean("silenced",
			mcp.Description("Include silenced alerts (default: false)"),
			mcp.DefaultBool(false),
		),
		mcp.WithBoolean("inhibited",
			mcp.Description("Include inhibited alerts (default: false)"),
			mcp.DefaultBool(false),
		),
		mcp.WithBoolean("unprocessed",
			mcp.Description("Include unprocessed alerts (default: false)"),
			mcp.DefaultBool(false),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filter := parseFilter(req.GetString("filter", ""))
		active := req.GetBool("active", true)
		silenced := req.GetBool("silenced", false)
		inhibited := req.GetBool("inhibited", false)
		unprocessed := req.GetBool("unprocessed", false)

		log.Debug().
			Strs("filter", filter).
			Bool("active", active).
			Bool("silenced", silenced).
			Bool("inhibited", inhibited).
			Bool("unprocessed", unprocessed).
			Msg("tool: list_alerts")

		alerts, err := c.ListAlerts(ctx, filter, active, silenced, inhibited, unprocessed)
		if err != nil {
			log.Debug().Err(err).Msg("tool: list_alerts failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Int("count", len(alerts)).Msg("tool: list_alerts result")
		return jsonResult(alerts)
	})
}

func registerGetAlertGroups(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("get_alert_groups",
		mcp.WithDescription(
			"Get alert groups from Alertmanager. "+
				"Groups are determined by the route configuration's group_by setting.",
		),
		mcp.WithString("filter",
			mcp.Description(
				"Comma-separated list of label matchers, "+
					"e.g. alertname=\"HighMemory\"",
			),
		),
		mcp.WithBoolean("active",
			mcp.Description("Include active alerts (default: true)"),
			mcp.DefaultBool(true),
		),
		mcp.WithBoolean("silenced",
			mcp.Description("Include silenced alerts (default: false)"),
			mcp.DefaultBool(false),
		),
		mcp.WithBoolean("inhibited",
			mcp.Description("Include inhibited alerts (default: false)"),
			mcp.DefaultBool(false),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filter := parseFilter(req.GetString("filter", ""))
		active := req.GetBool("active", true)
		silenced := req.GetBool("silenced", false)
		inhibited := req.GetBool("inhibited", false)

		log.Debug().
			Strs("filter", filter).
			Bool("active", active).
			Bool("silenced", silenced).
			Bool("inhibited", inhibited).
			Msg("tool: get_alert_groups")

		groups, err := c.GetAlertGroups(ctx, filter, active, silenced, inhibited)
		if err != nil {
			log.Debug().Err(err).Msg("tool: get_alert_groups failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Int("count", len(groups)).Msg("tool: get_alert_groups result")
		return jsonResult(groups)
	})
}

func registerListSilences(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("list_silences",
		mcp.WithDescription(
			"List silences from Alertmanager. "+
				"Returns all silences (active, pending, expired) unless filtered.",
		),
		mcp.WithString("filter",
			mcp.Description(
				"Comma-separated list of label matchers to filter silences",
			),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		filter := parseFilter(req.GetString("filter", ""))

		log.Debug().Strs("filter", filter).Msg("tool: list_silences")

		silences, err := c.ListSilences(ctx, filter)
		if err != nil {
			log.Debug().Err(err).Msg("tool: list_silences failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Int("count", len(silences)).Msg("tool: list_silences result")
		return jsonResult(silences)
	})
}

func registerGetSilence(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("get_silence",
		mcp.WithDescription("Get a single silence by ID from Alertmanager."),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("The silence ID"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Str("id", id).Msg("tool: get_silence")

		silence, err := c.GetSilence(ctx, id)
		if err != nil {
			log.Debug().Err(err).Str("id", id).Msg("tool: get_silence failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Str("id", id).Msg("tool: get_silence result")
		return jsonResult(silence)
	})
}

func registerCreateSilence(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("create_silence",
		mcp.WithDescription(
			"Create a silence in Alertmanager. "+
				"Matchers define which alerts to silence. "+
				"Duration or ends_at controls how long the silence lasts.",
		),
		mcp.WithString("matchers",
			mcp.Required(),
			mcp.Description(
				"Comma-separated label matchers, "+
					"e.g. alertname=\"HighMemory\",severity=\"critical\". "+
					"Use =~ for regex, != for negative, !~ for negative regex.",
			),
		),
		mcp.WithString("duration",
			mcp.Description(
				"Duration of silence, e.g. \"2h\", \"30m\", \"1h30m\". "+
					"Used if ends_at is not set. Defaults to 2h.",
			),
		),
		mcp.WithString("ends_at",
			mcp.Description(
				"Explicit end time in RFC3339 format. Overrides duration.",
			),
		),
		mcp.WithString("created_by",
			mcp.Required(),
			mcp.Description("Who is creating this silence (e.g. a username)"),
		),
		mcp.WithString("comment",
			mcp.Required(),
			mcp.Description("Reason for the silence"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		matcherStr, err := req.RequireString("matchers")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		createdBy, err := req.RequireString("created_by")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		comment, err := req.RequireString("comment")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().
			Str("matchers", matcherStr).
			Str("created_by", createdBy).
			Str("comment", comment).
			Msg("tool: create_silence")

		matchers, err := parseMatchers(matcherStr)
		if err != nil {
			return mcp.NewToolResultError(
				fmt.Sprintf("invalid matchers: %v", err),
			), nil
		}

		now := time.Now().UTC()
		startsAt := strfmt.DateTime(now)
		endsAt, err := resolveSilenceEnd(
			now, req.GetString("duration", ""), req.GetString("ends_at", ""),
		)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		ps := &models.PostableSilence{
			Silence: models.Silence{
				StartsAt:  &startsAt,
				EndsAt:    &endsAt,
				Comment:   &comment,
				CreatedBy: &createdBy,
				Matchers:  matchers,
			},
		}

		id, err := c.CreateSilence(ctx, ps)
		if err != nil {
			log.Debug().Err(err).Msg("tool: create_silence failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Str("silence_id", id).Msg("tool: create_silence result")
		return jsonResult(map[string]string{"silenceID": id})
	})
}

func registerDeleteSilence(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("delete_silence",
		mcp.WithDescription(
			"Expire (delete) a silence by ID in Alertmanager.",
		),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("The silence ID to expire"),
		),
	)

	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Str("id", id).Msg("tool: delete_silence")

		if err := c.DeleteSilence(ctx, id); err != nil {
			log.Debug().Err(err).Str("id", id).Msg("tool: delete_silence failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Str("id", id).Msg("tool: delete_silence result")
		return mcp.NewToolResultText(
			fmt.Sprintf("Silence %s expired successfully.", id),
		), nil
	})
}

func registerGetStatus(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("get_status",
		mcp.WithDescription(
			"Get the current status of the Alertmanager instance, "+
				"including cluster status, config, and version info.",
		),
	)

	s.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		log.Debug().Msg("tool: get_status")

		status, err := c.GetStatus(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("tool: get_status failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Msg("tool: get_status result")
		return jsonResult(status)
	})
}

func registerListReceivers(s *mcpserver.MCPServer, c *alertmanager.Client) {
	tool := mcp.NewTool("list_receivers",
		mcp.WithDescription(
			"List all configured receivers in Alertmanager.",
		),
	)

	s.AddTool(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		log.Debug().Msg("tool: list_receivers")

		receivers, err := c.ListReceivers(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("tool: list_receivers failed")
			return mcp.NewToolResultError(err.Error()), nil
		}

		log.Debug().Int("count", len(receivers)).Msg("tool: list_receivers result")
		return jsonResult(receivers)
	})
}

func resolveSilenceEnd(
	now time.Time, duration, endsAt string,
) (strfmt.DateTime, error) {
	end := now.Add(defaultSilenceDuration)

	if duration != "" {
		dur, err := time.ParseDuration(duration)
		if err != nil {
			return strfmt.DateTime{}, fmt.Errorf(
				"invalid duration %q: %w", duration, err,
			)
		}
		end = now.Add(dur)
	}

	if endsAt != "" {
		parsed, err := time.Parse(time.RFC3339, endsAt)
		if err != nil {
			return strfmt.DateTime{}, fmt.Errorf(
				"invalid ends_at %q: %w", endsAt, err,
			)
		}
		end = parsed
	}

	return strfmt.DateTime(end), nil
}

func parseFilter(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func parseMatchers(s string) (models.Matchers, error) {
	var matchers models.Matchers
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		m, err := parseSingleMatcher(part)
		if err != nil {
			return nil, fmt.Errorf("parsing %q: %w", part, err)
		}
		matchers = append(matchers, m)
	}

	if len(matchers) == 0 {
		return nil, errors.New("no matchers provided")
	}
	return matchers, nil
}

func parseSingleMatcher(s string) (*models.Matcher, error) {
	isEqual := true
	isRegex := false

	var name, value string

	if idx := strings.Index(s, "!~"); idx > 0 {
		name = s[:idx]
		value = s[idx+2:]
		isEqual = false
		isRegex = true
	} else if idx := strings.Index(s, "!="); idx > 0 {
		name = s[:idx]
		value = s[idx+2:]
		isEqual = false
	} else if idx := strings.Index(s, "=~"); idx > 0 {
		name = s[:idx]
		value = s[idx+2:]
		isRegex = true
	} else if idx := strings.Index(s, "="); idx > 0 {
		name = s[:idx]
		value = s[idx+1:]
	} else {
		return nil, fmt.Errorf(
			"no operator found in matcher %q", s,
		)
	}

	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"")

	return &models.Matcher{
		Name:    &name,
		Value:   &value,
		IsRegex: &isRegex,
		IsEqual: &isEqual,
	}, nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(
			fmt.Sprintf("encoding result: %v", err),
		), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
