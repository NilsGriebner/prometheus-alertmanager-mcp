package alertmanager

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

const (
	// loginTimeout bounds how long the server waits for the user to complete
	// the browser login before giving up.
	loginTimeout = 5 * time.Minute
	// discoveryTimeout bounds the OIDC discovery request.
	discoveryTimeout = 30 * time.Second
	// readHeaderTimeout bounds header reads on the loopback callback server.
	readHeaderTimeout = 10 * time.Second
	// stateEntropyBytes is the size of the generated state parameter.
	stateEntropyBytes = 32
)

// OIDCConfig describes the authorization code + PKCE login used to obtain
// bearer tokens for an OIDC-protected Alertmanager API.
type OIDCConfig struct {
	// Issuer is the OIDC issuer URL. The authorization and token endpoints are
	// resolved from its discovery document.
	Issuer   string
	ClientID string
	// ClientSecret is optional and should normally stay empty: a secret
	// distributed with a per-workstation binary is not secret, and RFC 8252
	// §8.5 tells native apps not to use one. PKCE authenticates the exchange
	// instead. Set it only when the provider insists on a confidential client
	// registration and rejects the token exchange without it.
	ClientSecret string
	Scopes       []string

	// RedirectPort is the fixed loopback port for the redirect URI. Zero picks
	// a random free port, which requires the provider to allow a wildcard port
	// on the registered redirect URI.
	RedirectPort int

	// UseIDToken sends the ID token instead of the access token as the bearer
	// credential. The access token is the correct credential for an API, so
	// this is a workaround for providers whose access token carries an
	// audience the proxy in front of Alertmanager rejects, or which issue
	// opaque access tokens a JWT-validating proxy cannot parse. Configuring an
	// audience mapper on the provider is the better fix where that is possible.
	UseIDToken bool

	// CachePath is the file the token is cached in. Empty disables caching,
	// forcing a browser login on every start.
	CachePath string
}

// Enabled reports whether OIDC login is configured.
func (c OIDCConfig) Enabled() bool {
	return c.Issuer != "" && c.ClientID != ""
}

func (c OIDCConfig) validate() error {
	if c.Issuer == "" {
		return errors.New("oidc issuer is required")
	}
	if c.ClientID == "" {
		return errors.New("oidc client ID is required")
	}
	if c.RedirectPort < 0 || c.RedirectPort > 65535 {
		return fmt.Errorf("invalid oidc redirect port %d", c.RedirectPort)
	}

	return nil
}

// endpoints holds the parts of the discovery document we need.
type endpoints struct {
	AuthURL  string `json:"authorization_endpoint"`
	TokenURL string `json:"token_endpoint"`
}

// NewOIDCHTTPClient returns an HTTP client that attaches an OIDC bearer token
// to every request, refreshing it as needed.
//
// Discovery and login are deferred to the first request. Logging in eagerly
// would block a stdio server before it can answer the MCP handshake, and the
// client would give up while the browser tab was still open.
func NewOIDCHTTPClient(
	ctx context.Context, cfg OIDCConfig,
) (*http.Client, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &http.Client{
		Transport: &bearerTransport{
			tokens:     &tokenManager{baseCtx: ctx, cfg: cfg},
			useIDToken: cfg.UseIDToken,
		},
	}, nil
}

// tokenManager owns the OIDC token: it logs in on demand, refreshes, persists
// and re-authenticates when the refresh token is no longer accepted. Requests
// are served concurrently, so every field is guarded by mu.
type tokenManager struct {
	mu      sync.Mutex
	baseCtx context.Context //nolint:containedctx // outlives any single request
	cfg     OIDCConfig

	oauthCfg *oauth2.Config
	source   oauth2.TokenSource

	lastAccessToken string
	// lastIDToken is retained because providers need not repeat the ID token
	// in a refresh response.
	lastIDToken string
}

