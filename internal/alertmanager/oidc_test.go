package alertmanager

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"golang.org/x/oauth2"
)

// fakeProvider is a minimal OIDC provider: discovery, an authorization
// endpoint that redirects straight back, and a token endpoint.
type fakeProvider struct {
	server *httptest.Server

	gotVerifierChallenge string
	gotCode              string
	issuedIDToken        string
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

	if provider.gotVerifierChallenge == "" {
		t.Error("provider received no PKCE code_challenge")
	}

	if provider.gotCode != "auth-code-123" {
		t.Errorf("exchanged code = %q, want auth-code-123", provider.gotCode)
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

	if _, err := NewOIDCHTTPClient(t.Context(), cfg); err != nil {
		t.Fatalf("first login: %v", err)
	}

	openURL = func(context.Context, string) error {
		t.Error("browser opened despite a valid cached token")

		return nil
	}

	if _, err := NewOIDCHTTPClient(t.Context(), cfg); err != nil {
		t.Fatalf("second login: %v", err)
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
