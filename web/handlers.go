package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"evemaildiscord/config"
	"evemaildiscord/db"

	"github.com/bwmarrin/discordgo"
)

type WebHandler struct {
	dg *discordgo.Session
}

func NewWebHandler(dg *discordgo.Session) *WebHandler {
	return &WebHandler{dg: dg}
}

func (h *WebHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	session := Store.GetSession(r)
	if session != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	_ = RenderLanding(w)
}

func (h *WebHandler) HandleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	clientID := config.GetDiscordClientID()
	if clientID == "" {
		http.Error(w, "DISCORD_CLIENT_ID is not configured", http.StatusInternalServerError)
		return
	}

	state, err := GenerateRandomToken(16)
	if err != nil {
		http.Error(w, "Failed to generate security state", http.StatusInternalServerError)
		return
	}

	Store.SaveOAuthState(state, "", "", "discord")

	redirectURI := config.GetDiscordOAuthRedirect()
	authURL := fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=identify%%20guilds&state=%s",
		url.QueryEscape(clientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

type discordTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type discordUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
}

type discordUserGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions string `json:"permissions"`
}

func (h *WebHandler) HandleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		http.Error(w, "Missing state or authorization code", http.StatusBadRequest)
		return
	}

	_, valid := Store.ValidateAndConsumeOAuthState(state, "discord")
	if !valid {
		http.Error(w, "Invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	clientID := config.GetDiscordClientID()
	clientSecret := config.GetDiscordClientSecret()
	redirectURI := config.GetDiscordOAuthRedirect()

	if clientID == "" || clientSecret == "" {
		http.Error(w, "Discord OAuth credentials not configured", http.StatusInternalServerError)
		return
	}

	form := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}

	resp, err := http.PostForm("https://discord.com/api/oauth2/token", form)
	if err != nil {
		log.Printf("Error exchanging Discord code: %v", err)
		http.Error(w, "Failed to exchange Discord authorization code", http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Discord token endpoint returned status %d: %s", resp.StatusCode, string(body))
		http.Error(w, "Discord token exchange failed", http.StatusBadRequest)
		return
	}

	var tokenResp discordTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Error(w, "Failed to decode Discord token", http.StatusInternalServerError)
		return
	}

	// Fetch user info
	userReq, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me", nil)
	userReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	userResp, err := http.DefaultClient.Do(userReq)
	if err != nil || userResp.StatusCode != http.StatusOK {
		if userResp != nil {
			_ = userResp.Body.Close()
		}
		http.Error(w, "Failed to fetch user profile from Discord", http.StatusInternalServerError)
		return
	}

	var dUser discordUser
	_ = json.NewDecoder(userResp.Body).Decode(&dUser)
	_ = userResp.Body.Close()

	// Fetch user guilds
	guildsReq, _ := http.NewRequest("GET", "https://discord.com/api/v10/users/@me/guilds", nil)
	guildsReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	guildsResp, err := http.DefaultClient.Do(guildsReq)
	if err != nil || guildsResp.StatusCode != http.StatusOK {
		if guildsResp != nil {
			_ = guildsResp.Body.Close()
		}
		http.Error(w, "Failed to fetch guilds from Discord", http.StatusInternalServerError)
		return
	}

	var rawGuilds []discordUserGuild
	_ = json.NewDecoder(guildsResp.Body).Decode(&rawGuilds)
	_ = guildsResp.Body.Close()

	adminGuilds := make(map[string]DiscordGuildInfo)
	for _, g := range rawGuilds {
		permsInt, _ := strconv.ParseInt(g.Permissions, 10, 64)
		// Administrator permission flag is 0x8
		isAdmin := (permsInt&0x8) != 0 || g.Owner
		if isAdmin {
			botInGuild := false
			if h.dg != nil && h.dg.State != nil {
				if _, err := h.dg.State.Guild(g.ID); err == nil {
					botInGuild = true
				}
			}
			adminGuilds[g.ID] = DiscordGuildInfo{
				ID:          g.ID,
				Name:        g.Name,
				Icon:        g.Icon,
				Owner:       g.Owner,
				Permissions: g.Permissions,
				HasAdmin:    true,
				BotInGuild:  botInGuild,
			}
		}
	}

	session, err := Store.CreateSession(dUser.ID, dUser.Username, dUser.Avatar, tokenResp.AccessToken, adminGuilds)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	Store.SetSessionCookie(w, session.ID, session.ExpiresAt)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *WebHandler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var guildsList []GuildCardView
	for _, g := range session.AdminGuilds {
		if h.dg != nil && h.dg.State != nil {
			if _, err := h.dg.State.Guild(g.ID); err == nil {
				g.BotInGuild = true
			} else {
				g.BotInGuild = false
			}
		}

		integ, _ := db.GetGuildIntegration(g.ID)
		eveLinked := integ.EveCharacterID > 0 && integ.EveRefreshToken != ""
		seatConfigured := integ.SeatURL != "" && integ.SeatAPIKey != ""

		guildsList = append(guildsList, GuildCardView{
			DiscordGuildInfo: g,
			EveLinked:        eveLinked,
			SeatConfigured:   seatConfigured,
		})
	}

	botClientID := config.GetDiscordClientID()
	botInviteURL := fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&permissions=268435456&scope=bot%%20applications.commands",
		url.QueryEscape(botClientID),
	)

	_ = RenderDashboard(w, DashboardData{
		User:         session,
		Guilds:       guildsList,
		BotInviteURL: botInviteURL,
	})
}

