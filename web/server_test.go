package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"evemaildiscord/db"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_web.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	_, err = database.Exec(`
		CREATE TABLE IF NOT EXISTS guild_integrations (
			guild_id TEXT PRIMARY KEY,
			eve_character_id INTEGER NOT NULL DEFAULT 0,
			eve_refresh_token TEXT NOT NULL DEFAULT '',
			seat_url TEXT NOT NULL DEFAULT '',
			seat_api_key TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS config (
			guild_id TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY (guild_id, key)
		);
	`)
	if err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	db.DB = database
}

func TestHandleIndex_Unauthenticated(t *testing.T) {
	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.HandleIndex(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	body := w.Body.String()
	if !strings.Contains(body, "CNGBot Control Panel") {
		t.Errorf("expected landing page content, got: %s", body)
	}
}

func TestHandleIndex_Authenticated(t *testing.T) {
	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleIndex(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect to /dashboard, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard" {
		t.Fatalf("expected redirect to /dashboard, got %s", loc)
	}
}

func TestHandleDiscordLogin(t *testing.T) {
	os.Setenv("DISCORD_CLIENT_ID", "test_discord_client_id")
	os.Setenv("DISCORD_OAUTH_REDIRECT", "http://localhost:7887/auth/discord/callback")
	defer func() {
		os.Unsetenv("DISCORD_CLIENT_ID")
		os.Unsetenv("DISCORD_OAUTH_REDIRECT")
	}()

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/auth/discord/login", nil)
	w := httptest.NewRecorder()

	handler.HandleDiscordLogin(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}

	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "https://discord.com/api/oauth2/authorize") {
		t.Errorf("expected discord authorize url, got: %s", loc)
	}
	if !strings.Contains(loc, "client_id=test_discord_client_id") {
		t.Errorf("expected client_id in location, got: %s", loc)
	}
}

func TestHandleDashboard_And_GuildConfig(t *testing.T) {
	setupTestDB(t)

	adminGuilds := map[string]DiscordGuildInfo{
		"guild_100": {
			ID:          "guild_100",
			Name:        "Alpha Corp Server",
			HasAdmin:    true,
			Permissions: "8",
		},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)

	// 1. Test Dashboard
	reqDash := httptest.NewRequest("GET", "/dashboard", nil)
	reqDash.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wDash := httptest.NewRecorder()

	handler.HandleDashboard(wDash, reqDash)
	if wDash.Code != http.StatusOK {
		t.Fatalf("expected status 200 on dashboard, got %d", wDash.Code)
	}
	if !strings.Contains(wDash.Body.String(), "Alpha Corp Server") {
		t.Errorf("expected guild name in dashboard body")
	}

	// 2. Test Guild Config with access
	reqConfig := httptest.NewRequest("GET", "/dashboard/guild?id=guild_100", nil)
	reqConfig.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wConfig := httptest.NewRecorder()

	handler.HandleGuildConfig(wConfig, reqConfig)
	if wConfig.Code != http.StatusOK {
		t.Fatalf("expected status 200 on guild config, got %d", wConfig.Code)
	}
	if !strings.Contains(wConfig.Body.String(), "Alpha Corp Server") {
		t.Errorf("expected guild config for Alpha Corp Server")
	}

	// 3. Test Guild Config without access
	reqForbidden := httptest.NewRequest("GET", "/dashboard/guild?id=guild_999", nil)
	reqForbidden.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wForbidden := httptest.NewRecorder()

	handler.HandleGuildConfig(wForbidden, reqForbidden)
	if wForbidden.Code != http.StatusForbidden {
		t.Fatalf("expected status 403 forbidden, got %d", wForbidden.Code)
	}
}

func TestHandleGuildSeatSave_And_EveUnlink(t *testing.T) {
	setupTestDB(t)

	adminGuilds := map[string]DiscordGuildInfo{
		"guild_100": {
			ID:       "guild_100",
			Name:     "Alpha Server",
			HasAdmin: true,
		},
	}

	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", adminGuilds)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)

	// 1. Save SeAT configuration
	seatForm := url.Values{
		"guild_id":     {"guild_100"},
		"seat_url":     {"https://seat.testcorp.com"},
		"seat_api_key": {"super_secret_seat_token"},
	}
	reqSeat := httptest.NewRequest("POST", "/dashboard/guild/seat", strings.NewReader(seatForm.Encode()))
	reqSeat.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSeat.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wSeat := httptest.NewRecorder()

	handler.HandleGuildSeatSave(wSeat, reqSeat)
	if wSeat.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on seat save, got %d", wSeat.Code)
	}

	integ, err := db.GetGuildIntegration("guild_100")
	if err != nil {
		t.Fatalf("failed to get guild integration: %v", err)
	}
	if integ.SeatURL != "https://seat.testcorp.com" || integ.SeatAPIKey != "super_secret_seat_token" {
		t.Fatalf("seat integration mismatch: %+v", integ)
	}

	// 2. Set EVE Auth then test unlink
	err = db.SetGuildEveAuth("guild_100", 991234, "mock_refresh_token")
	if err != nil {
		t.Fatalf("failed to set eve auth: %v", err)
	}

	unlinkForm := url.Values{
		"guild_id": {"guild_100"},
	}
	reqUnlink := httptest.NewRequest("POST", "/dashboard/guild/eve/unlink", strings.NewReader(unlinkForm.Encode()))
	reqUnlink.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqUnlink.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	wUnlink := httptest.NewRecorder()

	handler.HandleGuildEveUnlink(wUnlink, reqUnlink)
	if wUnlink.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on unlink, got %d", wUnlink.Code)
	}

	integ, err = db.GetGuildIntegration("guild_100")
	if err != nil || integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected eve integration cleared, got %+v", integ)
	}
}

func TestExtractEveCharacterFromToken_JWT(t *testing.T) {
	// Generate mock JWT with sub="CHARACTER:EVE:12345678" and name="Test Pilot"
	header := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9"
	payload := "eyJzdWIiOiJDSEFSQUNURVI6RVZFOjEyMzQ1Njc4IiwibmFtZSI6IlRlc3QgUGlsb3QifQ" // {"sub":"CHARACTER:EVE:12345678","name":"Test Pilot"}
	sig := "dummy_signature"
	mockJWT := header + "." + payload + "." + sig

	charID, charName := extractEveCharacterFromToken(mockJWT)
	if charID != 12345678 {
		t.Errorf("expected charID 12345678, got %d", charID)
	}
	if charName != "Test Pilot" {
		t.Errorf("expected charName 'Test Pilot', got %s", charName)
	}
}

func TestHandleLogout(t *testing.T) {
	session, err := Store.CreateSession("user123", "TestUser", "avatar1", "tok", nil)
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	handler := NewWebHandler(nil)
	req := httptest.NewRequest("GET", "/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
	w := httptest.NewRecorder()

	handler.HandleLogout(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", resp.StatusCode)
	}

	// Session should be deleted
	if Store.GetSession(req) != nil {
		t.Errorf("expected session to be deleted from store")
	}
}
