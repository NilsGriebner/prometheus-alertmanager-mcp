package alertmanager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// fakeProvider is a minimal OIDC provider: discovery, an authorization
// endpoint that redirects straight back, and a token endpoint.
type fakeProvider struct {
	server *httptest.Server

	gotVerifierChallenge string
	gotCode              string
	issuedIDToken        string

	// rejectRefresh makes the token endpoint refuse refresh_token grants the
	// way a provider does once the session is gone.
	rejectRefresh bool
	// omitIDTokenOnRefresh mimics providers that only return an ID token on
	// the initial exchange.
	omitIDTokenOnRefresh bool
	refreshes            int
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()

	p := &fakeProvider{issuedIDToken: "id-token-value"}
	mux := http.NewServeMux()

	mux.HandleFunc(
		"/.well-known/openid-configuration",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 p.server.URL,
				"authorization_endpoint": p.server.URL + "/auth",
				"token_endpoint":         p.server.URL + "/token",
			})
		},
	)

	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p.gotVerifierChallenge = q.Get("code_challenge")

		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "expected S256 PKCE", http.StatusBadRequest)

			return
		}

		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)

			return
		}

		rq := redirect.Query()
		rq.Set("code", "auth-code-123")
		rq.Set("state", q.Get("state"))
		redirect.RawQuery = rq.Encode()

		http.Redirect(w, r, redirect.String(), http.StatusFound)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		if r.Form.Get("grant_type") == "refresh_token" {
			p.refreshes++

			if p.rejectRefresh {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": "invalid_grant",
				})

				return
			}

			body := map[string]any{
				"access_token": "refreshed-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}
			if !p.omitIDTokenOnRefresh {
				body["id_token"] = p.issuedIDToken
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)

			return
		}

		p.gotCode = r.Form.Get("code")

		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "missing code_verifier", http.StatusBadRequest)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access-token-value",
			"id_token":      p.issuedIDToken,
			"refresh_token": "refresh-token-value",
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	})

	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)

	return p
}

// useFakeBrowser replaces the browser launcher with an HTTP client that
// follows the authorization redirect back to the loopback listener.
func useFakeBrowser(t *testing.T) {
	t.Helper()

	original := openURL
	t.Cleanup(func() { openURL = original })

	openURL = func(ctx context.Context, target string) error {
		req, err := http.NewRequestWithContext(
			ctx, http.MethodGet, target, nil,
		)
		if err != nil {
			return err
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		return resp.Body.Close()
	}
}

func TestOIDCLoginAttachesBearerToken(t *testing.T) {
	provider := newFakeProvider(t)
	useFakeBrowser(t)

	cfg := OIDCConfig{
		Issuer:    provider.server.URL,
		ClientID:  "test-client",
		Scopes:    []string{"openid"},
		CachePath: filepath.Join(t.TempDir(), "token.json"),
	}

	client, err := NewOIDCHTTPClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	var gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		},
	))
	defer upstream.Close()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, upstream.URL, nil,
	)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through OIDC client: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if want := "Bearer access-token-value"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}

	if provider.gotVerifierChallenge == "" {
		t.Error("provider received no PKCE code_challenge")
	}

	if provider.gotCode != "auth-code-123" {
		t.Errorf("exchanged code = %q, want auth-code-123", provider.gotCode)
	}
}

func TestOIDCUseIDTokenSendsIDToken(t *testing.T) {
	provider := newFakeProvider(t)
	useFakeBrowser(t)

	cfg := OIDCConfig{
		Issuer:     provider.server.URL,
		ClientID:   "test-client",
		UseIDToken: true,
		CachePath:  filepath.Join(t.TempDir(), "token.json"),
	}

	client, err := NewOIDCHTTPClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	var gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
		},
	))
	defer upstream.Close()

	req, _ := http.NewRequestWithContext(
		t.Context(), http.MethodGet, upstream.URL, nil,
	)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through OIDC client: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if want := "Bearer id-token-value"; gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

// A second start must reuse the cached token instead of opening a browser.
func TestOIDCReusesCachedToken(t *testing.T) {
	provider := newFakeProvider(t)
	useFakeBrowser(t)

	cachePath := filepath.Join(t.TempDir(), "token.json")
	cfg := OIDCConfig{
		Issuer:    provider.server.URL,
		ClientID:  "test-client",
		CachePath: cachePath,
	}

	first, err := NewOIDCHTTPClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("first login: %v", err)
	}

	mustGet(t, first)

	openURL = func(context.Context, string) error {
		t.Error("browser opened despite a valid cached token")

		return nil
	}

	second, err := NewOIDCHTTPClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("second client: %v", err)
	}

	mustGet(t, second)
}

// mustGet drives one request through the client against a throwaway upstream.
func mustGet(t *testing.T, client *http.Client) string {
	t.Helper()

	var gotAuth string

	upstream := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
		},
	))
	defer upstream.Close()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodGet, upstream.URL, nil,
	)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request through OIDC client: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	return gotAuth
}

// seedExpiredToken writes a cache entry whose access token has already expired.
func seedExpiredToken(t *testing.T, path, idToken string) {
	t.Helper()

	token := (&oauth2.Token{
		AccessToken:  "stale-access-token",
		RefreshToken: "refresh-token-value",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}).WithExtra(map[string]any{"id_token": idToken})

	if err := storeToken(path, token); err != nil {
		t.Fatalf("seeding token cache: %v", err)
	}
}

