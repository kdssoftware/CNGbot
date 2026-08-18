package roles

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"evemaildiscord/config"
	"evemaildiscord/db"
	"evemaildiscord/esi"
	"evemaildiscord/mail"
	"evemaildiscord/seat"

	"github.com/bwmarrin/discordgo"
)

func Generate2FACode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

type EsiCharacterAffiliation struct {
	AllianceID    int `json:"alliance_id"`
	CharacterID   int `json:"character_id"`
	CorporationID int `json:"corporation_id"`
	FactionID     int `json:"faction_id"`
}

type EsiStanding struct {
	ContactID int     `json:"contact_id"`
	FromID    int     `json:"from_id"`
	FromType  string  `json:"from_type"`
	Standing  float64 `json:"standing"`
}

func (s EsiStanding) ID() int {
	if s.ContactID != 0 {
		return s.ContactID
	}
	return s.FromID
}

type BotAffiliation struct {
	CharacterID   string
	CorporationID int
	AllianceID    int
}

const (
	StandingTerrible  = "terrible"
	StandingBad       = "bad"
	StandingNeutral   = "neutral"
	StandingGood      = "good"
	StandingExcellent = "excellent"
)

var AllStandingTypes = []string{
	StandingTerrible,
	StandingBad,
	StandingNeutral,
	StandingGood,
	StandingExcellent,
}

func GetOAuthHTTPClient(guildID string) *http.Client {
	return esi.GetOAuthHTTPClient(guildID)
}

