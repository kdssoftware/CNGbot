package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "github.com/mattn/go-sqlite3"
)

var (
	DB         *sql.DB
	queueMutex sync.Mutex
	cmdQueue   []QueuedCommand
)

type QueuedCommand struct {
	Session   *discordgo.Session
	GuildID   string
	ChannelID string
	Execute   func() error
}

func InitDB() error {
	var err error
	DB, err = sql.Open("sqlite3", "./data.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("error opening database: %w", err)
	}

	DB.SetMaxOpenConns(1)

	if err := createOrMigrateTables(DB); err != nil {
		return fmt.Errorf("error migrating tables: %w", err)
	}

	return nil
}

func createOrMigrateTables(database *sql.DB) error {
	// 1. Ensure global tables exist
	_, err := database.Exec(`
		CREATE TABLE IF NOT EXISTS character_to_discord (
			eve_id INTEGER PRIMARY KEY,
			discord_id TEXT
		);
		CREATE TABLE IF NOT EXISTS guild_integrations (
			guild_id TEXT PRIMARY KEY,
			eve_character_id INTEGER NOT NULL DEFAULT 0,
			eve_refresh_token TEXT NOT NULL DEFAULT '',
			seat_url TEXT NOT NULL DEFAULT '',
			seat_api_key TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS mail_state (
			guild_id TEXT PRIMARY KEY,
			last_mail_id INTEGER NOT NULL DEFAULT 0
		);
	`)
	if err != nil {
		return fmt.Errorf("error creating base tables: %w", err)
	}

	// 2. Define all guild-scoped tables
	type tableDef struct {
		name      string
		schema    string
		colSelect string
	}

	guildTables := []tableDef{
		{
			name:      "corp_to_role",
			schema:    "CREATE TABLE corp_to_role (guild_id TEXT NOT NULL DEFAULT '', corp_id INTEGER NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, corp_id));",
			colSelect: "corp_id, role_id",
		},
		{
			name:      "alliance_to_role",
			schema:    "CREATE TABLE alliance_to_role (guild_id TEXT NOT NULL DEFAULT '', alliance_id INTEGER NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, alliance_id));",
			colSelect: "alliance_id, role_id",
		},
		{
			name:      "config",
			schema:    "CREATE TABLE config (guild_id TEXT NOT NULL DEFAULT '', key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (guild_id, key));",
			colSelect: "key, value",
		},
		{
			name:      "excluded_users",
			schema:    "CREATE TABLE excluded_users (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id",
		},
		{
			name:      "admin_roles",
			schema:    "CREATE TABLE admin_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "auto_map_attempts",
			schema:    "CREATE TABLE auto_map_attempts (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, search_name TEXT, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, search_name",
		},
		{
			name:      "excluded_mappings",
			schema:    "CREATE TABLE excluded_mappings (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id",
		},
		{
			name:      "greet_on_role",
			schema:    "CREATE TABLE greet_on_role (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "standing_to_role",
			schema:    "CREATE TABLE standing_to_role (guild_id TEXT NOT NULL DEFAULT '', standing_type TEXT NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (guild_id, standing_type));",
			colSelect: "standing_type, role_id",
		},
		{
			name:      "excluded_corp_standings",
			schema:    "CREATE TABLE excluded_corp_standings (guild_id TEXT NOT NULL DEFAULT '', corp_id INTEGER NOT NULL, PRIMARY KEY (guild_id, corp_id));",
			colSelect: "corp_id",
		},
		{
			name:      "pending_2fa",
			schema:    "CREATE TABLE pending_2fa (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, eve_id INTEGER, char_name TEXT, code TEXT, expires_at DATETIME, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, eve_id, char_name, code, expires_at",
		},
		{
			name:      "excluded_2fa_roles",
			schema:    "CREATE TABLE excluded_2fa_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "verified_2fa_users",
			schema:    "CREATE TABLE verified_2fa_users (guild_id TEXT NOT NULL DEFAULT '', discord_id TEXT NOT NULL, verified_at DATETIME, PRIMARY KEY (guild_id, discord_id));",
			colSelect: "discord_id, verified_at",
		},
		{
			name:      "seat_needed_roles",
			schema:    "CREATE TABLE seat_needed_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
		{
			name:      "alliance_required_corp_roles",
			schema:    "CREATE TABLE alliance_required_corp_roles (guild_id TEXT NOT NULL DEFAULT '', role_id TEXT NOT NULL, PRIMARY KEY (guild_id, role_id));",
			colSelect: "role_id",
		},
	}

	for _, t := range guildTables {
		if err := ensureGuildTable(database, t.name, t.schema, t.colSelect); err != nil {
			return fmt.Errorf("error migrating table %s: %w", t.name, err)
		}
	}

	return nil
}

func ensureGuildTable(database *sql.DB, tableName, schema, cols string) error {
	// Check if table exists
	var count int
	err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		_, err := database.Exec(schema)
		return err
	}

	// Check if guild_id column exists
	hasGuildID := false
	rows, err := database.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk) == nil {
			if strings.EqualFold(name, "guild_id") {
				hasGuildID = true
			}
		}
	}
	_ = rows.Close()

	if hasGuildID {
		return nil
	}

	// Migrate old table to new schema with guild_id
	log.Printf("Migrating table %s to include guild_id...", tableName)
	tempName := "old_" + tableName
	_, err = database.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s;", tableName, tempName))
	if err != nil {
		return fmt.Errorf("failed to rename %s: %w", tableName, err)
	}

	_, err = database.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create new %s: %w", tableName, err)
	}

	// If DISCORD_GUILD env var was set previously, preserve it as default guild for migrated data
	fallbackGuildID := strings.TrimSpace(os.Getenv("DISCORD_GUILD"))
	copySQL := fmt.Sprintf("INSERT INTO %s (guild_id, %s) SELECT ?, %s FROM %s;", tableName, cols, cols, tempName)
	_, err = database.Exec(copySQL, fallbackGuildID)
	if err != nil {
		return fmt.Errorf("failed to copy data for %s: %w", tableName, err)
	}

	_, err = database.Exec(fmt.Sprintf("DROP TABLE %s;", tempName))
	if err != nil {
		return fmt.Errorf("failed to drop %s: %w", tempName, err)
	}

	return nil
}

