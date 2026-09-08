package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestCreateOrMigrateTables_NewDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed on fresh db: %v", err)
	}

	// Verify guild_id exists on corp_to_role
	_, err = database.Exec("INSERT INTO corp_to_role (guild_id, corp_id, role_id) VALUES (?, ?, ?)", "guild1", 123, "role1")
	if err != nil {
		t.Fatalf("failed to insert into migrated corp_to_role: %v", err)
	}
}

func TestCreateOrMigrateTables_LegacyDB(t *testing.T) {
	t.Setenv("DISCORD_GUILD", "123456789")

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	// Create legacy schema
	_, err = database.Exec(`
		CREATE TABLE corp_to_role (corp_id INTEGER PRIMARY KEY, role_id TEXT);
		CREATE TABLE config (key TEXT PRIMARY KEY, value TEXT);
		INSERT INTO corp_to_role (corp_id, role_id) VALUES (999, 'role_legacy');
		INSERT INTO config (key, value) VALUES ('log_channel', '112233');
	`)
	if err != nil {
		t.Fatalf("failed to setup legacy schema: %v", err)
	}

	// Run migration
	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Check migrated data attached to previous DISCORD_GUILD
	var roleID string
	err = database.QueryRow("SELECT role_id FROM corp_to_role WHERE guild_id = '123456789' AND corp_id = 999").Scan(&roleID)
	if err != nil || roleID != "role_legacy" {
		t.Fatalf("expected legacy corp role preserved with guild_id, got %q (err: %v)", roleID, err)
	}

	var logChan string
	err = database.QueryRow("SELECT value FROM config WHERE guild_id = '123456789' AND key = 'log_channel'").Scan(&logChan)
	if err != nil || logChan != "112233" {
		t.Fatalf("expected legacy config preserved with guild_id, got %q (err: %v)", logChan, err)
	}
}

func TestGuildIntegrationsAndMailState(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_integrations.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	// Test GuildIntegration CRUD
	guildID := "guild_123"
	integ, err := GetGuildIntegration(guildID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected empty integration, got %+v", integ)
	}

	err = SetGuildEveAuth(guildID, 98765, "refresh_tok_abc")
	if err != nil {
		t.Fatalf("failed to set eve auth: %v", err)
	}

	err = SetGuildSeatConfig(guildID, "https://seat.example.com", "seat_key_xyz")
	if err != nil {
		t.Fatalf("failed to set seat config: %v", err)
	}

	integ, err = GetGuildIntegration(guildID)
	if err != nil {
		t.Fatalf("failed to get guild integration: %v", err)
	}
	if integ.EveCharacterID != 98765 || integ.EveRefreshToken != "refresh_tok_abc" || integ.SeatURL != "https://seat.example.com" || integ.SeatAPIKey != "seat_key_xyz" {
		t.Fatalf("integration mismatch: %+v", integ)
	}

	all, err := GetAllGuildIntegrations()
	if err != nil || len(all) != 1 {
		t.Fatalf("expected 1 integration, got %d (err: %v)", len(all), err)
	}

	err = DeleteGuildEveAuth(guildID)
	if err != nil {
		t.Fatalf("failed to delete eve auth: %v", err)
	}

	integ, err = GetGuildIntegration(guildID)
	if err != nil || integ.EveCharacterID != 0 || integ.EveRefreshToken != "" {
		t.Fatalf("expected eve auth cleared, got %+v", integ)
	}

	// Test MailState CRUD
	mailState, err := GetMailState(guildID)
	if err != nil || mailState != 0 {
		t.Fatalf("expected initial 0 mail state, got %d (err: %v)", mailState, err)
	}

	err = SetMailState(guildID, 54321)
	if err != nil {
		t.Fatalf("failed to set mail state: %v", err)
	}

	mailState, err = GetMailState(guildID)
	if err != nil || mailState != 54321 {
		t.Fatalf("expected mail state 54321, got %d (err: %v)", mailState, err)
	}
}

func TestDonationsDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_donations.db")

	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer database.Close()

	if err := createOrMigrateTables(database); err != nil {
		t.Fatalf("createOrMigrateTables failed: %v", err)
	}

	oldDB := DB
	DB = database
	defer func() { DB = oldDB }()

	guildID := "guild_donations_test"

	// 1. Test GuildDonationSettings
	settings, err := GetGuildDonationSettings(guildID)
	if err != nil {
		t.Fatalf("unexpected error getting settings: %v", err)
	}
	if settings != nil {
		t.Fatalf("expected nil settings for unset guild, got %+v", settings)
	}

	chanID := "chan_123"
	if err := SetGuildDonationChannel(guildID, &chanID); err != nil {
		t.Fatalf("failed to set donation channel: %v", err)
	}

	settings, err = GetGuildDonationSettings(guildID)
	if err != nil || settings == nil || settings.ChannelID == nil || *settings.ChannelID != "chan_123" {
		t.Fatalf("expected chan_123, got %+v", settings)
	}

	if err := SetGuildDonationChannel(guildID, nil); err != nil {
		t.Fatalf("failed to disable donation channel: %v", err)
	}

	settings, err = GetGuildDonationSettings(guildID)
	if err != nil || settings != nil {
		t.Fatalf("expected nil settings after disable, got %+v", settings)
	}

	// 2. Test TrackedCorporations
	roleID := "role_corp_1"
	eveCorpID := 98000001
	if err := AddTrackedCorporation(guildID, roleID, eveCorpID); err != nil {
		t.Fatalf("failed to add tracked corp: %v", err)
	}

	tc, err := GetTrackedCorporation(guildID, roleID)
	if err != nil || tc == nil || tc.EveCorpID != eveCorpID {
		t.Fatalf("expected tracked corp with id %d, got %+v", eveCorpID, tc)
	}

	corps, err := GetTrackedCorporationsForGuild(guildID)
	if err != nil || len(corps) != 1 || corps[0].EveCorpID != eveCorpID {
		t.Fatalf("expected 1 tracked corp, got %+v", corps)
	}

	uniqueCorps, err := GetAllUniqueTrackedCorpIDs()
	if err != nil || len(uniqueCorps) != 1 || uniqueCorps[0] != eveCorpID {
		t.Fatalf("expected 1 unique corp id, got %+v", uniqueCorps)
	}

	guilds, err := GetGuildsTrackingCorp(eveCorpID)
	if err != nil || len(guilds) != 1 || guilds[0] != guildID {
		t.Fatalf("expected guildID, got %+v", guilds)
	}

	// 3. Test DonationRecord & Leaderboard
	exists, err := DonationRecordExists(111)
	if err != nil || exists {
		t.Fatalf("expected false, got %v (err: %v)", exists, err)
	}

	rec1 := DonationRecord{
		TransactionID:  111,
		ReceiverCorpID: eveCorpID,
		DonorID:        1001,
		DonorType:      "character",
		Amount:         5000000,
	}
	if err := InsertDonationRecord(rec1); err != nil {
		t.Fatalf("failed to insert donation record 1: %v", err)
	}

	exists, err = DonationRecordExists(111)
	if err != nil || !exists {
		t.Fatalf("expected true, got %v", exists)
	}

	rec2 := DonationRecord{
		TransactionID:  112,
		ReceiverCorpID: eveCorpID,
		DonorID:        1001,
		DonorType:      "character",
		Amount:         10000000,
	}
	if err := InsertDonationRecord(rec2); err != nil {
		t.Fatalf("failed to insert donation record 2: %v", err)
	}

	rec3 := DonationRecord{
		TransactionID:  113,
		ReceiverCorpID: eveCorpID,
		DonorID:        2001,
		DonorType:      "corporation",
		Amount:         20000000,
	}
	if err := InsertDonationRecord(rec3); err != nil {
		t.Fatalf("failed to insert donation record 3: %v", err)
	}

	leaderboard, err := GetDonationsLeaderboard(guildID, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboard: %v", err)
	}
	if len(leaderboard) != 2 {
		t.Fatalf("expected 2 leaderboard entries, got %d", len(leaderboard))
	}
	if leaderboard[0].DonorID != 2001 || leaderboard[0].TotalAmount != 20000000 || leaderboard[0].DonationCount != 1 {
		t.Fatalf("unexpected rank 1: %+v", leaderboard[0])
	}
	if leaderboard[1].DonorID != 1001 || leaderboard[1].TotalAmount != 15000000 || leaderboard[1].DonationCount != 2 {
		t.Fatalf("unexpected rank 2: %+v", leaderboard[1])
	}

	// 4. Test RemoveTrackedCorporation
	removed, err := RemoveTrackedCorporation(guildID, roleID)
	if err != nil || !removed {
		t.Fatalf("expected removed=true, got %v", removed)
	}

	tc, err = GetTrackedCorporation(guildID, roleID)
	if err != nil || tc != nil {
		t.Fatalf("expected nil after removal, got %+v", tc)
	}
}

