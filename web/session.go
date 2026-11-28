package web

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	SessionCookieName = "cngbot_session"
	SessionDuration   = 7 * 24 * time.Hour
)

type DiscordGuildInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions string `json:"permissions"`
	HasAdmin    bool   `json:"has_admin"`
	BotInGuild  bool   `json:"bot_in_guild"`
}

type UserSession struct {
	ID          string                      `json:"id"`
	UserID      string                      `json:"user_id"`
	Username    string                      `json:"username"`
	Avatar      string                      `json:"avatar"`
	AccessToken string                      `json:"access_token"`
	AdminGuilds map[string]DiscordGuildInfo `json:"admin_guilds"`
	CreatedAt   time.Time                   `json:"created_at"`
	ExpiresAt   time.Time                   `json:"expires_at"`
}

type SessionStore struct {
	mu          sync.RWMutex
	sessions    map[string]*UserSession
	oauthStates map[string]OAuthStateData
	linkTickets map[string]LinkTicket
}

type OAuthStateData struct {
	SessionID     string
	GuildID       string
	Provider      string
	Mode          string
	DiscordUserID string
	CreatedAt     time.Time
}

type LinkTicket struct {
	GuildID       string
	DiscordUserID string
	CreatedAt     time.Time
}

var Store = &SessionStore{
	sessions:    make(map[string]*UserSession),
	oauthStates: make(map[string]OAuthStateData),
	linkTickets: make(map[string]LinkTicket),
}

func GenerateRandomToken(bytesLen int) (string, error) {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SessionStore) CreateSession(userID, username, avatar, accessToken string, adminGuilds map[string]DiscordGuildInfo) (*UserSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID, err := GenerateRandomToken(32)
	if err != nil {
		return nil, err
	}

	session := &UserSession{
		ID:          sessionID,
		UserID:      userID,
		Username:    username,
		Avatar:      avatar,
		AccessToken: accessToken,
		AdminGuilds: adminGuilds,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(SessionDuration),
	}

	s.sessions[sessionID] = session
	return session, nil
}

func (s *SessionStore) GetSession(r *http.Request) *UserSession {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[cookie.Value]
	if !exists {
		return nil
	}

	if time.Now().After(session.ExpiresAt) {
		return nil
	}

	return session
}

func (s *SessionStore) DeleteSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		s.mu.Lock()
		delete(s.sessions, cookie.Value)
		s.mu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *SessionStore) SetSessionCookie(w http.ResponseWriter, sessionID string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *SessionStore) SaveOAuthState(state, sessionID, guildID, provider, mode string, discordUserID ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up old states (>15 mins)
	now := time.Now()
	for k, v := range s.oauthStates {
		if now.Sub(v.CreatedAt) > 15*time.Minute {
			delete(s.oauthStates, k)
		}
	}

	userID := ""
	if len(discordUserID) > 0 {
		userID = discordUserID[0]
	}

	s.oauthStates[state] = OAuthStateData{
		SessionID:     sessionID,
		GuildID:       guildID,
		Provider:      provider,
		Mode:          mode,
		DiscordUserID: userID,
		CreatedAt:     now,
	}
}

// CreateLinkTicket mints a short-lived server-side ticket binding a /link URL
// to a specific Discord user and guild.
func (s *SessionStore) CreateLinkTicket(guildID, discordUserID string) (string, error) {
	ticket, err := GenerateRandomToken(16)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.linkTickets == nil {
		s.linkTickets = make(map[string]LinkTicket)
	}

	now := time.Now()
	for k, v := range s.linkTickets {
		if now.Sub(v.CreatedAt) > 15*time.Minute {
			delete(s.linkTickets, k)
		}
	}

	s.linkTickets[ticket] = LinkTicket{
		GuildID:       guildID,
		DiscordUserID: discordUserID,
		CreatedAt:     now,
	}
	return ticket, nil
}

// ResolveLinkTicket returns the Discord user ID bound to ticket for guildID if
// valid and unexpired. It is non-consuming within its 15-minute window so a
// user can link multiple alts from one /link prompt.
func (s *SessionStore) ResolveLinkTicket(ticket, guildID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, exists := s.linkTickets[ticket]
	if !exists {
		return "", false
	}
	if time.Since(data.CreatedAt) > 15*time.Minute {
		return "", false
	}
	if data.GuildID != guildID {
		return "", false
	}
	return data.DiscordUserID, true
}

func (s *SessionStore) ValidateAndConsumeOAuthState(state, expectedProvider string) (OAuthStateData, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, exists := s.oauthStates[state]
	if !exists {
		return OAuthStateData{}, false
	}

	delete(s.oauthStates, state)

	if time.Since(data.CreatedAt) > 15*time.Minute {
		return OAuthStateData{}, false
	}

	if expectedProvider != "" && data.Provider != expectedProvider {
		return OAuthStateData{}, false
	}

	return data, true
}