// credential returns the bearer value for a request, logging in if needed.
func (m *tokenManager) credential(useIDToken bool) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.source == nil {
		if err := m.initLocked(); err != nil {
			return "", err
		}
	}

	token, err := m.source.Token()
	if err != nil {
		if !isGrantRejected(err) {
			return "", fmt.Errorf("obtaining access token: %w", err)
		}

		// The refresh token is no longer accepted, e.g. the SSO session ended.
		// Drop the stale cache and send the user through login once more.
		log.Info().Msg("stored OIDC session rejected, logging in again")

		if loginErr := m.loginLocked(); loginErr != nil {
			return "", loginErr
		}

		token, err = m.source.Token()
		if err != nil {
			return "", fmt.Errorf("obtaining access token after login: %w", err)
		}
	}

	m.recordLocked(token)

	if !useIDToken {
		return token.AccessToken, nil
	}

	if m.lastIDToken == "" {
		return "", errors.New("token response contained no ID token")
	}

	return m.lastIDToken, nil
}

// initLocked resolves the provider and restores a cached session, falling back
// to a browser login.
func (m *tokenManager) initLocked() error {
	eps, err := discover(m.baseCtx, m.cfg.Issuer)
	if err != nil {
		return err
	}

	m.oauthCfg = &oauth2.Config{
		ClientID:     m.cfg.ClientID,
		ClientSecret: m.cfg.ClientSecret,
		Scopes:       m.cfg.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  eps.AuthURL,
			TokenURL: eps.TokenURL,
		},
	}

	token, err := cachedToken(m.cfg.CachePath)
	if err != nil && !errors.Is(err, errNoCachedToken) {
		log.Warn().Err(err).Msg("ignoring unusable token cache")
	}

	// A cached token without a refresh token is useless once it expires.
	if token != nil && !token.Valid() && token.RefreshToken == "" {
		token = nil
	}

	if token == nil {
		return m.loginLocked()
	}

	if idToken, ok := token.Extra("id_token").(string); ok {
		m.lastIDToken = idToken
	}

	m.lastAccessToken = token.AccessToken
	m.source = m.oauthCfg.TokenSource(m.baseCtx, token)

	return nil
}

// loginLocked discards any cached session and runs the browser login.
func (m *tokenManager) loginLocked() error {
	m.source = nil
	m.lastAccessToken = ""
	m.lastIDToken = ""
	discardToken(m.cfg.CachePath)

	token, err := browserLogin(m.baseCtx, m.oauthCfg, m.cfg.RedirectPort)
	if err != nil {
		return err
	}

	m.source = m.oauthCfg.TokenSource(m.baseCtx, token)
	m.recordLocked(token)

	return nil
}

// recordLocked remembers a newly issued token and persists it.
func (m *tokenManager) recordLocked(token *oauth2.Token) {
	if idToken, ok := token.Extra("id_token").(string); ok && idToken != "" {
		m.lastIDToken = idToken
	}

	if token.AccessToken == m.lastAccessToken {
		return
	}

	m.lastAccessToken = token.AccessToken

	if err := storeToken(m.cfg.CachePath, token); err != nil {
		log.Warn().Err(err).Msg("failed to cache token")
	}
}

// isGrantRejected reports whether the provider refused the grant outright,
// which no amount of retrying will fix but a fresh login will.
func isGrantRejected(err error) bool {
	var retrieveErr *oauth2.RetrieveError
	if !errors.As(err, &retrieveErr) {
		return false
	}

	if retrieveErr.ErrorCode == "invalid_grant" {
		return true
	}

	return retrieveErr.Response != nil &&
		retrieveErr.Response.StatusCode == http.StatusBadRequest
}

// discover resolves the authorization and token endpoints from the issuer's
// OpenID Connect discovery document.
func discover(ctx context.Context, issuer string) (*endpoints, error) {
	discoveryURL := strings.TrimSuffix(issuer, "/") +
		"/.well-known/openid-configuration"

	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, discoveryURL, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("building discovery request: %w", err)
	}

	client := &http.Client{Timeout: discoveryTimeout}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", discoveryURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"fetching %s: unexpected status %s", discoveryURL, resp.Status,
		)
	}

	var eps endpoints
	if err := json.NewDecoder(resp.Body).Decode(&eps); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", discoveryURL, err)
	}

	if eps.AuthURL == "" || eps.TokenURL == "" {
		return nil, fmt.Errorf(
			"discovery document %s is missing endpoints", discoveryURL,
		)
	}

	return &eps, nil
}

