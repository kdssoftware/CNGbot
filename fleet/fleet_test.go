package fleet

import (
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"evemaildiscord/db"
	"evemaildiscord/esi"

	_ "github.com/mattn/go-sqlite3"
)

func TestIsJSpace(t *testing.T) {
	cases := map[int]bool{
		31000513: true,  // wormhole space
		30000142: false, // highsec (Jita)
		32000010: false, // Pochven (triglavian)
		0:        false,
		30999999: false,
	}
	for sysID, want := range cases {
		if got := isJSpace(sysID); got != want {
			t.Errorf("isJSpace(%d) = %v, want %v", sysID, got, want)
		}
	}
}

func TestGetFleetMembers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"character_id": 42, "role": "fleet_commander", "solar_system_id": 31000513},
			{"character_id": 99, "role": "fleet_member", "solar_system_id": 30000142}
		]`))
	}))
	defer server.Close()

	origBase := esiBase
	esiBase = server.URL
	defer func() { esiBase = origBase }()

	members, err := getFleetMembers(http.DefaultClient, 12345)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(members))
	}
	boss, ok := members[42]
	if !ok {
		t.Fatal("expected member with character_id 42")
	}
	if boss.Role != "fleet_commander" || boss.SolarSystemID != 31000513 {
		t.Errorf("unexpected member 42: %+v", boss)
	}
	member, ok := members[99]
	if !ok {
		t.Fatal("expected member with character_id 99")
	}
	if member.Role != "fleet_member" || member.SolarSystemID != 30000142 {
		t.Errorf("unexpected member 99: %+v", member)
	}
}

func TestSendInvite(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fleets/12345/wings/" {
			_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":9,"name":"Squad 1"}]}]`))
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	origBase := esiBase
	esiBase = server.URL
	defer func() { esiBase = origBase }()

	if err := sendInvite(http.DefaultClient, 12345, 7, 9, 42); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{`"character_id":42`, `"role":"squad_member"`, `"wing_id":7`, `"squad_id":9`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("expected body to contain %s, got: %s", want, gotBody)
		}
	}

	rejectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Character is not eligible for the fleet"}`))
	}))
	defer rejectServer.Close()
	esiBase = rejectServer.URL

	err := sendInvite(http.DefaultClient, 12345, 7, 9, 42)
	if err == nil {
		t.Error("expected error for 400 response, got nil")
	} else if !strings.Contains(err.Error(), "Character is not eligible for the fleet") {
		t.Errorf("expected error to include the ESI error body, got: %v", err)
	}
}

func TestGetFleetSquad(t *testing.T) {
	origBase := esiBase
	defer func() { esiBase = origBase }()

	t.Run("existing squad", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/fleets/12345/wings/" {
				t.Errorf("unexpected path %q", r.URL.Path)
			}
			_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":9,"name":"Squad 1"}]}]`))
		}))
		defer server.Close()
		esiBase = server.URL

		wingID, squadID, err := getFleetSquad(http.DefaultClient, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wingID != 7 || squadID != 9 {
			t.Errorf("expected wing 7 squad 9, got %d/%d", wingID, squadID)
		}
	})

	t.Run("creates squad when wing has none", func(t *testing.T) {
		var created bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/fleets/12345/wings/":
				if created {
					_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[{"id":11,"name":"Squad 2"}]}]`))
				} else {
					_, _ = w.Write([]byte(`[{"id":7,"name":"Wing 1","squads":[]}]`))
				}
			case "/fleets/12345/wings/7/squads/":
				created = true
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected path %q", r.URL.Path)
			}
		}))
		defer server.Close()
		esiBase = server.URL

		wingID, squadID, err := getFleetSquad(http.DefaultClient, 12345)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wingID != 7 || squadID != 11 {
			t.Errorf("expected wing 7 squad 11, got %d/%d", wingID, squadID)
		}
	})

	t.Run("wings error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()
		esiBase = server.URL

		if _, _, err := getFleetSquad(http.DefaultClient, 12345); err == nil {
			t.Error("expected error for 403 response, got nil")
		}
	})
}

func TestFilterByAlliance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"character_id":1,"alliance_id":99000001},{"character_id":2,"alliance_id":99000002}]`))
	}))
	defer server.Close()

	origURL := affiliationURL
	affiliationURL = server.URL
	defer func() { affiliationURL = origURL }()

	got, err := filterByAlliance(map[int]bool{1: true, 2: true, 3: true}, 99000001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !got[1] {
		t.Errorf("expected exactly {1: true}, got %v", got)
	}

	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errorServer.Close()
	affiliationURL = errorServer.URL

	if _, err := filterByAlliance(map[int]bool{1: true}, 99000001); err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestFetchESILocation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"ok", http.StatusOK, `{"solar_system_id":31000513}`, 31000513},
		{"offline", http.StatusNotFound, `{"error":"Character not found"}`, 0},
		{"server error", http.StatusInternalServerError, `{}`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/characters/12345/location/" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			origBase := esiBase
			esiBase = server.URL
			defer func() { esiBase = origBase }()

			got, err := fetchESILocation(server.Client(), 12345)
			if tc.status == http.StatusInternalServerError {
				if err == nil {
					t.Fatal("expected error for 500 response, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected solar system %d, got %d", tc.want, got)
			}
		})
	}
}

func setupFleetTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "fleet_test.db")
	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
		CREATE TABLE character_tokens (
			eve_character_id INTEGER PRIMARY KEY,
			refresh_token TEXT NOT NULL DEFAULT '',
			guild_id TEXT NOT NULL DEFAULT '',
			discord_user_id TEXT NOT NULL DEFAULT '',
			linked_at DATETIME
		);
		CREATE TABLE character_to_discord (
			eve_id INTEGER PRIMARY KEY,
			discord_id TEXT
		);
		CREATE TABLE config (
			guild_id TEXT NOT NULL DEFAULT '',
			key TEXT NOT NULL,
			value TEXT NOT NULL,
			PRIMARY KEY (guild_id, key)
		);
	`)
	if err != nil {
		t.Fatalf("failed to init test schema: %v", err)
	}

	oldDB := db.DB
	db.DB = database
	t.Cleanup(func() { db.DB = oldDB })
}

func TestResolveUserFleetBoss(t *testing.T) {
	t.Setenv("DASHBOARD_URL", "https://bot.example.com")

	t.Run("no linked characters prompts /link", func(t *testing.T) {
		setupFleetTestDB(t)

		client, fl, prompt := resolveUserFleetBoss("g1", "discord_1", fleetBuilderScopes)
		if client != nil || fl.FleetID != 0 {
			t.Fatalf("expected no fleet client, got client=%v fleet=%+v", client, fl)
		}
		if !strings.Contains(prompt, "/auth/eve/login") || !strings.Contains(prompt, "link_token=") {
			t.Fatalf("expected /link URL in prompt, got: %s", prompt)
		}
	})

	t.Run("missing scope prompts /link with missing scope name", func(t *testing.T) {
		setupFleetTestDB(t)
		if err := db.SetCharacterToken(1001, "tok_missing_write", "g1", "discord_1"); err != nil {
			t.Fatalf("failed to insert token: %v", err)
		}

		origOAuth := oauthClientForScopes
		oauthClientForScopes = func(refreshToken string, requiredScopes ...string) (*http.Client, error) {
			return nil, &esi.MissingScopesError{Missing: []string{"esi-fleets.write_fleet.v1"}}
		}
		defer func() { oauthClientForScopes = origOAuth }()

		client, _, prompt := resolveUserFleetBoss("g1", "discord_1", fleetBuilderScopes)
		if client != nil {
			t.Fatalf("expected nil client")
		}
		if !strings.Contains(prompt, "esi-fleets.write_fleet.v1") || !strings.Contains(prompt, "/auth/eve/login") {
			t.Fatalf("expected missing scope and /link URL in prompt, got: %s", prompt)
		}
	})

	t.Run("selects fleet boss alt when another alt lacks scope or fleet", func(t *testing.T) {
		setupFleetTestDB(t)
		if err := db.SetCharacterToken(1001, "tok_alt1_missing", "g1", "discord_1"); err != nil {
			t.Fatalf("failed to insert alt 1: %v", err)
		}
		if err := db.SetCharacterToken(1002, "tok_alt2_boss", "g1", "discord_1"); err != nil {
			t.Fatalf("failed to insert alt 2: %v", err)
		}

		esiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/characters/1002/fleet/" {
				_, _ = w.Write([]byte(`{"fleet_id":555,"fleet_boss_id":1002,"role":"fleet_commander"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer esiSrv.Close()

		origBase := esiBase
		esiBase = esiSrv.URL
		defer func() { esiBase = origBase }()

		origOAuth := oauthClientForScopes
		oauthClientForScopes = func(refreshToken string, requiredScopes ...string) (*http.Client, error) {
			if refreshToken == "tok_alt1_missing" {
				return nil, &esi.MissingScopesError{Missing: []string{"esi-fleets.write_fleet.v1"}}
			}
			return esiSrv.Client(), nil
		}
		defer func() { oauthClientForScopes = origOAuth }()

		client, fl, prompt := resolveUserFleetBoss("g1", "discord_1", fleetBuilderScopes)
		if prompt != "" || client == nil || fl.FleetID != 555 || fl.FleetBossID != 1002 {
			t.Fatalf("expected alt 2 fleet boss, got fl=%+v prompt=%q", fl, prompt)
		}
	})

	t.Run("in fleet but not boss returns clear message", func(t *testing.T) {
		setupFleetTestDB(t)
		if err := db.SetCharacterToken(1001, "tok_member", "g1", "discord_1"); err != nil {
			t.Fatalf("failed to insert token: %v", err)
		}

		esiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"fleet_id":555,"fleet_boss_id":9999,"role":"squad_member"}`))
		}))
		defer esiSrv.Close()

		origBase := esiBase
		esiBase = esiSrv.URL
		defer func() { esiBase = origBase }()

		origOAuth := oauthClientForScopes
		oauthClientForScopes = func(refreshToken string, requiredScopes ...string) (*http.Client, error) {
			return esiSrv.Client(), nil
		}
		defer func() { oauthClientForScopes = origOAuth }()

		client, _, prompt := resolveUserFleetBoss("g1", "discord_1", fleetBuilderScopes)
		if client != nil || !strings.Contains(prompt, "Fleet Boss") {
			t.Fatalf("expected Fleet Boss error, got client=%v prompt=%q", client, prompt)
		}
	})
}

func TestFleetBuilder_ForbiddenError(t *testing.T) {
	t.Run("BuildFleetFromTemplate 403", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"missing scope"}`))
		}))
		defer srv.Close()

		origBase := esiBase
		esiBase = srv.URL
		defer func() { esiBase = origBase }()

		err := BuildFleetFromTemplate(srv.Client(), 12345, FleetTemplate{
			Name:  "PVP",
			Wings: []WingTemplate{{Name: "Main", Squads: []string{"DPS"}}},
		})
		if err == nil || !isForbiddenError(err) {
			t.Fatalf("expected forbidden error, got %v", err)
		}
	})

	t.Run("MoveMembersToFirstSquad 403 on move", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/fleets/12345/wings/":
				_, _ = w.Write([]byte(`[{"id":1,"name":"W1","squads":[{"id":10,"name":"S1"}]}]`))
			case r.Method == http.MethodGet && r.URL.Path == "/fleets/12345/members/":
				_, _ = w.Write([]byte(`[{"character_id":200,"role":"squad_member","wing_id":2,"squad_id":20}]`))
			case r.Method == http.MethodPut:
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()

		origBase := esiBase
		esiBase = srv.URL
		defer func() { esiBase = origBase }()

		err := MoveMembersToFirstSquad(srv.Client(), ownFleet{FleetID: 12345, FleetBossID: 100})
		if err == nil || !isForbiddenError(err) {
			t.Fatalf("expected forbidden error from MoveMembersToFirstSquad, got %v", err)
		}
	})
}

