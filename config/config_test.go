package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	content := `
# Comment
TEST_DISCORD_TOKEN=token123
TEST_CHARACTER_ID="987654321"
TEST_SCOPES=["publicData"]
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test env: %v", err)
	}

	LoadEnvFile(envPath)

	if os.Getenv("TEST_DISCORD_TOKEN") != "token123" {
		t.Errorf("expected token123, got %q", os.Getenv("TEST_DISCORD_TOKEN"))
	}
	if os.Getenv("TEST_CHARACTER_ID") != "987654321" {
		t.Errorf("expected 987654321, got %q", os.Getenv("TEST_CHARACTER_ID"))
	}
	if os.Getenv("TEST_SCOPES") != `["publicData"]` {
		t.Errorf(`expected ["publicData"], got %q`, os.Getenv("TEST_SCOPES"))
	}
}

func TestGetters(t *testing.T) {
	os.Setenv("DISCORD_TOKEN", "bot-token")
	os.Setenv("DISCORD_CLIENT_ID", "discord-client-id")
	os.Setenv("DISCORD_CLIENT_SECRET", "discord-client-secret")
	os.Setenv("DISCORD_OAUTH_REDIRECT", "http://localhost:7887/auth/discord/callback")
	os.Setenv("CHARACTER_ID", "char-id")
	os.Setenv("EVE_CLIENT_ID", "client-id")
	os.Setenv("EVE_SECRET", "secret-key")
	os.Setenv("EVE_CALLBACK_URL", "http://localhost:7887/auth/eve/callback")
	os.Setenv("REFRESH_TOKEN", "refresh-token")
	os.Setenv("PORT", "9090")
	os.Setenv("DASHBOARD_URL", "http://localhost:9090")
	os.Setenv("EVE_MAIL_EMOJI", "1538809742615642172")
	os.Setenv("EVE_CALENDAR_EMOJI", "1538809784416084028")

	defer func() {
		os.Unsetenv("DISCORD_TOKEN")
		os.Unsetenv("DISCORD_CLIENT_ID")
		os.Unsetenv("DISCORD_CLIENT_SECRET")
		os.Unsetenv("DISCORD_OAUTH_REDIRECT")
		os.Unsetenv("CHARACTER_ID")
		os.Unsetenv("EVE_CLIENT_ID")
		os.Unsetenv("EVE_SECRET")
		os.Unsetenv("EVE_CALLBACK_URL")
		os.Unsetenv("REFRESH_TOKEN")
		os.Unsetenv("PORT")
		os.Unsetenv("DASHBOARD_URL")
		os.Unsetenv("EVE_MAIL_EMOJI")
		os.Unsetenv("EVE_CALENDAR_EMOJI")
	}()

	if GetDiscordToken() != "bot-token" {
		t.Errorf("unexpected discord token: %s", GetDiscordToken())
	}
	if GetDiscordClientID() != "discord-client-id" {
		t.Errorf("unexpected discord client id: %s", GetDiscordClientID())
	}
	if GetDiscordClientSecret() != "discord-client-secret" {
		t.Errorf("unexpected discord client secret: %s", GetDiscordClientSecret())
	}
	if GetDiscordOAuthRedirect() != "http://localhost:7887/auth/discord/callback" {
		t.Errorf("unexpected discord oauth redirect: %s", GetDiscordOAuthRedirect())
	}
	if GetCharacterID() != "char-id" {
		t.Errorf("unexpected char id: %s", GetCharacterID())
	}
	if GetEveClientID() != "client-id" {
		t.Errorf("unexpected client id: %s", GetEveClientID())
	}
	if GetEveSecret() != "secret-key" {
		t.Errorf("unexpected secret: %s", GetEveSecret())
	}
	if GetEveCallbackURL() != "http://localhost:7887/auth/eve/callback" {
		t.Errorf("unexpected eve callback url: %s", GetEveCallbackURL())
	}
	if GetRefreshToken() != "refresh-token" {
		t.Errorf("unexpected refresh token: %s", GetRefreshToken())
	}
	if GetPort() != "9090" {
		t.Errorf("unexpected port: %s", GetPort())
	}
	if GetDashboardURL() != "http://localhost:9090" {
		t.Errorf("unexpected dashboard url: %s", GetDashboardURL())
	}
	if GetEveMailEmoji() != "1538809742615642172" {
		t.Errorf("unexpected eve mail emoji: %s", GetEveMailEmoji())
	}
	if GetEveCalendarEmoji() != "1538809784416084028" {
		t.Errorf("unexpected eve calendar emoji: %s", GetEveCalendarEmoji())
	}
}