func ExecuteOrQueue(s *discordgo.Session, channelID string, f func() error) {
	err := f()
	if err != nil && IsLockedError(err) {
		if channelID != "" {
			_, _ = s.ChannelMessageSend(channelID, "Currently busy with something else. Your command has been added to the queue")
		}
		queueMutex.Lock()
		cmdQueue = append(cmdQueue, QueuedCommand{
			Session:   s,
			ChannelID: channelID,
			Execute:   f,
		})
		queueMutex.Unlock()
		return
	}
	if err != nil {
		if channelID != "" {
			_, _ = s.ChannelMessageSend(channelID, fmt.Sprintf("Error executing command: %v", err))
		}
	}
}

func IsLockedError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "busy")
}

func ProcessQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lockStartTime time.Time

	for range ticker.C {
		queueMutex.Lock()
		if len(cmdQueue) == 0 {
			lockStartTime = time.Time{}
			queueMutex.Unlock()
			continue
		}

		task := cmdQueue[0]
		queueMutex.Unlock()

		err := task.Execute()
		if err != nil {
			if IsLockedError(err) {
				if lockStartTime.IsZero() {
					lockStartTime = time.Now()
				} else if time.Since(lockStartTime) >= 5*time.Minute {
					msg := "Database has been locked for 5+ minutes. Forcibly breaking the database lock..."
					DiscordLog(task.Session, task.GuildID, msg)

					_ = DB.Close()

					var errOpen error
					DB, errOpen = sql.Open("sqlite3", "./data.db")
					if errOpen != nil {
						log.Printf("Fatal: failed to reopen DB after breaking lock: %v", errOpen)
					}

					lockStartTime = time.Time{}
				}
				log.Println("Database is still locked, keeping command in queue...")
				continue
			}
			if task.ChannelID != "" {
				_, _ = task.Session.ChannelMessageSend(task.ChannelID, fmt.Sprintf("Error executing queued command: %v", err))
			}
		}

		queueMutex.Lock()
		cmdQueue = cmdQueue[1:]
		queueMutex.Unlock()
		lockStartTime = time.Time{}
	}
}

