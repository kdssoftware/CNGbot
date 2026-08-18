//go:build ignore

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"evemaildiscord/config"

	"golang.org/x/oauth2"
)

func main() {
	clientID := config.GetEveClientID()
	clientSecret := config.GetEveSecret()
	if clientID == "" || clientSecret == "" {
		log.Fatal("EVE_CLIENT_ID and EVE_SECRET (or EVE_CLIENT_SECRET) must be set in environment or .env file")
	}

	conf := &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes: []string{
			// comment out for scopes that are not needed
			// for ease of use, enable everything
			"esi-characters.read_blueprints.v1",
			"esi-location.read_online.v1",
			"publicData",
			"esi-calendar.respond_calendar_events.v1",
			"esi-calendar.read_calendar_events.v1",
			"esi-location.read_location.v1",
			"esi-location.read_ship_type.v1",
			"esi-mail.organize_mail.v1",
			"esi-mail.read_mail.v1",
			"esi-mail.send_mail.v1",
			"esi-skills.read_skills.v1",
			"esi-skills.read_skillqueue.v1",
			"esi-wallet.read_character_wallet.v1",
			"esi-wallet.read_corporation_wallet.v1",
			"esi-search.search_structures.v1",
			"esi-clones.read_clones.v1",
			"esi-characters.read_contacts.v1",
			"esi-universe.read_structures.v1",
			"esi-killmails.read_killmails.v1",
			"esi-corporations.read_corporation_membership.v1",
			"esi-assets.read_assets.v1",
			"esi-planets.manage_planets.v1",
			"esi-fleets.read_fleet.v1",
			"esi-fleets.write_fleet.v1",
			"esi-ui.open_window.v1",
			"esi-ui.write_waypoint.v1",
			"esi-characters.write_contacts.v1",
			"esi-fittings.read_fittings.v1",
			"esi-fittings.write_fittings.v1",
			"esi-markets.structure_markets.v1",
			"esi-corporations.read_structures.v1",
			"esi-characters.read_loyalty.v1",
			"esi-characters.read_chat_channels.v1",
			"esi-characters.read_medals.v1",
			"esi-characters.read_standings.v1",
			"esi-characters.read_agents_research.v1",
			"esi-industry.read_character_jobs.v1",
			"esi-markets.read_character_orders.v1",
			"esi-characters.read_corporation_roles.v1",
			"esi-contracts.read_character_contracts.v1",
			"esi-clones.read_implants.v1",
			"esi-characters.read_fatigue.v1",
			"esi-killmails.read_corporation_killmails.v1",
			"esi-corporations.track_members.v1",
			"esi-wallet.read_corporation_wallets.v1",
			"esi-characters.read_notifications.v1",
			"esi-corporations.read_divisions.v1",
			"esi-corporations.read_contacts.v1",
			"esi-assets.read_corporation_assets.v1",
			"esi-corporations.read_titles.v1",
			"esi-corporations.read_blueprints.v1",
			"esi-contracts.read_corporation_contracts.v1",
			"esi-corporations.read_standings.v1",
			"esi-corporations.read_starbases.v1",
			"esi-industry.read_corporation_jobs.v1",
			"esi-markets.read_corporation_orders.v1",
			"esi-corporations.read_container_logs.v1",
			"esi-industry.read_character_mining.v1",
			"esi-industry.read_corporation_mining.v1",
			"esi-planets.read_customs_offices.v1",
			"esi-corporations.read_facilities.v1",
			"esi-corporations.read_medals.v1",
			"esi-characters.read_titles.v1",
			"esi-alliances.read_contacts.v1",
			"esi-characters.read_fw_stats.v1",
			"esi-corporations.read_fw_stats.v1",
			"esi-corporations.read_projects.v1",
			"esi-corporations.read_freelance_jobs.v1",
			"esi-characters.read_freelance_jobs.v1",
			"esi-structures.read_corporation.v1",
			"esi-structures.read_character.v1",
			"esi-activities.read_character.v1",
			"esi-access.read_lists.v1",
		},
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://login.eveonline.com/v2/oauth/authorize",
			TokenURL: "https://login.eveonline.com/v2/oauth/token",
		},
		RedirectURL: "http://localhost:7887/callback",
	}

	url := conf.AuthCodeURL("random-state-string", oauth2.AccessTypeOffline)

	fmt.Println("=================================================================")
	fmt.Printf("1. Open this link in your browser:\n\n%v\n\n", url)
	fmt.Println("2. Log in with your EVE character and click 'Authorize'.")
	fmt.Println("3. Waiting for you to be redirected to localhost:7887/callback...")
	fmt.Println("=================================================================")

	http.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			fmt.Fprintf(w, "No code found in the URL. Did you cancel the login?")
			return
		}

		token, err := conf.Exchange(context.Background(), code)
		if err != nil {
			fmt.Fprintf(w, "Error exchanging code: %v", err)
			return
		}

		fmt.Fprintf(w, "Success! You can close this browser window now. Check your terminal for the Refresh Token.")

		fmt.Printf("\n\nSUCCESS!\n")
		fmt.Printf("Your Refresh Token is:\n\n%s\n\n", token.RefreshToken)
		fmt.Printf("Copy this token, set it as REFRESH_TOKEN in your .env file, and press Ctrl+C to close this script.\n")
	})

	log.Fatal(http.ListenAndServe(":7887", nil))
}