// callbackResult carries the outcome of the redirect back from the provider.
type callbackResult struct {
	code string
	err  error
}

// browserLogin runs the authorization code flow with PKCE, opening the user's
// browser and receiving the redirect on a loopback listener.
func browserLogin(
	ctx context.Context, oauthCfg *oauth2.Config, port int,
) (*oauth2.Token, error) {
	var lc net.ListenConfig

	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("opening loopback listener: %w", err)
	}
	defer func() { _ = listener.Close() }()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, errors.New("loopback listener has no TCP address")
	}

	oauthCfg.RedirectURL = fmt.Sprintf(
		"http://127.0.0.1:%d/callback", addr.Port,
	)

	state, err := randomString()
	if err != nil {
		return nil, err
	}

	verifier := oauth2.GenerateVerifier()
	authURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
	)

	results := make(chan callbackResult, 1)
	srv := &http.Server{
		Handler:           callbackHandler(state, results),
		ReadHeaderTimeout: readHeaderTimeout,
	}
	defer func() { _ = srv.Close() }()

	go func() {
		if err := srv.Serve(listener); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			select {
			case results <- callbackResult{err: err}:
			default:
			}
		}
	}()

	log.Info().
		Str("url", authURL).
		Msg("opening browser for OIDC login")

	if err := openURL(ctx, authURL); err != nil {
		log.Warn().Err(err).
			Str("url", authURL).
			Msg("could not open a browser, open the URL manually")
	}

	waitCtx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	select {
	case <-waitCtx.Done():
		return nil, fmt.Errorf("waiting for OIDC login: %w", waitCtx.Err())
	case res := <-results:
		if res.err != nil {
			return nil, res.err
		}

		token, err := oauthCfg.Exchange(
			ctx, res.code, oauth2.VerifierOption(verifier),
		)
		if err != nil {
			return nil, fmt.Errorf("exchanging authorization code: %w", err)
		}

		log.Info().Msg("OIDC login successful")

		return token, nil
	}
}

// callbackHandler serves the redirect URI, validating state and handing the
// authorization code back to the login flow.
func callbackHandler(
	state string, results chan<- callbackResult,
) http.Handler {
	// The browser may hit /callback more than once (prefetch, reload, provider
	// retry). Only the first outcome matters, and a send must never block a
	// handler goroutine, so later ones are dropped.
	report := func(res callbackResult) {
		select {
		case results <- res:
		default:
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		if errCode := query.Get("error"); errCode != "" {
			desc := query.Get("error_description")
			http.Error(w, "login failed: "+errCode, http.StatusBadRequest)
			report(callbackResult{
				err: fmt.Errorf("provider returned %s: %s", errCode, desc),
			})

			return
		}

		// Constant-time comparison keeps the check free of timing signals.
		got, want := query.Get("state"), state
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			report(callbackResult{err: errors.New("state mismatch")})

			return
		}

		code := query.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			report(callbackResult{
				err: errors.New("callback contained no authorization code"),
			})

			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(
			"<html><body><h3>Login successful</h3>" +
				"<p>You can close this tab and return to the terminal.</p>" +
				"</body></html>",
		))

		report(callbackResult{code: code})
	})

	return mux
}

// openURL launches the system browser; indirected so tests can substitute it.
//
//nolint:gochecknoglobals // seam for testing the browser login flow
var openURL = openBrowser

// openBrowser launches the system browser for the given URL.
func openBrowser(ctx context.Context, target string) error {
	var cmd string

	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}

	args = append(args, target)

	if err := exec.CommandContext(ctx, cmd, args...).Start(); err != nil {
		return fmt.Errorf("launching %s: %w", cmd, err)
	}

	return nil
}

// randomString returns 32 bytes of entropy in URL-safe base64.
func randomString() (string, error) {
	buf := make([]byte, stateEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating random value: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// bearerTransport attaches the OIDC token to outgoing requests.
type bearerTransport struct {
	tokens     *tokenManager
	useIDToken bool
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	credential, err := t.tokens.credential(t.useIDToken)
	if err != nil {
		return nil, err
	}

	// RoundTrippers must not modify the request they are given.
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+credential)

	return http.DefaultTransport.RoundTrip(clone)
}
