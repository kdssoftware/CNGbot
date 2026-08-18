package config

import (
	"bufio"
	"os"
	"strings"
)

func init() {
	LoadEnvFile(".env")
}

func LoadEnvFile(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
	}
	_ = scanner.Err()
}

func GetDiscordToken() string {
	return strings.TrimSpace(os.Getenv("DISCORD_TOKEN"))
}

func GetCharacterID() string {
	if val := strings.TrimSpace(os.Getenv("CHARACTER_ID")); val != "" {
		return val
	}
	return strings.TrimSpace(os.Getenv("EVE_CHARACTER_ID"))
}

func GetEveClientID() string {
	return strings.TrimSpace(os.Getenv("EVE_CLIENT_ID"))
}

func GetEveSecret() string {
	if val := strings.TrimSpace(os.Getenv("EVE_SECRET")); val != "" {
		return val
	}
	return strings.TrimSpace(os.Getenv("EVE_CLIENT_SECRET"))
}

func GetRefreshToken() string {
	if val := strings.TrimSpace(os.Getenv("REFRESH_TOKEN")); val != "" {
		return val
	}
	return strings.TrimSpace(os.Getenv("EVE_REFRESH_TOKEN"))
}

func GetSeatURL() string {
	url := os.Getenv("SEAT_URL")
	return strings.TrimRight(strings.TrimSpace(url), "/")
}

func GetSeatAPIKey() string {
	if key := os.Getenv("SEAT_API"); strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key)
	}
	return strings.TrimSpace(os.Getenv("SEAT_API_KEY"))
}

func GetDiscordClientID() string {
	return strings.TrimSpace(os.Getenv("DISCORD_CLIENT_ID"))
}

func GetDiscordClientSecret() string {
	return strings.TrimSpace(os.Getenv("DISCORD_CLIENT_SECRET"))
}

func GetDiscordOAuthRedirect() string {
	if redirect := strings.TrimSpace(os.Getenv("DISCORD_OAUTH_REDIRECT")); redirect != "" {
		return redirect
	}
	port := GetPort()
	return "http://localhost:" + port + "/auth/discord/callback"
}

func GetEveCallbackURL() string {
	if callback := strings.TrimSpace(os.Getenv("EVE_CALLBACK_URL")); callback != "" {
		return callback
	}
	port := GetPort()
	return "http://localhost:" + port + "/auth/eve/callback"
}

func GetPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	return "7887"
}

func GetDashboardURL() string {
	if url := strings.TrimSpace(os.Getenv("DASHBOARD_URL")); url != "" {
		return strings.TrimRight(url, "/")
	}
	if redirect := strings.TrimSpace(os.Getenv("DISCORD_OAUTH_REDIRECT")); redirect != "" {
		if idx := strings.Index(redirect, "/auth/discord/callback"); idx != -1 {
			return strings.TrimRight(redirect[:idx], "/")
		}
	}
	return "http://localhost:" + GetPort()
}

func GetEveMailEmoji() string {
	if emoji := strings.TrimSpace(os.Getenv("EVE_MAIL_EMOJI")); emoji != "" {
		return emoji
	}
	return strings.TrimSpace(os.Getenv("DISCORD_MAIL_EMOJI"))
}

func GetEveCalendarEmoji() string {
	if emoji := strings.TrimSpace(os.Getenv("EVE_CALENDAR_EMOJI")); emoji != "" {
		return emoji
	}
	return strings.TrimSpace(os.Getenv("DISCORD_CALENDAR_EMOJI"))
}


