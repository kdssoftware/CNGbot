package roles

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"evemaildiscord/db"
	"evemaildiscord/esi"
	"evemaildiscord/seat"

	"github.com/bwmarrin/discordgo"
)

func PollGuestRoles(dg *discordgo.Session) {
	CheckGuestRoles(dg)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		CheckGuestRoles(dg)
	}
}

func CheckGuestRoles(dg *discordgo.Session) {
	type CharMap struct {
		CharID    int
		DiscordID string
	}

	var charMappings []CharMap
	rows, err := db.DB.Query("SELECT eve_id, discord_id FROM character_to_discord")
	if err != nil {
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

	for _, guild := range dg.State.Guilds {
		guildID := guild.ID

		var guestRoleID string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&guestRoleID)
		if guestRoleID == "" {
			continue
		}

		excludedUsers := make(map[string]bool)
		exRows, err := db.DB.Query("SELECT discord_id FROM excluded_users WHERE guild_id = ?", guildID)
		if err == nil {
			for exRows.Next() {
				var dID string
				if exRows.Scan(&dID) == nil {
					excludedUsers[dID] = true
				}
			}
			_ = exRows.Close()
		}

		var activeMappings []CharMap
		var charIDs []int
		for _, cmap := range charMappings {
			if excludedUsers[cmap.DiscordID] {
				continue
			}

			member, err := dg.GuildMember(guildID, cmap.DiscordID)
			if err != nil || member == nil {
				continue
			}

			if HasRole(member.Roles, guestRoleID) {
				activeMappings = append(activeMappings, cmap)
				charIDs = append(charIDs, cmap.CharID)
			}
		}

		if len(charIDs) == 0 {
			continue
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

		var greetingChannel string
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'greeting_channel'", guildID).Scan(&greetingChannel)

		bodyBytes, err := json.Marshal(charIDs)
		if err != nil {
			continue
		}

		resp, err := esi.Post("https://esi.evetech.net/latest/characters/affiliation/", "application/json", bytes.NewBuffer(bodyBytes))
		if err != nil || (resp != nil && resp.StatusCode != 200) {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			continue
		}

		var affiliations []EsiCharacterAffiliation
		if err := json.NewDecoder(resp.Body).Decode(&affiliations); err != nil {
			_ = resp.Body.Close()
			continue
		}
		_ = resp.Body.Close()

		affMap := make(map[int]EsiCharacterAffiliation)
		for _, aff := range affiliations {
			affMap[aff.CharacterID] = aff
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

		var validSeatChars map[int]bool
		if len(seatNeededRoles) > 0 {
			var errSeat error
			validSeatChars, errSeat = seat.GetValidSeatCharacters(guildID)
			if errSeat != nil {
				log.Printf("Warning: failed to fetch valid SeAT characters in guest worker: %v", errSeat)
			}
		}

		for _, cmap := range activeMappings {
			charID := cmap.CharID
			discordID := cmap.DiscordID

			charInfo, exists := affMap[charID]
			if !exists {
				continue
			}

			member, err := dg.GuildMember(guildID, discordID)
			if err != nil || member == nil {
				continue
			}

			charName := fmt.Sprintf("Character ID %d", charID)

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

			requires2FA := false
			if is2FAEnabled && !isVerified && (corpMatchFound || allianceMatchFound) {
				corpExcluded := userCorpRoleID != "" && excluded2FARoles[userCorpRoleID]
				allianceExcluded := userAllianceRoleID != "" && excluded2FARoles[userAllianceRoleID]
				if !corpExcluded && !allianceExcluded {
					requires2FA = true
				}
			}

			if requires2FA {
				continue
			}

			for corpID, roleID := range corpToRole {
				if charInfo.CorporationID == corpID {
					if seatNeededRoles[roleID] && !isSeatActive {
						continue
					}
					if !HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Adding Corp role <@&%s> to user <@%s> (Char: %s, Mapped Corp: %s)", roleID, discordID, charName, GetCorpName(corpID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleAdd(guildID, discordID, roleID)
						if err == nil && greetOnRole[roleID] && greetingChannel != "" {
							greetMsg := fmt.Sprintf("<@%s>, welcome to <@&%s>", discordID, roleID)
							_, _ = dg.ChannelMessageSend(greetingChannel, greetMsg)
						}
					}
				}
			}

			for allianceID, roleID := range allianceToRole {
				if charInfo.AllianceID == allianceID {
					if seatNeededRoles[roleID] && !isSeatActive {
						continue
					}
					if allianceReqCorpRoles[roleID] && !corpMatchFound {
						continue
					}
					if !HasRole(member.Roles, roleID) {
						msg := fmt.Sprintf("Adding Alliance role <@&%s> to user <@%s> (Char: %s, Mapped Alliance: %s)", roleID, discordID, charName, GetAllianceName(allianceID))
						db.DiscordLog(dg, guildID, msg)
						err := dg.GuildMemberRoleAdd(guildID, discordID, roleID)
						if err == nil && greetOnRole[roleID] && greetingChannel != "" {
							greetMsg := fmt.Sprintf("<@%s>, welcome to <@&%s>", discordID, roleID)
							_, _ = dg.ChannelMessageSend(greetingChannel, greetMsg)
						}
					}
				}
			}

			if corpMatchFound || allianceMatchFound {
				if HasRole(member.Roles, guestRoleID) {
					msg := fmt.Sprintf("Removing Guest role <@&%s> from user <@%s> because they now have a mapped Corp/Alliance role.", guestRoleID, discordID)
					db.DiscordLog(dg, guildID, msg)
					_ = dg.GuildMemberRoleRemove(guildID, discordID, guestRoleID)
				}
			}
		}
	}
}
