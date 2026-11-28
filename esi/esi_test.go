package esi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoggingTransport_Success(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	resp, err := Get(server.URL + "/latest/universe/ids/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", string(body))
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[ESI] GET") || !strings.Contains(logOutput, "200 OK") {
		t.Errorf("expected ESI log with 200 OK, got: %s", logOutput)
	}
}

func TestLoggingTransport_PostSuccess(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":1}]`))
	}))
	defer server.Close()

	resp, err := Post(server.URL+"/latest/universe/ids/", "application/json", strings.NewReader(`["Test"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[ESI] POST") || !strings.Contains(logOutput, "200 OK") {
		t.Errorf("expected ESI log with POST and 200 OK, got: %s", logOutput)
	}
}

func TestLoggingTransport_Timeout(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	// Create test client with a very short timeout for unit test
	testClient := &http.Client{
		Timeout: 50 * time.Millisecond,
		Transport: &LoggingTransport{
			Base: http.DefaultTransport,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, err := testClient.Get(server.URL + "/slow")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[ESI] GET") {
		t.Errorf("expected ESI log on timeout failure, got: %s", logOutput)
	}
}

func TestTimeoutConstant(t *testing.T) {
	if Timeout != 5*time.Second {
		t.Errorf("expected Timeout to be 5s, got %v", Timeout)
	}
	if Client.Timeout != 5*time.Second {
		t.Errorf("expected Client.Timeout to be 5s, got %v", Client.Timeout)
	}
}

func TestGetOAuthHTTPClient_Timeout(t *testing.T) {
	client := GetOAuthHTTPClientForToken("test-refresh-token")
	if client == nil {
		t.Fatal("expected non-nil oauth client")
	}
	if client.Timeout != 5*time.Second {
		t.Errorf("expected client.Timeout to be 5s, got %v", client.Timeout)
	}
}

func TestCachingTransport(t *testing.T) {
	requestsCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestsCount++
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Expires", time.Now().Add(5*time.Minute).UTC().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cached":true}`))
	}))
	defer server.Close()

	resp1, err := Get(server.URL + "/cached-endpoint")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	_, _ = io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()

	resp2, err := Get(server.URL + "/cached-endpoint")
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	_, _ = io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()

	if requestsCount != 1 {
		t.Errorf("expected 1 request to hit the server due to caching, got %d", requestsCount)
	}
}

func makeTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("failed to marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
}

func TestExtractScopesFromJWT(t *testing.T) {
	t.Run("array scp", func(t *testing.T) {
		jwt := makeTestJWT(t, map[string]any{
			"scp": []string{"publicData", "esi-fleets.read_fleet.v1", "esi-fleets.write_fleet.v1"},
		})
		scopes, ok := ExtractScopesFromJWT(jwt)
		if !ok || len(scopes) != 3 {
			t.Fatalf("expected 3 scopes, got ok=%v scopes=%v", ok, scopes)
		}
	})

	t.Run("single string scp", func(t *testing.T) {
		jwt := makeTestJWT(t, map[string]any{
			"scp": "esi-fleets.read_fleet.v1",
		})
		scopes, ok := ExtractScopesFromJWT(jwt)
		if !ok || len(scopes) != 1 || scopes[0] != "esi-fleets.read_fleet.v1" {
			t.Fatalf("expected single scope, got ok=%v scopes=%v", ok, scopes)
		}
	})

	t.Run("missing scp", func(t *testing.T) {
		jwt := makeTestJWT(t, map[string]any{"sub": "CHARACTER:EVE:123"})
		if scopes, ok := ExtractScopesFromJWT(jwt); ok || len(scopes) != 0 {
			t.Fatalf("expected missing scp, got ok=%v scopes=%v", ok, scopes)
		}
	})

	t.Run("malformed jwt", func(t *testing.T) {
		if scopes, ok := ExtractScopesFromJWT("not-a-jwt"); ok || len(scopes) != 0 {
			t.Fatalf("expected malformed jwt to fail, got ok=%v scopes=%v", ok, scopes)
		}
	})
}

func TestMissingScopes(t *testing.T) {
	granted := []string{"publicData", "esi-fleets.read_fleet.v1"}
	required := []string{"esi-fleets.read_fleet.v1", "esi-fleets.write_fleet.v1"}

	missing := MissingScopes(granted, required)
	if len(missing) != 1 || missing[0] != "esi-fleets.write_fleet.v1" {
		t.Fatalf("expected [esi-fleets.write_fleet.v1], got %v", missing)
	}

	if got := MissingScopes(append(granted, "esi-fleets.write_fleet.v1"), required); len(got) != 0 {
		t.Fatalf("expected no missing scopes, got %v", got)
	}
}

func TestGetOAuthHTTPClientForScopes(t *testing.T) {
	t.Setenv("EVE_CLIENT_ID", "test-client")
	t.Setenv("EVE_SECRET", "test-secret")

	t.Run("valid scopes", func(t *testing.T) {
		jwt := makeTestJWT(t, map[string]any{
			"scp": []string{"esi-fleets.read_fleet.v1", "esi-fleets.write_fleet.v1"},
		})
		tokenHits := 0
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenHits++
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  jwt,
				"token_type":    "Bearer",
				"expires_in":    1200,
				"refresh_token": "refreshed-tok",
			})
		}))
		defer tokenSrv.Close()

		origTokenURL := eveTokenURL
		eveTokenURL = tokenSrv.URL
		defer func() { eveTokenURL = origTokenURL }()

		client, err := GetOAuthHTTPClientForScopes("my-refresh-tok", "esi-fleets.read_fleet.v1", "esi-fleets.write_fleet.v1")
		if err != nil {
			t.Fatalf("expected success, got err=%v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}

		var gotAuth string
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer apiSrv.Close()

		resp, err := client.Get(apiSrv.URL + "/test")
		if err != nil {
			t.Fatalf("api call failed: %v", err)
		}
		_ = resp.Body.Close()

		if gotAuth != "Bearer "+jwt {
			t.Errorf("expected Bearer JWT, got %q", gotAuth)
		}
		if tokenHits != 1 {
			t.Errorf("expected token endpoint to be called once, got %d", tokenHits)
		}
	})

	t.Run("missing scope returns MissingScopesError", func(t *testing.T) {
		jwt := makeTestJWT(t, map[string]any{
			"scp": []string{"esi-fleets.read_fleet.v1"},
		})
		tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": jwt,
				"token_type":   "Bearer",
				"expires_in":   1200,
			})
		}))
		defer tokenSrv.Close()

		origTokenURL := eveTokenURL
		eveTokenURL = tokenSrv.URL
		defer func() { eveTokenURL = origTokenURL }()

		client, err := GetOAuthHTTPClientForScopes("my-refresh-tok", "esi-fleets.read_fleet.v1", "esi-fleets.write_fleet.v1")
		if client != nil {
			t.Fatalf("expected nil client on missing scopes, got %v", client)
		}
		var missingErr *MissingScopesError
		if !errors.As(err, &missingErr) {
			t.Fatalf("expected *MissingScopesError, got %T (%v)", err, err)
		}
		if len(missingErr.Missing) != 1 || missingErr.Missing[0] != "esi-fleets.write_fleet.v1" {
			t.Fatalf("unexpected missing scopes: %v", missingErr.Missing)
		}
	})
}