func FetchBotAffiliation(charIDStr string) (BotAffiliation, error) {
	if charIDStr == "" || charIDStr == "0" {
		charIDStr = config.GetCharacterID()
	}
	bot := BotAffiliation{CharacterID: charIDStr}
	if charIDStr == "" || charIDStr == "0" {
		return bot, fmt.Errorf("no character ID configured")
	}
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/latest/characters/%s/", charIDStr))
	if err != nil {
		return bot, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return bot, fmt.Errorf("ESI character lookup returned status %d", resp.StatusCode)
	}

	var charData struct {
		CorporationID int `json:"corporation_id"`
		AllianceID    int `json:"alliance_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&charData); err != nil {
		return bot, err
	}
	bot.CorporationID = charData.CorporationID
	bot.AllianceID = charData.AllianceID
	return bot, nil
}

func FetchBotCorpID(charIDStr string) (int, error) {
	aff, err := FetchBotAffiliation(charIDStr)
	if err != nil {
		return 0, err
	}
	return aff.CorporationID, nil
}

func fetchContactsPage(client *http.Client, baseURL string) ([]EsiStanding, error) {
	var allItems []EsiStanding
	for page := 1; ; page++ {
		url := fmt.Sprintf("%s?page=%d", baseURL, page)
		resp, err := client.Get(url)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}

		if resp.StatusCode != 200 {
			_ = resp.Body.Close()
			if page == 1 {
				respFallback, errFallback := client.Get(baseURL)
				if errFallback == nil && respFallback.StatusCode == 200 {
					var fallbackItems []EsiStanding
					if json.NewDecoder(respFallback.Body).Decode(&fallbackItems) == nil {
						_ = respFallback.Body.Close()
						return fallbackItems, nil
					}
					_ = respFallback.Body.Close()
				} else if respFallback != nil {
					_ = respFallback.Body.Close()
				}
				return nil, fmt.Errorf("ESI endpoint %s returned status %d", baseURL, resp.StatusCode)
			}
			break
		}

		var pageItems []EsiStanding
		if err := json.NewDecoder(resp.Body).Decode(&pageItems); err != nil {
			_ = resp.Body.Close()
			break
		}
		_ = resp.Body.Close()

		if len(pageItems) == 0 {
			break
		}
		allItems = append(allItems, pageItems...)
		if len(pageItems) < 1000 {
			break
		}
	}
	return allItems, nil
}

func FetchCorpStandings(client *http.Client, charIDStr string, corpID int) (map[int]float64, error) {
	standingsMap := make(map[int]float64)
	var fetchErrors []string

	if client == nil {
		return nil, fmt.Errorf("OAuth HTTP client is nil")
	}

	botAff, errBot := FetchBotAffiliation(charIDStr)
	if errBot == nil && botAff.AllianceID > 0 {
		allianceContacts, err := fetchContactsPage(client, fmt.Sprintf("https://esi.evetech.net/latest/alliances/%d/contacts/", botAff.AllianceID))
		if err == nil {
			log.Printf("Fetched %d alliance contacts for alliance %d", len(allianceContacts), botAff.AllianceID)
			for _, c := range allianceContacts {
				if id := c.ID(); id != 0 {
					standingsMap[id] = c.Standing
				}
			}
		} else {
			log.Printf("Warning: failed to fetch alliance contacts for %d: %v", botAff.AllianceID, err)
		}
	}

	if corpID > 0 {
		corpContacts, err := fetchContactsPage(client, fmt.Sprintf("https://esi.evetech.net/latest/corporations/%d/contacts/", corpID))
		if err == nil {
			log.Printf("Fetched %d corporation contacts for corp %d", len(corpContacts), corpID)
			for _, c := range corpContacts {
				if id := c.ID(); id != 0 {
					standingsMap[id] = c.Standing
				}
			}
		} else {
			fetchErrors = append(fetchErrors, fmt.Sprintf("corp contacts error: %v", err))
			log.Printf("Warning: failed to fetch corporation contacts for %d: %v", corpID, err)
		}
	}

	if charIDStr != "" && charIDStr != "0" {
		charContacts, err := fetchContactsPage(client, fmt.Sprintf("https://esi.evetech.net/latest/characters/%s/contacts/", charIDStr))
		if err == nil {
			log.Printf("Fetched %d character contacts for char %s", len(charContacts), charIDStr)
			for _, c := range charContacts {
				if id := c.ID(); id != 0 {
					standingsMap[id] = c.Standing
				}
			}
		} else {
			log.Printf("Warning: failed to fetch character contacts for %s: %v", charIDStr, err)
		}
	}

	if corpID > 0 {
		npcStandings, err := fetchContactsPage(client, fmt.Sprintf("https://esi.evetech.net/latest/corporations/%d/standings/", corpID))
		if err == nil {
			log.Printf("Fetched %d NPC standings for corp %d", len(npcStandings), corpID)
			for _, s := range npcStandings {
				if id := s.ID(); id != 0 {
					if _, exists := standingsMap[id]; !exists {
						standingsMap[id] = s.Standing
					}
				}
			}
		} else {
			log.Printf("Warning: failed to fetch NPC standings for corp %d: %v", corpID, err)
		}
	}

	if len(standingsMap) == 0 && len(fetchErrors) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(fetchErrors, "; "))
	}

	return standingsMap, nil
}

func ResolveStanding(standingsMap map[int]float64, charAff EsiCharacterAffiliation) float64 {
	if standingsMap == nil {
		return 0.0
	}
	if val, ok := standingsMap[charAff.CharacterID]; ok {
		return val
	}
	if val, ok := standingsMap[charAff.CorporationID]; ok {
		return val
	}
	if charAff.AllianceID != 0 {
		if val, ok := standingsMap[charAff.AllianceID]; ok {
			return val
		}
	}
	if charAff.FactionID != 0 {
		if val, ok := standingsMap[charAff.FactionID]; ok {
			return val
		}
	}
	return 0.0
}

func DetermineStandingType(val float64) string {
	if val >= 5.0 {
		return StandingExcellent
	} else if val > 0.0 {
		return StandingGood
	} else if val == 0.0 {
		return StandingNeutral
	} else if val > -5.0 {
		return StandingBad
	}
	return StandingTerrible
}

func PollRoles(dg *discordgo.Session) {
	CheckRoles(dg)
	ticker := time.NewTicker(60 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		CheckRoles(dg)
	}
}

func CheckRoles(dg *discordgo.Session) {
	log.Println("Checking ESI for character roles...")

	type CharMap struct {
		CharID    int
		DiscordID string
	}

	var charMappings []CharMap

	rows, err := db.DB.Query("SELECT eve_id, discord_id FROM character_to_discord")
	if err != nil {
		log.Printf("Error fetching char mappings: %v", err)
		return
	}
	for rows.Next() {
		var charID int
		var discordID string
		if err := rows.Scan(&charID, &discordID); err == nil {
			charMappings = append(charMappings, CharMap{CharID: charID, DiscordID: discordID})
		}
	}
	_ = rows.Close()

	if len(charMappings) == 0 {
		return
	}

	var charIDs []int
	for _, cmap := range charMappings {
		charIDs = append(charIDs, cmap.CharID)
	}

	charNamesMap := make(map[int]string)
	namesBodyBytes, err := json.Marshal(charIDs)
	if err == nil {
		namesResp, namesErr := esi.Post("https://esi.evetech.net/latest/universe/names/", "application/json", bytes.NewBuffer(namesBodyBytes))
		if namesErr == nil && namesResp != nil && namesResp.StatusCode == 200 {
			var namesData []struct {
				ID       int    `json:"id"`
				Name     string `json:"name"`
				Category string `json:"category"`
			}
			if json.NewDecoder(namesResp.Body).Decode(&namesData) == nil {
				for _, n := range namesData {
					charNamesMap[n.ID] = n.Name
				}
			}
			_ = namesResp.Body.Close()
		} else if namesResp != nil && namesResp.Body != nil {
			_ = namesResp.Body.Close()
		}
	}

	bodyBytes, err := json.Marshal(charIDs)
	if err != nil {
		log.Printf("Error encoding character IDs for affiliation: %v", err)
		return
	}

	resp, err := esi.Post("https://esi.evetech.net/latest/characters/affiliation/", "application/json", bytes.NewBuffer(bodyBytes))
	if err != nil || (resp != nil && resp.StatusCode != 200) {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		log.Printf("Error fetching character affiliations from ESI: %v (Status: %d)", err, status)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return
	}

	var affiliations []EsiCharacterAffiliation
	if err := json.NewDecoder(resp.Body).Decode(&affiliations); err != nil {
		log.Printf("Error decoding character affiliations: %v", err)
		_ = resp.Body.Close()
		return
	}
	_ = resp.Body.Close()

	affMap := make(map[int]EsiCharacterAffiliation)
	for _, aff := range affiliations {
		affMap[aff.CharacterID] = aff
	}

	// Process each guild the bot is in
	for _, guild := range dg.State.Guilds {
		guildID := guild.ID

		var syncEnabled string
		if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'sync_roles'", guildID).Scan(&syncEnabled); err == nil && syncEnabled == "0" {
			continue
		}

		integ, _ := db.GetGuildIntegration(guildID)
		charIDStr := ""
		if integ.EveCharacterID > 0 {
			charIDStr = strconv.Itoa(integ.EveCharacterID)
		} else {
			charIDStr = config.GetCharacterID()
		}

		validSeatChars, errSeat := seat.GetValidSeatCharacters(guildID)
		if errSeat != nil {
			log.Printf("[Guild %s] Warning: failed to fetch valid SeAT characters: %v", guildID, errSeat)
		}

		var standingsMap map[int]float64
		if charIDStr != "" && charIDStr != "0" {
			botCorpID, errCorp := FetchBotCorpID(charIDStr)
			if errCorp == nil {
				oauthClient := GetOAuthHTTPClient(guildID)
				if oauthClient != nil {
					fetchedStandings, errStandings := FetchCorpStandings(oauthClient, charIDStr, botCorpID)
					if errStandings == nil {
						standingsMap = fetchedStandings
					}
				}
			}
		}

		corpToRole := make(map[int]string)
		corpRows, err := db.DB.Query("SELECT corp_id, role_id FROM corp_to_role WHERE guild_id = ?", guildID)
		if err == nil {
			for corpRows.Next() {
				var cid int
				var rid string
				if err := corpRows.Scan(&cid, &rid); err == nil {
					corpToRole[cid] = rid
				}
			}
			_ = corpRows.Close()
		}

		allianceToRole := make(map[int]string)
		allianceRows, err := db.DB.Query("SELECT alliance_id, role_id FROM alliance_to_role WHERE guild_id = ?", guildID)
		if err == nil {
			for allianceRows.Next() {
				var aid int
				var rid string
				if err := allianceRows.Scan(&aid, &rid); err == nil {
					allianceToRole[aid] = rid
				}
			}
			_ = allianceRows.Close()
		}

		greetOnRole := make(map[string]bool)
		greetRows, err := db.DB.Query("SELECT role_id FROM greet_on_role WHERE guild_id = ?", guildID)
		if err == nil {
			for greetRows.Next() {
				var rid string
				if err := greetRows.Scan(&rid); err == nil {
					greetOnRole[rid] = true
				}
			}
			_ = greetRows.Close()
		}

		standingToRole := make(map[string]string)
		standingRows, err := db.DB.Query("SELECT standing_type, role_id FROM standing_to_role WHERE guild_id = ?", guildID)
		if err == nil {
			for standingRows.Next() {
				var stype string
				var rid string
				if err := standingRows.Scan(&stype, &rid); err == nil {
					standingToRole[stype] = rid
				}
			}
			_ = standingRows.Close()
		}

		excludedCorpStandings := make(map[int]bool)
		exCorpRows, err := db.DB.Query("SELECT corp_id FROM excluded_corp_standings WHERE guild_id = ?", guildID)
		if err == nil {
			for exCorpRows.Next() {
				var cid int
				if err := exCorpRows.Scan(&cid); err == nil {
					excludedCorpStandings[cid] = true
				}
			}
			_ = exCorpRows.Close()
		}

		var is2FAEnabled bool
		var twoFAVal string
		if err := db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = '2fa_enabled'", guildID).Scan(&twoFAVal); err == nil && twoFAVal == "1" {
			is2FAEnabled = true
		}

		excluded2FARoles := make(map[string]bool)
		ex2FARows, err := db.DB.Query("SELECT role_id FROM excluded_2fa_roles WHERE guild_id = ?", guildID)
		if err == nil {
			for ex2FARows.Next() {
				var rid string
				if err := ex2FARows.Scan(&rid); err == nil {
					excluded2FARoles[rid] = true
				}
			}
			_ = ex2FARows.Close()
		}

		seatNeededRoles := make(map[string]bool)
		seatRows, err := db.DB.Query("SELECT role_id FROM seat_needed_roles WHERE guild_id = ?", guildID)
		if err == nil {
			for seatRows.Next() {
				var rid string
				if err := seatRows.Scan(&rid); err == nil {
					seatNeededRoles[rid] = true
				}
			}
			_ = seatRows.Close()
		}

		allianceReqCorpRoles := make(map[string]bool)
		reqCorpRows, err := db.DB.Query("SELECT role_id FROM alliance_required_corp_roles WHERE guild_id = ?", guildID)
		if err == nil {
			for reqCorpRows.Next() {
				var rid string
				if err := reqCorpRows.Scan(&rid); err == nil {
					allianceReqCorpRoles[rid] = true
				}
			}
			_ = reqCorpRows.Close()
		}

		var greetingChannel string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_channel'", guildID).Scan(&greetingChannel)

		var guestRoleID string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&guestRoleID)

		excludedUsers := make(map[string]bool)
		exUsersRows, err := db.DB.Query("SELECT discord_id FROM excluded_users WHERE guild_id = ?", guildID)
		if err == nil {
			for exUsersRows.Next() {
				var dID string
				if exUsersRows.Scan(&dID) == nil {
					excludedUsers[dID] = true
				}
			}
			_ = exUsersRows.Close()
		}

		for _, cmap := range charMappings {
			charID := cmap.CharID
			discordID := cmap.DiscordID

			if excludedUsers[discordID] {
				continue
			}

			charInfo, exists := affMap[charID]
			if !exists {
				continue
			}

			member, err := dg.GuildMember(guildID, discordID)
			if err != nil || member == nil {
				// Member is not in this guild, continue
				continue
			}

			if realCharName, ok := charNamesMap[charID]; ok {
				searchName := member.Nick
				if searchName == "" {
					if member.User != nil && member.User.GlobalName != "" {
						searchName = member.User.GlobalName
					} else if member.User != nil {
						searchName = member.User.Username
					}
				}

				if !strings.EqualFold(searchName, realCharName) {
					msg := fmt.Sprintf("User <@%s> nickname ('%s') no longer matches their mapped EVE character '%s' (https://evewho.com/character/%d). Removing mapping and triggering automap.", discordID, searchName, realCharName, charID)
					db.DiscordLog(dg, guildID, msg)
					_, _ = db.DB.Exec("DELETE FROM character_to_discord WHERE discord_id = ?", discordID)
					_, _ = db.DB.Exec("DELETE FROM auto_map_attempts WHERE guild_id = ? AND discord_id = ?", guildID, discordID)
					continue
				}
			}

			hadGuestRole := guestRoleID != "" && HasRole(member.Roles, guestRoleID)

			charName := fmt.Sprintf("Character ID %d", charID)
			if realCharName, ok := charNamesMap[charID]; ok && realCharName != "" {
				charName = realCharName
			}

			isSeatActive := validSeatChars != nil && validSeatChars[charID]

			var isVerified bool
			var vCount int
			if err := db.DB.QueryRow("SELECT COUNT(*) FROM verified_2fa_users WHERE guild_id = ? AND discord_id = ?", guildID, discordID).Scan(&vCount); err == nil && vCount > 0 {
				isVerified = true
			}

			var corpMatchFound bool
			var userCorpRoleID string
			for corpID, roleID := range corpToRole {
				if charInfo.CorporationID == corpID {
					if seatNeededRoles[roleID] && !isSeatActive {
						break
					}
					corpMatchFound = true
					userCorpRoleID = roleID
					break
				}
			}

			var allianceMatchFound bool
			var userAllianceRoleID string
			for allianceID, roleID := range allianceToRole {
				if charInfo.AllianceID == allianceID {
					if seatNeededRoles[roleID] && !isSeatActive {
						break
					}
					if allianceReqCorpRoles[roleID] && !corpMatchFound {
						break
					}
					allianceMatchFound = true
					userAllianceRoleID = roleID
					break
				}
			}

			// Determine if 2FA applies to this user
			requires2FA := false
			if is2FAEnabled && !isVerified && (corpMatchFound || allianceMatchFound) {
				corpExcluded := userCorpRoleID != "" && excluded2FARoles[userCorpRoleID]
				allianceExcluded := userAllianceRoleID != "" && excluded2FARoles[userAllianceRoleID]

				if !corpExcluded && !allianceExcluded {
					requires2FA = true
				}
			}

			if requires2FA {
				for _, roleID := range corpToRole {
					if HasRole(member.Roles, roleID) {
						_ = dg.GuildMemberRoleRemove(guildID, discordID, roleID)
					}
				}
				for _, roleID := range allianceToRole {
					if HasRole(member.Roles, roleID) {
						_ = dg.GuildMemberRoleRemove(guildID, discordID, roleID)
					}
				}
				for _, roleID := range standingToRole {
					if HasRole(member.Roles, roleID) {
						_ = dg.GuildMemberRoleRemove(guildID, discordID, roleID)
					}
				}

				if guestRoleID != "" && !HasRole(member.Roles, guestRoleID) {
					msg := fmt.Sprintf("Adding Guest role <@&%s> to user <@%s> pending 2FA verification.", guestRoleID, discordID)
					db.DiscordLog(dg, guildID, msg)
					_ = dg.GuildMemberRoleAdd(guildID, discordID, guestRoleID)
				}

				var pendingCode string
				errPending := db.DB.QueryRow("SELECT code FROM pending_2fa WHERE guild_id = ? AND discord_id = ?", guildID, discordID).Scan(&pendingCode)
				if errPending == sql.ErrNoRows {
					code, errCode := Generate2FACode()
					if errCode == nil {
						nick := member.Nick
						if nick == "" {
							if member.User != nil && member.User.GlobalName != "" {
								nick = member.User.GlobalName
							} else if member.User != nil {
								nick = member.User.Username
							}
						}
						if realCharName, ok := charNamesMap[charID]; ok && realCharName != "" {
							nick = realCharName
						}

						_, _ = db.DB.Exec("INSERT OR REPLACE INTO pending_2fa (guild_id, discord_id, eve_id, char_name, code, expires_at) VALUES (?, ?, ?, ?, ?, ?)",
							guildID, discordID, charID, nick, code, time.Now().Add(24*time.Hour))

						senderCharID := integ.EveCharacterID
						go func(cID, sCharID int, n, c, dID, gID string) {
							oauthClient := GetOAuthHTTPClient(gID)
							mailBody := fmt.Sprintf("Your Discord 2FA verification code is: %s\n\nPlease reply to the bot on Discord with this code to receive your Discord roles.", c)
							errMail := mail.SendEveMail(oauthClient, sCharID, cID, "Discord 2FA Verification Code", mailBody)
							if errMail != nil {
								db.DiscordLog(dg, gID, fmt.Sprintf("Failed to send 2FA EVE-Mail to character %s (ID: %d): %v", n, cID, errMail))
							} else {
								db.DiscordLog(dg, gID, fmt.Sprintf("Sent 2FA EVE-Mail code to character %s (ID: %d) for user <@%s>", n, cID, dID))
							}
						}(charID, senderCharID, nick, code, discordID, guildID)

						go func(dID, n string) {
							dmChan, errDM := dg.UserChannelCreate(dID)
							if errDM == nil && dmChan != nil {
								dmText := fmt.Sprintf("Welcome %s, please fill in the two factor authentication code we have send to your character (%s)'s eve-mail, in order to receive your Discord roles. If you need a new code, reply with 'resend 2FA'.", n, n)
								_, _ = dg.ChannelMessageSend(dmChan.ID, dmText)
							}
						}(discordID, nick)
					}
				}
				continue
			}

			for corpID, roleID := range corpToRole {
				if charInfo.CorporationID == corpID {
					if seatNeededRoles[roleID] && !isSeatActive {
						if HasRole(member.Roles, roleID) {
							msg := fmt.Sprintf("Removing Corp role <@&%s> from user <@%s> (Char: %s, Mapped Corp: %s) because character is not active in SeAT.", roleID, discordID, charName, GetCorpName(corpID))
							db.DiscordLog(dg, guildID, msg)
							err := dg.GuildMemberRoleRemove(guildID, discordID, roleID)
							if err != nil {
								db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Corp role <@&%s> from user <@%s>: %v", roleID, discordID, err))
							}
						}
						continue
					}
					if !HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Adding Corp role <@&%s> to user <@%s> (Char: %s, Mapped Corp: %s)", roleID, discordID, charName, GetCorpName(corpID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleAdd(guildID, discordID, roleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to add Corp role <@&%s> to user <@%s>: %v", roleID, discordID, err))
						} else if hadGuestRole && greetOnRole[roleID] && greetingChannel != "" {
							greetMsg := fmt.Sprintf("<@%s>, welcome to <@&%s>", discordID, roleID)
							_, _ = dg.ChannelMessageSend(greetingChannel, greetMsg)
						}
					}
				} else {
					if HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Removing Corp role <@&%s> from user <@%s> (Char: %s, Mapped Corp: %s)", roleID, discordID, charName, GetCorpName(corpID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleRemove(guildID, discordID, roleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Corp role <@&%s> from user <@%s>: %v", roleID, discordID, err))
						}
					}
				}
			}

			for allianceID, roleID := range allianceToRole {
				if charInfo.AllianceID == allianceID {
					if seatNeededRoles[roleID] && !isSeatActive {
						if HasRole(member.Roles, roleID) {
							msg := fmt.Sprintf("Removing Alliance role <@&%s> from user <@%s> (Char: %s, Mapped Alliance: %s) because character is not active in SeAT.", roleID, discordID, charName, GetAllianceName(allianceID))
							db.DiscordLog(dg, guildID, msg)
							err := dg.GuildMemberRoleRemove(guildID, discordID, roleID)
							if err != nil {
								db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Alliance role <@&%s> from user <@%s>: %v", roleID, discordID, err))
							}
						}
						continue
					}
					if allianceReqCorpRoles[roleID] && !corpMatchFound {
						if HasRole(member.Roles, roleID) {
							msg := fmt.Sprintf("Removing Alliance role <@&%s> from user <@%s> (Char: %s, Mapped Alliance: %s) because they do not have a mapped Corporation role.", roleID, discordID, charName, GetAllianceName(allianceID))
							db.DiscordLog(dg, guildID, msg)
							err := dg.GuildMemberRoleRemove(guildID, discordID, roleID)
							if err != nil {
								db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Alliance role <@&%s> from user <@%s>: %v", roleID, discordID, err))
							}
						}
						continue
					}
					if !HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Adding Alliance role <@&%s> to user <@%s> (Char: %s, Mapped Alliance: %s)", roleID, discordID, charName, GetAllianceName(allianceID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleAdd(guildID, discordID, roleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to add Alliance role <@&%s> to user <@%s>: %v", roleID, discordID, err))
						} else if hadGuestRole && greetOnRole[roleID] && greetingChannel != "" {
							greetMsg := fmt.Sprintf("<@%s>, welcome to <@&%s>", discordID, roleID)
							_, _ = dg.ChannelMessageSend(greetingChannel, greetMsg)
						}
					}
				} else {
					if HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Removing Alliance role <@&%s> from user <@%s> (Char: %s, Mapped Alliance: %s)", roleID, discordID, charName, GetAllianceName(allianceID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleRemove(guildID, discordID, roleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Alliance role <@&%s> from user <@%s>: %v", roleID, discordID, err))
						}
					}
				}
			}

			if guestRoleID != "" {
				if corpMatchFound || allianceMatchFound {
					if HasRole(member.Roles, guestRoleID) {
						msg := fmt.Sprintf("Removing Guest role <@&%s> from user <@%s> because they now have a mapped Corp/Alliance role.", guestRoleID, discordID)
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleRemove(guildID, discordID, guestRoleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Guest role <@&%s> from user <@%s>: %v", guestRoleID, discordID, err))
						}
					}
				} else {
					if !HasRole(member.Roles, guestRoleID) {
						msg := fmt.Sprintf("Adding Guest role <@&%s> to user <@%s> because their EVE Corporation or Alliance is unmapped.", guestRoleID, discordID)
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleAdd(guildID, discordID, guestRoleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to add Guest role <@&%s> to user <@%s>: %v", guestRoleID, discordID, err))
						}
					}
				}
			}

			// Standings
			if len(standingToRole) > 0 && standingsMap != nil {
				standingVal := ResolveStanding(standingsMap, charInfo)
				stype := DetermineStandingType(standingVal)
				expectedRoleID := standingToRole[stype]

				isExcluded := false
				if charInfo.CorporationID != 0 && excludedCorpStandings[charInfo.CorporationID] {
					isExcluded = true
				}

				if isExcluded {
					for _, sRoleID := range standingToRole {
						if HasRole(member.Roles, sRoleID) {
							msg := fmt.Sprintf("Removing Standing role <@&%s> from user <@%s> (Char: %s) because their corporation is excluded from standings.", sRoleID, discordID, charName)
							db.DiscordLog(dg, guildID, msg)
							err := dg.GuildMemberRoleRemove(guildID, discordID, sRoleID)
							if err != nil {
								db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Standing role <@&%s> from user <@%s>: %v", sRoleID, discordID, err))
							}
						}
					}
				} else {
					for st, sRoleID := range standingToRole {
						if st == stype && expectedRoleID != "" {
							if !HasRole(member.Roles, sRoleID) {
								msg := fmt.Sprintf("Adding Standing role <@&%s> to user <@%s> (Char: %s, Standing: %s, Value: %.1f)", sRoleID, discordID, charName, stype, standingVal)
								db.DiscordLog(dg, guildID, msg)
								err := dg.GuildMemberRoleAdd(guildID, discordID, sRoleID)
								if err != nil {
									db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to add Standing role <@&%s> to user <@%s>: %v", sRoleID, discordID, err))
								}
							}
						} else {
							if HasRole(member.Roles, sRoleID) {
								msg := fmt.Sprintf("Removing Standing role <@&%s> from user <@%s> (Char: %s, Current Standing: %s)", sRoleID, discordID, charName, stype)
								db.DiscordLog(dg, guildID, msg)
								err := dg.GuildMemberRoleRemove(guildID, discordID, sRoleID)
								if err != nil {
									db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Standing role <@&%s> from user <@%s>: %v", sRoleID, discordID, err))
								}
							}
						}
					}
				}
			} else if len(standingToRole) > 0 {
				for _, sRoleID := range standingToRole {
					if HasRole(member.Roles, sRoleID) {
						msg := fmt.Sprintf("Removing Standing role <@&%s> from user <@%s> (Char: %s) because standings could not be resolved.", sRoleID, discordID, charName)
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleRemove(guildID, discordID, sRoleID)
						if err != nil {
							db.DiscordLog(dg, guildID, fmt.Sprintf("Failed to remove Standing role <@&%s> from user <@%s>: %v", sRoleID, discordID, err))
						}
					}
				}
			}
		}
	}
}

func HasRole(roles []string, roleID string) bool {
	for _, r := range roles {
		if r == roleID {
			return true
		}
	}
	return false
}

func GetCorpName(id int) string {
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/v4/corporations/%d/", id))
	if err != nil || resp.StatusCode != 200 {
		return fmt.Sprintf("ID: %d", id)
	}
	var info struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		_ = resp.Body.Close()
		return fmt.Sprintf("ID: %d", id)
	}
	_ = resp.Body.Close()
	return info.Name
}

func GetAllianceName(id int) string {
	resp, err := esi.Get(fmt.Sprintf("https://esi.evetech.net/v3/alliances/%d/", id))
	if err != nil || resp.StatusCode != 200 {
		return fmt.Sprintf("ID: %d", id)
	}
	var info struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		_ = resp.Body.Close()
		return fmt.Sprintf("ID: %d", id)
	}
	_ = resp.Body.Close()
	return info.Name
}

func HandleDirectMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	if m.GuildID != "" {
		return
	}

	content := strings.TrimSpace(m.Content)
	discordID := m.Author.ID

	rows, err := db.DB.Query("SELECT guild_id, eve_id, char_name, code FROM pending_2fa WHERE discord_id = ?", discordID)
	if err != nil {
		log.Printf("Error checking pending 2FA for user %s: %v", discordID, err)
		return
	}
	defer rows.Close()

	type pendingItem struct {
		guildID  string
		eveID    int
		charName string
		code     string
	}
	var pendings []pendingItem
	for rows.Next() {
		var p pendingItem
		if rows.Scan(&p.guildID, &p.eveID, &p.charName, &p.code) == nil {
			pendings = append(pendings, p)
		}
	}

	if len(pendings) == 0 {
		return
	}

	if strings.EqualFold(content, "resend 2FA") {
		newCode, errCode := Generate2FACode()
		if errCode != nil {
			_, _ = s.ChannelMessageSend(m.ChannelID, "An error occurred while generating a new 2FA code. Please try again later.")
			return
		}

		_, errUpdate := db.DB.Exec("UPDATE pending_2fa SET code = ?, expires_at = ? WHERE discord_id = ?", newCode, time.Now().Add(24*time.Hour), discordID)
		if errUpdate != nil {
			_, _ = s.ChannelMessageSend(m.ChannelID, "An error occurred updating your 2FA code. Please try again later.")
			return
		}

		eveID := pendings[0].eveID
		charName := pendings[0].charName
		pGuildID := pendings[0].guildID

		go func() {
			oauthClient := GetOAuthHTTPClient(pGuildID)
			integ, _ := db.GetGuildIntegration(pGuildID)
			senderCharID := integ.EveCharacterID
			mailBody := fmt.Sprintf("Your new Discord 2FA verification code is: %s\n\nPlease reply to the bot on Discord with this code to receive your Discord roles.", newCode)
			errMail := mail.SendEveMail(oauthClient, senderCharID, eveID, "Discord 2FA Verification Code", mailBody)
			if errMail != nil {
				for _, p := range pendings {
					db.DiscordLog(s, p.guildID, fmt.Sprintf("Failed to resend 2FA EVE-Mail to character %s (ID: %d): %v", charName, eveID, errMail))
				}
			} else {
				for _, p := range pendings {
					db.DiscordLog(s, p.guildID, fmt.Sprintf("Resent 2FA EVE-Mail code to character %s (ID: %d) for user <@%s>", charName, eveID, discordID))
				}
			}
		}()

		_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("A new two factor authentication code has been sent to your character (%s)'s eve-mail.", charName))
		return
	}

	matchedAny := false
	for _, p := range pendings {
		if content == p.code {
			matchedAny = true
			_, _ = db.DB.Exec("INSERT OR REPLACE INTO verified_2fa_users (guild_id, discord_id, verified_at) VALUES (?, ?, ?)", p.guildID, discordID, time.Now())
			_, _ = db.DB.Exec("DELETE FROM pending_2fa WHERE guild_id = ? AND discord_id = ?", p.guildID, discordID)

			var guestRoleID string
			_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", p.guildID).Scan(&guestRoleID)
			if guestRoleID != "" {
				_ = s.GuildMemberRoleRemove(p.guildID, discordID, guestRoleID)
			}

			db.DiscordLog(s, p.guildID, fmt.Sprintf("User <@%s> successfully completed 2FA verification for character %s.", discordID, p.charName))
		}
	}

	if matchedAny {
		_, _ = s.ChannelMessageSend(m.ChannelID, "Your two factor authentication was successful! Your Discord roles are now being assigned.")
		go CheckRoles(s)
		return
	}

	_, _ = s.ChannelMessageSend(m.ChannelID, "Invalid two factor authentication code. Please check your EVE-mail and try again, or reply with 'resend 2FA' for a new code.")
}