func (h *WebHandler) HandleGuildConfig(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	guildID := r.URL.Query().Get("id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	guildInfo, hasAccess := session.AdminGuilds[guildID]
	if !hasAccess {
		http.Error(w, "You do not have Administrator access to this Discord server", http.StatusForbidden)
		return
	}

	integ, _ := db.GetGuildIntegration(guildID)

	var eveCharName string
	if integ.EveCharacterID > 0 {
		eveCharName = fetchCharacterName(integ.EveCharacterID)
	}

	var confData struct {
		MailChannel   string
		LogChannel    string
		EventsChannel string
		GuestRole     string
		TwoFA         string
		AutoMap       string
		SyncRoles     string
	}

	if db.DB != nil {
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND (key = 'mail_channel' OR key = 'evemail_channel')", guildID).Scan(&confData.MailChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'log_channel'", guildID).Scan(&confData.LogChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'events_channel'", guildID).Scan(&confData.EventsChannel)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'guest_role'", guildID).Scan(&confData.GuestRole)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'enable_2fa'", guildID).Scan(&confData.TwoFA)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'auto_map_chars'", guildID).Scan(&confData.AutoMap)
		_ = db.DB.QueryRow("SELECT value FROM config WHERE guild_id = ? AND key = 'sync_roles'", guildID).Scan(&confData.SyncRoles)
	}

	successCode := r.URL.Query().Get("success")
	errorCode := r.URL.Query().Get("error")

	_ = RenderGuildConfig(w, GuildConfigData{
		User:             session,
		Guild:            guildInfo,
		Integration:      integ,
		EveCharacterName: eveCharName,
		Config:           confData,
		Success:          successCode,
		Error:            errorCode,
	})
}

func (h *WebHandler) HandleEveLogin(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	guildID := r.URL.Query().Get("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	clientID := config.GetEveClientID()
	if clientID == "" {
		http.Error(w, "EVE_CLIENT_ID is not configured", http.StatusInternalServerError)
		return
	}

	state, err := GenerateRandomToken(16)
	if err != nil {
		http.Error(w, "Failed to generate security state", http.StatusInternalServerError)
		return
	}

	Store.SaveOAuthState(state, session.ID, guildID, "eve")

	scopes := []string{
		"esi-mail.read_mail.v1",
		"esi-mail.send_mail.v1",
		"esi-characters.read_contacts.v1",
		"esi-alliances.read_contacts.v1",
		"esi-corporations.read_contacts.v1",
		"esi-corporations.read_standings.v1",
		"publicData",
	}

	callbackURL := config.GetEveCallbackURL()
	authURL := fmt.Sprintf(
		"https://login.eveonline.com/v2/oauth/authorize?response_type=code&redirect_uri=%s&client_id=%s&scope=%s&state=%s",
		url.QueryEscape(callbackURL),
		url.QueryEscape(clientID),
		url.QueryEscape(strings.Join(scopes, " ")),
		url.QueryEscape(state),
	)

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

type eveTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

type eveVerifyResponse struct {
	CharacterID   int    `json:"CharacterID"`
	CharacterName string `json:"CharacterName"`
}

func (h *WebHandler) HandleEveCallback(w http.ResponseWriter, r *http.Request) {
	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		http.Error(w, "Missing state or authorization code", http.StatusBadRequest)
		return
	}

	stateData, valid := Store.ValidateAndConsumeOAuthState(state, "eve")
	if !valid || stateData.SessionID != session.ID {
		http.Error(w, "Invalid or expired state parameter", http.StatusBadRequest)
		return
	}

	guildID := stateData.GuildID
	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	clientID := config.GetEveClientID()
	clientSecret := config.GetEveSecret()
	if clientID == "" || clientSecret == "" {
		http.Error(w, "EVE SSO credentials not configured", http.StatusInternalServerError)
		return
	}

	form := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {code},
	}

	req, err := http.NewRequest("POST", "https://login.eveonline.com/v2/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		http.Error(w, "Failed to create EVE token request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	basicAuth := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
	req.Header.Set("Authorization", "Basic "+basicAuth)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Error exchanging EVE SSO code: %v", err)
		http.Error(w, "Failed to exchange EVE SSO code", http.StatusInternalServerError)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("EVE SSO token endpoint returned %d: %s", resp.StatusCode, string(body))
		http.Error(w, "EVE SSO token exchange failed", http.StatusBadRequest)
		return
	}

	var tokenResp eveTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		http.Error(w, "Failed to decode EVE SSO token", http.StatusInternalServerError)
		return
	}

	charID, charName := extractEveCharacterFromToken(tokenResp.AccessToken)
	if charID <= 0 {
		http.Error(w, "Failed to identify EVE Character from token", http.StatusInternalServerError)
		return
	}

	err = db.SetGuildEveAuth(guildID, charID, tokenResp.RefreshToken)
	if err != nil {
		log.Printf("Failed to save EVE SSO auth for guild %s: %v", guildID, err)
		http.Error(w, "Failed to save EVE authentication", http.StatusInternalServerError)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("EVE Online Character **%s** (ID: %d) successfully linked to this server by <@%s>.", charName, charID, session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=eve_linked", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildSeatSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")
	seatURL := strings.TrimSpace(r.FormValue("seat_url"))
	seatAPIKey := strings.TrimSpace(r.FormValue("seat_api_key"))

	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	seatURL = strings.TrimRight(seatURL, "/")

	err := db.SetGuildSeatConfig(guildID, seatURL, seatAPIKey)
	if err != nil {
		log.Printf("Error saving SeAT config for guild %s: %v", guildID, err)
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Failed+to+save+SeAT+settings", http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("SeAT API configuration updated by <@%s>.", session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=seat_saved", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildAutomationSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")
	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	autoMap := r.FormValue("auto_map_chars")
	syncRoles := r.FormValue("sync_roles")

	if db.DB != nil {
		_, _ = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'auto_map_chars', ?)", guildID, autoMap)
		_, _ = db.DB.Exec("INSERT OR REPLACE INTO config (guild_id, key, value) VALUES (?, 'sync_roles', ?)", guildID, syncRoles)
	}

	db.DiscordLog(h.dg, guildID, fmt.Sprintf("Automation settings updated by <@%s>.", session.UserID))
	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=1", http.StatusSeeOther)
}