// Login must not happen until a request needs it, or a stdio server would
// block before answering the MCP handshake.
func TestOIDCLoginIsDeferredUntilFirstRequest(t *testing.T) {
	provider := newFakeProvider(t)

	opened := false
	original := openURL
	t.Cleanup(func() { openURL = original })
	openURL = func(ctx context.Context, target string) error {
		opened = true

		return original(ctx, target)
	}

	cfg := OIDCConfig{
		Issuer:    provider.server.URL,
		ClientID:  "test-client",
		CachePath: filepath.Join(t.TempDir(), "token.json"),
	}

	client, err := NewOIDCHTTPClient(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	if opened {
		t.Fatal("browser opened before any request was made")
	}

	useFakeBrowser(t)

	if got := mustGet(t, client); got == "" {
		t.Error("no Authorization header after the deferred login")
	}
}

// A refresh token the provider no longer accepts must trigger a fresh login
// rather than failing forever.
func TestOIDCReLoginsWhenRefreshRejected(t *testing.T) {
	provider := newFakeProvider(t)
	provider.rejectRefresh = true
	useFakeBrowser(t)

	cachePath := filepath.Join(t.TempDir(), "token.json")
	seedExpiredToken(t, cachePath, "id-token-value")

	client, err := NewOIDCHTTPClient(t.Context(), OIDCConfig{
		Issuer:    provider.server.URL,
		ClientID:  "test-client",
		CachePath: cachePath,
	})
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	if provider.refreshes != 0 {
		t.Fatalf("refresh attempted too early")
	}

	if want, got := "Bearer access-token-value", mustGet(t, client); got != want {
		t.Errorf("Authorization = %q, want %q (re-login did not happen)", got, want)
	}
}

// A refresh response without an ID token must not break --use-id-token.
func TestOIDCKeepsIDTokenAcrossRefresh(t *testing.T) {
	provider := newFakeProvider(t)
	provider.omitIDTokenOnRefresh = true
	useFakeBrowser(t)

	cachePath := filepath.Join(t.TempDir(), "token.json")
	seedExpiredToken(t, cachePath, "cached-id-token")

	client, err := NewOIDCHTTPClient(t.Context(), OIDCConfig{
		Issuer:     provider.server.URL,
		ClientID:   "test-client",
		UseIDToken: true,
		CachePath:  cachePath,
	})
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	if want, got := "Bearer cached-id-token", mustGet(t, client); got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}

	if provider.refreshes == 0 {
		t.Error("expected the expired token to be refreshed")
	}
}

// Concurrent tool calls share one token manager; run under -race.
func TestOIDCConcurrentRequests(t *testing.T) {
	provider := newFakeProvider(t)
	useFakeBrowser(t)

	client, err := NewOIDCHTTPClient(t.Context(), OIDCConfig{
		Issuer:    provider.server.URL,
		ClientID:  "test-client",
		CachePath: filepath.Join(t.TempDir(), "token.json"),
	})
	if err != nil {
		t.Fatalf("NewOIDCHTTPClient: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(
		func(_ http.ResponseWriter, _ *http.Request) {},
	))
	defer upstream.Close()

	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			req, err := http.NewRequestWithContext(
				t.Context(), http.MethodGet, upstream.URL, nil,
			)
			if err != nil {
				t.Error(err)

				return
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Error(err)

				return
			}

			_ = resp.Body.Close()
		}()
	}

	wg.Wait()
}

// A repeated callback must not block the handler goroutine.
func TestCallbackIgnoresRepeatDelivery(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(callbackHandler("s", results))

	defer srv.Close()

	for range 3 {
		req, _ := http.NewRequestWithContext(
			t.Context(), http.MethodGet, srv.URL+"/callback?code=abc&state=s", nil,
		)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("callback request: %v", err)
		}

		_ = resp.Body.Close()
	}

	if res := <-results; res.code != "abc" {
		t.Errorf("code = %q, want abc", res.code)
	}
}

func TestCallbackRejectsStateMismatch(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(callbackHandler("expected-state", results))

	defer srv.Close()

	req, _ := http.NewRequestWithContext(
		t.Context(), http.MethodGet,
		srv.URL+"/callback?code=abc&state=forged", nil,
	)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("callback request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}

	res := <-results
	if res.err == nil {
		t.Fatal("expected a state mismatch error")
	}

	if res.code != "" {
		t.Errorf("code leaked on mismatch: %q", res.code)
	}
}

func TestTokenCacheRoundTripsIDToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")

	token := (&oauth2.Token{
		AccessToken:  "a",
		RefreshToken: "r",
		TokenType:    "Bearer",
	}).WithExtra(map[string]any{"id_token": "i"})

	if err := storeToken(path, token); err != nil {
		t.Fatalf("storeToken: %v", err)
	}

	loaded, err := cachedToken(path)
	if err != nil {
		t.Fatalf("cachedToken: %v", err)
	}

	if loaded.AccessToken != "a" {
		t.Errorf("AccessToken = %q, want a", loaded.AccessToken)
	}

	if got, _ := loaded.Extra("id_token").(string); got != "i" {
		t.Errorf("id_token = %q, want i", got)
	}
}

func TestCachedTokenMissingFile(t *testing.T) {
	_, err := cachedToken(filepath.Join(t.TempDir(), "absent.json"))
	if !errors.Is(err, errNoCachedToken) {
		t.Errorf("err = %v, want errNoCachedToken", err)
	}
}
