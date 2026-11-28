package esi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"

	"github.com/gregjones/httpcache"
	"golang.org/x/oauth2"
)

var (
	eveAuthURL  = "https://login.eveonline.com/v2/oauth/authorize"
	eveTokenURL = "https://login.eveonline.com/v2/oauth/token"
)

// Timeout is the maximum duration allowed for any ESI HTTP call (5 seconds).
const Timeout = 5 * time.Second

// CacheMemory is the shared in-memory cache for ESI requests.
var CacheMemory = httpcache.NewMemoryCache()

// NewTransport returns an http.RoundTripper that incorporates RFC 7234 caching and ESI request logging.
func NewTransport() http.RoundTripper {
	loggingTransport := &LoggingTransport{
		Base: http.DefaultTransport,
	}
	cacheTransport := httpcache.NewTransport(CacheMemory)
	cacheTransport.Transport = loggingTransport
	return cacheTransport
}

// LoggingTransport is an http.RoundTripper that enforces timeouts and logs all ESI calls.
type LoggingTransport struct {
	Base http.RoundTripper
}

func (t *LoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	resp, err := base.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		var urlErr *url.Error
		isTimeout := errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(req.Context().Err(), context.DeadlineExceeded) ||
			(errors.As(err, &urlErr) && urlErr.Timeout()) ||
			duration >= Timeout

		if isTimeout {
			log.Printf("[ESI] %s %s STOPPED: request took longer than 5 seconds (%v): %v", req.Method, req.URL.String(), duration, err)
		} else {
			log.Printf("[ESI] %s %s failed after %v: %v", req.Method, req.URL.String(), duration, err)
		}
		return nil, err
	}

	if duration >= Timeout {
		log.Printf("[ESI] %s %s returned %s but took longer than 5 seconds (%v)", req.Method, req.URL.String(), resp.Status, duration)
	} else {
		log.Printf("[ESI] %s %s -> %s (%v)", req.Method, req.URL.String(), resp.Status, duration)
	}

	return resp, nil
}

// Client is a standard HTTP client configured with a 5-second timeout, caching, and logging transport for ESI calls.
var Client = &http.Client{
	Timeout:   Timeout,
	Transport: NewTransport(),
}

// Get sends a GET request to the given ESI URL with a 5-second timeout and logs the request.
func Get(urlStr string) (*http.Response, error) {
	return Client.Get(urlStr)
}

// Post sends a POST request to the given ESI URL with a 5-second timeout and logs the request.
func Post(urlStr string, contentType string, body io.Reader) (*http.Response, error) {
	return Client.Post(urlStr, contentType, body)
}

// MissingScopesError reports that an EVE SSO token does not include all required scopes.
type MissingScopesError struct {
	Missing []string
}

func (e *MissingScopesError) Error() string {
	return fmt.Sprintf("missing required ESI scope(s): %s", strings.Join(e.Missing, ", "))
}

// ExtractScopesFromJWT parses the "scp" claim from an EVE SSO v2 JWT access token.
// EVE encodes "scp" as either a single JSON string or a JSON string array.
func ExtractScopesFromJWT(accessToken string) ([]string, bool) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return nil, false
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}

	var claims struct {
		Scp json.RawMessage `json:"scp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}

	raw := bytes.TrimSpace(claims.Scp)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}

	if raw[0] == '[' {
		var scopes []string
		if err := json.Unmarshal(raw, &scopes); err != nil {
			return nil, false
		}
		return scopes, true
	}

	if raw[0] == '"' {
		var single string
		if err := json.Unmarshal(raw, &single); err != nil {
			return nil, false
		}
		if single == "" {
			return nil, true
		}
		return []string{single}, true
	}

	return nil, false
}

// MissingScopes returns any required scopes not present in granted.
func MissingScopes(granted, required []string) []string {
	var missing []string
	for _, req := range required {
		if !slices.Contains(granted, req) {
			missing = append(missing, req)
		}
	}
	return missing
}

// GetOAuthHTTPClientForScopes refreshes the token once, validates requiredScopes
// against the live JWT "scp" claim, and returns an HTTP client reusing that token.
func GetOAuthHTTPClientForScopes(refreshToken string, requiredScopes ...string) (*http.Client, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("empty refresh token")
	}

	conf := &oauth2.Config{
		ClientID:     config.GetEveClientID(),
		ClientSecret: config.GetEveSecret(),
		Endpoint: oauth2.Endpoint{
			AuthURL:  eveAuthURL,
			TokenURL: eveTokenURL,
		},
	}
	initialToken := &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-1 * time.Hour),
	}

	baseClient := &http.Client{
		Timeout:   Timeout,
		Transport: NewTransport(),
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, baseClient)
	ts := conf.TokenSource(ctx, initialToken)

	tok, err := ts.Token()
	if err != nil {
		return nil, err
	}

	if len(requiredScopes) > 0 {
		granted, _ := ExtractScopesFromJWT(tok.AccessToken)
		if missing := MissingScopes(granted, requiredScopes); len(missing) > 0 {
			return nil, &MissingScopesError{Missing: missing}
		}
	}

	oauthClient := &http.Client{
		Timeout: Timeout,
		Transport: &oauth2.Transport{
			Source: oauth2.ReuseTokenSource(tok, ts),
			Base:   NewTransport(),
		},
	}
	return oauthClient, nil
}

// GetOAuthHTTPClientForToken returns an authenticated OAuth2 HTTP client for a specific refresh token.
func GetOAuthHTTPClientForToken(refreshToken string) *http.Client {
	if refreshToken == "" {
		return nil
	}
	conf := &oauth2.Config{
		ClientID:     config.GetEveClientID(),
		ClientSecret: config.GetEveSecret(),
		Endpoint: oauth2.Endpoint{
			AuthURL:  eveAuthURL,
			TokenURL: eveTokenURL,
		},
	}
	token := &oauth2.Token{
		RefreshToken: refreshToken,
		Expiry:       time.Now().Add(-1 * time.Hour),
	}

	baseClient := &http.Client{
		Timeout:   Timeout,
		Transport: NewTransport(),
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, baseClient)
	oauthClient := conf.Client(ctx, token)
	oauthClient.Timeout = Timeout
	return oauthClient
}

// GetOAuthHTTPClient returns an authenticated OAuth2 HTTP client for the given guildID.
// If guildID is empty, it falls back to the configured global refresh token if available.
func GetOAuthHTTPClient(guildID string) *http.Client {
	var refreshToken string
	if guildID != "" {
		integ, err := db.GetGuildIntegration(guildID)
		if err == nil && integ.EveRefreshToken != "" {
			refreshToken = integ.EveRefreshToken
		}
	}
	if refreshToken == "" {
		refreshToken = config.GetRefreshToken()
	}
	if refreshToken == "" {
		return nil
	}
	return GetOAuthHTTPClientForToken(refreshToken)
}
