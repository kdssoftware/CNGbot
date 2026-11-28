package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"evemaildiscord/db"
	"evemaildiscord/esi"
	"evemaildiscord/web"

	"github.com/bwmarrin/discordgo"
)

var fleetBuilderScopes = []string{
	"esi-fleets.read_fleet.v1",
	"esi-fleets.write_fleet.v1",
}

// Package-level hook so unit tests can stub OAuth client creation without hitting EVE SSO.
var oauthClientForScopes = esi.GetOAuthHTTPClientForScopes

type esiStatusError struct {
	StatusCode int
	Operation  string
	Body       string
}

func (e *esiStatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("%s failed with status %d: %s", e.Operation, e.StatusCode, e.Body)
	}
	return fmt.Sprintf("%s failed with status %d", e.Operation, e.StatusCode)
}

func isForbiddenError(err error) bool {
	var statusErr *esiStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusForbidden
}

func readErrorBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 512))
	return strings.TrimSpace(string(b))
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i == nil {
		return ""
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func formatLinkPrompt(guildID, discordUserID, reason string) string {
	link := web.BuildCharacterLinkURL(guildID, discordUserID)
	return fmt.Sprintf("%s\nPlease authorize your EVE character via `/link` (or use this link directly): %s", reason, link)
}

// resolveUserFleetBoss finds a linked EVE character belonging to discordUserID
// in guildID that has requiredScopes and is currently the Fleet Boss of an
// active fleet. On failure, prompt contains an actionable user-facing message.
func resolveUserFleetBoss(guildID, discordUserID string, requiredScopes []string) (*http.Client, ownFleet, string) {
	tokens, err := db.GetUserCharacterTokens(guildID, discordUserID)
	if err != nil {
		return nil, ownFleet{}, fmt.Sprintf("Failed to look up your linked EVE characters: %v", err)
	}
	if len(tokens) == 0 {
		return nil, ownFleet{}, formatLinkPrompt(guildID, discordUserID, "You don't have an EVE character linked for this server yet.")
	}

	var (
		inFleetNotBoss bool
		hadValidToken  bool
		hadForbidden   bool
		hadAuthErr     bool
		missingScopes  []string
	)

	for _, tok := range tokens {
		client, err := oauthClientForScopes(tok.RefreshToken, requiredScopes...)
		if err != nil {
			var missingErr *esi.MissingScopesError
			if errors.As(err, &missingErr) {
				missingScopes = missingErr.Missing
			} else {
				hadAuthErr = true
			}
			continue
		}

		fl, status, err := GetOwnFleet(client, tok.EveCharacterID)
		if err != nil {
			if status == http.StatusForbidden {
				hadForbidden = true
			} else {
				hadAuthErr = true
			}
			continue
		}

		hadValidToken = true
		if fl.FleetID == 0 {
			continue
		}
		if fl.FleetBossID == int64(tok.EveCharacterID) {
			return client, fl, ""
		}
		inFleetNotBoss = true
	}

	switch {
	case inFleetNotBoss:
		return nil, ownFleet{}, "You must be the Fleet Boss in-game to structure the fleet."
	case hadValidToken:
		return nil, ownFleet{}, "You must be in an active EVE fleet as Fleet Boss to use this command."
	case len(missingScopes) > 0:
		return nil, ownFleet{}, formatLinkPrompt(guildID, discordUserID, fmt.Sprintf("Your linked EVE character token is missing required ESI scope(s): `%s`.", strings.Join(missingScopes, "`, `")))
	case hadForbidden:
		return nil, ownFleet{}, formatLinkPrompt(guildID, discordUserID, "Your linked EVE character token does not have the required fleet permissions (ESI returned 403).")
	case hadAuthErr:
		return nil, ownFleet{}, formatLinkPrompt(guildID, discordUserID, "Failed to authenticate your linked EVE character with EVE SSO.")
	default:
		return nil, ownFleet{}, formatLinkPrompt(guildID, discordUserID, "Unable to authorize your linked EVE character for fleet management.")
	}
}

// ==========================================
// 1. High-Level Orchestrator
// ==========================================

// BuildFleetFromTemplate synchronizes the live EVE fleet to the JSON template.
func BuildFleetFromTemplate(client *http.Client, fleetID int64, tpl FleetTemplate) error {
	if err := tpl.Validate(); err != nil {
		return err
	}

	// Fetch current state
	currentWings, err := fetchCurrentLayout(client, fleetID)
	if err != nil {
		return err
	}

	// Align wing count (create missing / delete excess)
	currentWings, err = syncWingCount(client, fleetID, currentWings, len(tpl.Wings))
	if err != nil {
		return err
	}

	// Sync names and squads for each wing
	for i, targetWing := range tpl.Wings {
		wing := currentWings[i]

		// Rename wing if it differs
		if wing.Name != targetWing.Name {
			url := fmt.Sprintf("%s/fleets/%d/wings/%d/", esiBase, fleetID, wing.ID)
			if err := renameFleetNode(client, url, targetWing.Name); err != nil {
				return fmt.Errorf("failed to rename wing %d: %w", i+1, err)
			}
		}

		// Align squad count for this wing
		wing.Squads, err = syncSquadCount(client, fleetID, wing.ID, wing.Squads, len(targetWing.Squads))
		if err != nil {
			return err
		}

		// Rename squads if they differ
		for j, targetSquadName := range targetWing.Squads {
			squad := wing.Squads[j]
			if squad.Name != targetSquadName {
				url := fmt.Sprintf("%s/fleets/%d/squads/%d/", esiBase, fleetID, squad.ID)
				if err := renameFleetNode(client, url, targetSquadName); err != nil {
					return fmt.Errorf("failed to rename squad %d in wing %d: %w", j+1, i+1, err)
				}
			}
		}
	}

	return nil
}

// ==========================================
// 2. State Alignment Logic
// ==========================================

func syncWingCount(client *http.Client, fleetID int64, current []esiWing, targetCount int) ([]esiWing, error) {
	// Delete excess wings (from the bottom up)
	for len(current) > targetCount {
		lastIdx := len(current) - 1
		wingID := current[lastIdx].ID

		if err := deleteNode(client, fmt.Sprintf("%s/fleets/%d/wings/%d/", esiBase, fleetID, wingID)); err != nil {
			return nil, fmt.Errorf("failed to delete wing: %w", err)
		}
		current = current[:lastIdx]
	}

	// Create missing wings
	for len(current) < targetCount {
		newID, err := createNode(client, fmt.Sprintf("%s/fleets/%d/wings/", esiBase, fleetID), "wing_id")
		if err != nil {
			return nil, fmt.Errorf("failed to create wing: %w", err)
		}
		current = append(current, esiWing{ID: newID, Squads: []esiSquad{}}) // Start with empty squads
	}

	return current, nil
}

func syncSquadCount(client *http.Client, fleetID, wingID int64, current []esiSquad, targetCount int) ([]esiSquad, error) {
	// Delete excess squads
	for len(current) > targetCount {
		lastIdx := len(current) - 1
		squadID := current[lastIdx].ID

		if err := deleteNode(client, fmt.Sprintf("%s/fleets/%d/squads/%d/", esiBase, fleetID, squadID)); err != nil {
			return nil, fmt.Errorf("failed to delete squad: %w", err)
		}
		current = current[:lastIdx]
	}

	// Create missing squads
	for len(current) < targetCount {
		newID, err := createNode(client, fmt.Sprintf("%s/fleets/%d/wings/%d/squads/", esiBase, fleetID, wingID), "squad_id")
		if err != nil {
			return nil, fmt.Errorf("failed to create squad: %w", err)
		}
		current = append(current, esiSquad{ID: newID})
	}

	return current, nil
}

// MoveMembersToFirstSquad sweeps all standard fleet members into Wing 1, Squad 1.
func MoveMembersToFirstSquad(client *http.Client, fleet ownFleet) error {
	wings, err := fetchCurrentLayout(client, fleet.FleetID)
	if err != nil {
		return fmt.Errorf("failed to fetch wings for movement: %w", err)
	}

	if len(wings) == 0 || len(wings[0].Squads) == 0 {
		return nil
	}

	targetWingID := wings[0].ID
	targetSquadID := wings[0].Squads[0].ID

	members, err := fetchFleetMembers(client, fleet.FleetID)
	if err != nil {
		return fmt.Errorf("failed to fetch fleet members: %w", err)
	}

	for _, member := range members {
		// Preserve the Fleet Boss
		if member.CharacterID == fleet.FleetBossID {
			err := moveFleetMember(client, fleet.FleetID, member.CharacterID, 0, 0, "fleet_commander")
			if err != nil {
				if isForbiddenError(err) {
					return err
				}
				fmt.Printf("Warning: Failed to move character %d: %v\n", member.CharacterID, err)
			}
			continue
		}

		if member.SquadID == targetSquadID {
			continue
		}

		err := moveFleetMember(client, fleet.FleetID, member.CharacterID, targetWingID, targetSquadID, "squad_member")
		if err != nil {
			if isForbiddenError(err) {
				return err
			}
			fmt.Printf("Warning: Failed to move character %d: %v\n", member.CharacterID, err)
		}
	}

	return nil
}

// ==========================================
// 3. ESI Network Wrappers
// ==========================================

func fetchCurrentLayout(client *http.Client, fleetID int64) ([]esiWing, error) {
	resp, err := client.Get(fmt.Sprintf("%s/fleets/%d/wings/", esiBase, fleetID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, &esiStatusError{StatusCode: resp.StatusCode, Operation: "fetch wings", Body: readErrorBody(resp.Body)}
	}

	var wings []esiWing
	if err := json.NewDecoder(resp.Body).Decode(&wings); err != nil {
		return nil, err
	}
	return wings, nil
}

// createNode sends a POST to ESI and extracts the resulting ID (works for both wings and squads)
func createNode(client *http.Client, url, idKey string) (int64, error) {
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 201 {
		return 0, &esiStatusError{StatusCode: resp.StatusCode, Operation: "creation", Body: readErrorBody(resp.Body)}
	}

	var result map[string]int64
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result[idKey], nil
}

func deleteNode(client *http.Client, url string) error {
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return &esiStatusError{StatusCode: resp.StatusCode, Operation: "deletion", Body: readErrorBody(resp.Body)}
	}
	return nil
}

func renameFleetNode(client *http.Client, url, name string) error {
	body, _ := json.Marshal(map[string]string{"name": name})
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return &esiStatusError{StatusCode: resp.StatusCode, Operation: "rename", Body: readErrorBody(resp.Body)}
	}
	return nil
}

func fetchFleetMembers(client *http.Client, fleetID int64) ([]esiMember, error) {
	resp, err := client.Get(fmt.Sprintf("%s/fleets/%d/members/", esiBase, fleetID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, &esiStatusError{StatusCode: resp.StatusCode, Operation: "fetch fleet members", Body: readErrorBody(resp.Body)}
	}

	var members []esiMember
	if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
		return nil, err
	}
	return members, nil
}

func moveFleetMember(client *http.Client, fleetID, charID, wingID, squadID int64, role string) error {
	url := fmt.Sprintf("%s/fleets/%d/members/%d/", esiBase, fleetID, charID)

	payload := map[string]interface{}{
		"role": role,
	}

	if wingID > 0 {
		payload["wing_id"] = wingID
	}
	if squadID > 0 {
		payload["squad_id"] = squadID
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 204 {
		return &esiStatusError{StatusCode: resp.StatusCode, Operation: "move fleet member", Body: readErrorBody(resp.Body)}
	}
	return nil
}
