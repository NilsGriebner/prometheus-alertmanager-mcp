package alertmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"golang.org/x/oauth2"
)

// tokenFileMode keeps the cached token readable only by its owner.
const tokenFileMode = 0o600

// errNoCachedToken reports that no token is cached, which is not a failure.
var errNoCachedToken = errors.New("no cached token")

// cachedTokenFile is the on-disk shape of a cached token. The ID token lives
// outside oauth2.Token, so it is stored alongside it.
type cachedTokenFile struct {
	Token   *oauth2.Token `json:"token"`
	IDToken string        `json:"id_token,omitempty"`
}

// DefaultCachePath returns the token cache location for the given issuer and
// client, keyed by a hash so that distinct providers do not collide.
func DefaultCachePath(issuer, clientID string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolving user cache dir: %w", err)
	}

	sum := sha256.Sum256([]byte(issuer + "\x00" + clientID))
	name := hex.EncodeToString(sum[:8]) + ".json"

	return filepath.Join(dir, "alertmanager-mcp", name), nil
}

// cachedToken loads a previously stored token. It returns errNoCachedToken
// when no cache exists or caching is disabled.
func cachedToken(path string) (*oauth2.Token, error) {
	if path == "" {
		return nil, errNoCachedToken
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNoCachedToken
		}

		return nil, fmt.Errorf("reading token cache %s: %w", path, err)
	}

	var file cachedTokenFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decoding token cache %s: %w", path, err)
	}

	if file.Token == nil {
		return nil, fmt.Errorf("token cache %s contains no token", path)
	}

	if file.IDToken != "" {
		return file.Token.WithExtra(
			map[string]any{"id_token": file.IDToken},
		), nil
	}

	return file.Token, nil
}

// storeToken persists a token with owner-only permissions.
func storeToken(path string, token *oauth2.Token) error {
	if path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating token cache dir: %w", err)
	}

	file := cachedTokenFile{Token: token}
	if idToken, ok := token.Extra("id_token").(string); ok {
		file.IDToken = idToken
	}

	data, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("encoding token cache: %w", err)
	}

	// Write via a temporary file so a concurrent reader never observes a
	// half-written cache.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".token-*.json")
	if err != nil {
		return fmt.Errorf("creating temporary token cache: %w", err)
	}

	tmpName := tmp.Name()

	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(tokenFileMode); err != nil {
		return fmt.Errorf("securing temporary token cache: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("writing temporary token cache: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temporary token cache: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replacing token cache %s: %w", path, err)
	}

	return nil
}

// discardToken removes a cached token that the provider no longer accepts, so
// the next start does not retry a session that cannot be revived.
func discardToken(path string) {
	if path == "" {
		return
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn().Err(err).Str("path", path).
			Msg("failed to remove stale token cache")
	}
}