func (h *WebHandler) HandleGuildEveUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session := Store.GetSession(r)
	if session == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	guildID := r.FormValue("guild_id")

	if guildID == "" {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if _, ok := session.AdminGuilds[guildID]; !ok {
		http.Error(w, "You do not have Administrator permissions for this server", http.StatusForbidden)
		return
	}

	err := db.DeleteGuildEveAuth(guildID)
	if err != nil {
		log.Printf("Error unlinking EVE auth for guild %s: %v", guildID, err)
		http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&error=Failed+to+unlink+EVE+character", http.StatusSeeOther)
		return
	}

	if h.dg != nil {
		db.DiscordLog(h.dg, guildID, fmt.Sprintf("EVE character integration unlinked from this server by <@%s>.", session.UserID))
	}

	http.Redirect(w, r, "/dashboard/guild?id="+guildID+"&success=eve_unlinked", http.StatusSeeOther)
}

func (h *WebHandler) HandleLogout(w http.ResponseWriter, r *http.Request) {
	Store.DeleteSession(w, r)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func extractEveCharacterFromToken(accessToken string) (int, string) {
	// 1. Try verify endpoint first
	verifyReq, err := http.NewRequest("GET", "https://login.eveonline.com/oauth/verify", nil)
	if err == nil {
		verifyReq.Header.Set("Authorization", "Bearer "+accessToken)
		verifyResp, errDo := http.DefaultClient.Do(verifyReq)
		if errDo == nil && verifyResp.StatusCode == http.StatusOK {
			var vResp eveVerifyResponse
			if json.NewDecoder(verifyResp.Body).Decode(&vResp) == nil && vResp.CharacterID > 0 {
				_ = verifyResp.Body.Close()
				return vResp.CharacterID, vResp.CharacterName
			}
			_ = verifyResp.Body.Close()
		} else if verifyResp != nil {
			_ = verifyResp.Body.Close()
		}
	}

	// 2. Fallback: Parse JWT payload (sub: "CHARACTER:EVE:<id>", name: "<name>")
	parts := strings.Split(accessToken, ".")
	if len(parts) >= 2 {
		payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err == nil {
			var claims struct {
				Sub  string `json:"sub"`
				Name string `json:"name"`
			}
			if json.NewDecoder(bytes.NewReader(payloadBytes)).Decode(&claims) == nil {
				if strings.HasPrefix(claims.Sub, "CHARACTER:EVE:") {
					idStr := strings.TrimPrefix(claims.Sub, "CHARACTER:EVE:")
					if id, errID := strconv.Atoi(idStr); errID == nil && id > 0 {
						return id, claims.Name
					}
				}
			}
		}
	}

	return 0, ""
}

func fetchCharacterName(charID int) string {
	resp, err := http.Get(fmt.Sprintf("https://esi.evetech.net/latest/characters/%d/", charID))
	if err != nil {
		return fmt.Sprintf("Character ID: %d", charID)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("Character ID: %d", charID)
	}

	var data struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil || data.Name == "" {
		return fmt.Sprintf("Character ID: %d", charID)
	}

	return data.Name
}