func DiscordLog(dg *discordgo.Session, guildID string, msg string) {
	log.Println(msg)
	if dg == nil {
		return
	}

	if guildID != "" {
		var val string
		err := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", guildID).Scan(&val)
		if err == sql.ErrNoRows || val == "1" {
			var logChannel string
			errChannel := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'log_channel'", guildID).Scan(&logChannel)
			if errChannel == nil && logChannel != "" {
				_, _ = dg.ChannelMessageSend(logChannel, msg)
			}
		}
		return
	}

	// If guildID is empty, broadcast to all configured log channels
	rows, err := DB.Query("SELECT guild_id, value FROM config WHERE key = 'log_channel'")
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()

	type guildLog struct {
		guildID string
		channel string
	}
	var targets []guildLog
	for rows.Next() {
		var gid, ch string
		if rows.Scan(&gid, &ch) == nil && ch != "" {
			targets = append(targets, guildLog{guildID: gid, channel: ch})
		}
	}

	for _, t := range targets {
		var val string
		err := DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'post_logs'", t.guildID).Scan(&val)
		if err == sql.ErrNoRows || val == "1" {
			_, _ = dg.ChannelMessageSend(t.channel, msg)
		}
	}
}

type GuildIntegration struct {
	GuildID         string `json:"guild_id"`
	EveCharacterID  int    `json:"eve_character_id"`
	EveRefreshToken string `json:"eve_refresh_token"`
	SeatURL         string `json:"seat_url"`
	SeatAPIKey      string `json:"seat_api_key"`
}

func GetGuildIntegration(guildID string) (GuildIntegration, error) {
	var g GuildIntegration
	g.GuildID = guildID
	if DB == nil {
		return g, fmt.Errorf("database not initialized")
	}
	err := DB.QueryRow("SELECT eve_character_id, eve_refresh_token, seat_url, seat_api_key FROM guild_integrations WHERE guild_id = ?", guildID).
		Scan(&g.EveCharacterID, &g.EveRefreshToken, &g.SeatURL, &g.SeatAPIKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return g, nil
		}
		return g, err
	}
	return g, nil
}

func GetAllGuildIntegrations() ([]GuildIntegration, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query("SELECT guild_id, eve_character_id, eve_refresh_token, seat_url, seat_api_key FROM guild_integrations")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []GuildIntegration
	for rows.Next() {
		var g GuildIntegration
		if err := rows.Scan(&g.GuildID, &g.EveCharacterID, &g.EveRefreshToken, &g.SeatURL, &g.SeatAPIKey); err == nil {
			results = append(results, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func SetGuildIntegration(g GuildIntegration) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, eve_character_id, eve_refresh_token, seat_url, seat_api_key)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			eve_character_id = excluded.eve_character_id,
			eve_refresh_token = excluded.eve_refresh_token,
			seat_url = excluded.seat_url,
			seat_api_key = excluded.seat_api_key
	`, g.GuildID, g.EveCharacterID, g.EveRefreshToken, g.SeatURL, g.SeatAPIKey)
	return err
}

func SetGuildEveAuth(guildID string, characterID int, refreshToken string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, eve_character_id, eve_refresh_token)
		VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			eve_character_id = excluded.eve_character_id,
			eve_refresh_token = excluded.eve_refresh_token
	`, guildID, characterID, refreshToken)
	return err
}

func DeleteGuildEveAuth(guildID string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		UPDATE guild_integrations SET eve_character_id = 0, eve_refresh_token = '' WHERE guild_id = ?
	`, guildID)
	return err
}

func SetGuildSeatConfig(guildID string, seatURL, seatAPIKey string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO guild_integrations (guild_id, seat_url, seat_api_key)
		VALUES (?, ?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			seat_url = excluded.seat_url,
			seat_api_key = excluded.seat_api_key
	`, guildID, seatURL, seatAPIKey)
	return err
}

func GetMailState(guildID string) (int, error) {
	if DB == nil {
		return 0, fmt.Errorf("database not initialized")
	}
	var lastID int
	err := DB.QueryRow("SELECT last_mail_id FROM mail_state WHERE guild_id = ?", guildID).Scan(&lastID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return lastID, nil
}

func SetMailState(guildID string, lastMailID int) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`
		INSERT INTO mail_state (guild_id, last_mail_id)
		VALUES (?, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			last_mail_id = excluded.last_mail_id
	`, guildID, lastMailID)
	return err
}

