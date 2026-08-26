package alertmanager

import (
	"context"
	"net/url"

	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	amclient "github.com/prometheus/alertmanager/api/v2/client"
	"github.com/prometheus/alertmanager/api/v2/client/alert"
	"github.com/prometheus/alertmanager/api/v2/client/alertgroup"
	"github.com/prometheus/alertmanager/api/v2/client/general"
	"github.com/prometheus/alertmanager/api/v2/client/receiver"
	"github.com/prometheus/alertmanager/api/v2/client/silence"
	"github.com/prometheus/alertmanager/api/v2/models"
	"github.com/rs/zerolog/log"
)

// Client wraps the Alertmanager v2 API client.
type Client struct {
	api *amclient.AlertmanagerAPI
}

type ClientOption func(*clientConfig)

type clientConfig struct {
	username string
	password string
}

func WithBasicAuth(username, password string) ClientOption {
	return func(c *clientConfig) {
		c.username = username
		c.password = password
	}
}

func NewClient(baseURL string, opts ...ClientOption) *Client {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	parsed, _ := url.Parse(baseURL)
	host := parsed.Host
	schemes := []string{parsed.Scheme}
	if parsed.Scheme == "" {
		schemes = []string{"http"}
	}

	transport := httptransport.New(
		host, amclient.DefaultBasePath, schemes,
	)
	if cfg.username != "" {
		transport.DefaultAuthentication = httptransport.BasicAuth(
			cfg.username, cfg.password,
		)
	}

	return &Client{
		api: amclient.New(transport, strfmt.Default),
	}
}

func (c *Client) ListAlerts(
	ctx context.Context, filter []string,
	active, silenced, inhibited, unprocessed bool,
) (models.GettableAlerts, error) {
	log.Debug().Strs("filter", filter).Msg("listing alerts")

	params := alert.NewGetAlertsParamsWithContext(ctx).
		WithFilter(filter).
		WithActive(&active).
		WithSilenced(&silenced).
		WithInhibited(&inhibited).
		WithUnprocessed(&unprocessed)

	resp, err := c.api.Alert.GetAlerts(params)
	if err != nil {
		log.Error().Err(err).Msg("failed to list alerts")
		return nil, err
	}

	return resp.Payload, nil
}

func (c *Client) GetAlertGroups(
	ctx context.Context, filter []string,
	active, silenced, inhibited bool,
) (models.AlertGroups, error) {
	log.Debug().Strs("filter", filter).Msg("getting alert groups")

	params := alertgroup.NewGetAlertGroupsParamsWithContext(ctx).
		WithFilter(filter).
		WithActive(&active).
		WithSilenced(&silenced).
		WithInhibited(&inhibited)

	resp, err := c.api.Alertgroup.GetAlertGroups(params)
	if err != nil {
		log.Error().Err(err).Msg("failed to get alert groups")
		return nil, err
	}

	return resp.Payload, nil
}

func (c *Client) ListSilences(
	ctx context.Context, filter []string,
) (models.GettableSilences, error) {
	log.Debug().Strs("filter", filter).Msg("listing silences")

	params := silence.NewGetSilencesParamsWithContext(ctx).
		WithFilter(filter)

	resp, err := c.api.Silence.GetSilences(params)
	if err != nil {
		log.Error().Err(err).Msg("failed to list silences")
		return nil, err
	}

	return resp.Payload, nil
}

func (c *Client) GetSilence(
	ctx context.Context, id string,
) (*models.GettableSilence, error) {
	log.Debug().Str("id", id).Msg("getting silence")

	params := silence.NewGetSilenceParamsWithContext(ctx).
		WithSilenceID(strfmt.UUID(id))

	resp, err := c.api.Silence.GetSilence(params)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("failed to get silence")
		return nil, err
	}

	return resp.Payload, nil
}

func (c *Client) CreateSilence(
	ctx context.Context, s *models.PostableSilence,
) (string, error) {
	log.Debug().Msg("creating silence")

	params := silence.NewPostSilencesParamsWithContext(ctx).
		WithSilence(s)

	resp, err := c.api.Silence.PostSilences(params)
	if err != nil {
		log.Error().Err(err).Msg("failed to create silence")
		return "", err
	}

	return resp.Payload.SilenceID, nil
}

func (c *Client) DeleteSilence(ctx context.Context, id string) error {
	log.Debug().Str("id", id).Msg("deleting silence")

	params := silence.NewDeleteSilenceParamsWithContext(ctx).
		WithSilenceID(strfmt.UUID(id))

	_, err := c.api.Silence.DeleteSilence(params)
	if err != nil {
		log.Error().Err(err).Str("id", id).Msg("failed to delete silence")
		return err
	}

	return nil
}

func (c *Client) GetStatus(
	ctx context.Context,
) (*models.AlertmanagerStatus, error) {
	log.Debug().Msg("getting status")

	resp, err := c.api.General.GetStatus(
		general.NewGetStatusParamsWithContext(ctx),
	)
	if err != nil {
		log.Error().Err(err).Msg("failed to get status")
		return nil, err
	}

	return resp.Payload, nil
}

func (c *Client) ListReceivers(
	ctx context.Context,
) ([]*models.Receiver, error) {
	log.Debug().Msg("listing receivers")

	resp, err := c.api.Receiver.GetReceivers(
		receiver.NewGetReceiversParamsWithContext(ctx),
	)
	if err != nil {
		log.Error().Err(err).Msg("failed to list receivers")
		return nil, err
	}

	return resp.Payload, nil
}
