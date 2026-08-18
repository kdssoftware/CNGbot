package esi

import (
	"bytes"
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