func TestFleetTemplateValidateAndSave(t *testing.T) {
	setupFleetTestDB(t)

	valid := FleetTemplate{
		Name: "PVP",
		Wings: []WingTemplate{
			{Name: "1234567890", Squads: []string{"1234567890", "Logi"}},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected 10-char names to be valid, got: %v", err)
	}
	if err := SaveFleetTemplate("g1", valid); err != nil {
		t.Fatalf("expected SaveFleetTemplate to succeed for valid template, got: %v", err)
	}

	cases := []struct {
		name string
		tpl  FleetTemplate
	}{
		{
			name: "wing name over 10 chars",
			tpl: FleetTemplate{
				Name:  "BadWing",
				Wings: []WingTemplate{{Name: "12345678901", Squads: []string{"DPS"}}},
			},
		},
		{
			name: "squad name over 10 chars",
			tpl: FleetTemplate{
				Name:  "BadSquad",
				Wings: []WingTemplate{{Name: "Main", Squads: []string{"12345678901"}}},
			},
		},
		{
			name: "empty wing name",
			tpl: FleetTemplate{
				Name:  "EmptyWing",
				Wings: []WingTemplate{{Name: "   ", Squads: []string{"DPS"}}},
			},
		},
		{
			name: "empty squad name",
			tpl: FleetTemplate{
				Name:  "EmptySquad",
				Wings: []WingTemplate{{Name: "Main", Squads: []string{""}}},
			},
		},
		{
			name: "too many wings",
			tpl: FleetTemplate{
				Name: "SixWings",
				Wings: []WingTemplate{
					{Name: "W1"}, {Name: "W2"}, {Name: "W3"}, {Name: "W4"}, {Name: "W5"}, {Name: "W6"},
				},
			},
		},
		{
			name: "too many squads",
			tpl: FleetTemplate{
				Name:  "SixSquads",
				Wings: []WingTemplate{{Name: "W1", Squads: []string{"S1", "S2", "S3", "S4", "S5", "S6"}}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tpl.Validate(); err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			if err := SaveFleetTemplate("g1", tc.tpl); err == nil {
				t.Fatalf("expected SaveFleetTemplate to reject %s, got nil", tc.name)
			}
		})
	}
}
