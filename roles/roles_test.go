package roles

import (
	"testing"
)

func TestDetermineStandingType(t *testing.T) {
	tests := []struct {
		name     string
		standing float64
		expected string
	}{
		{"Excellent max", 10.0, StandingExcellent},
		{"Excellent min", 5.0, StandingExcellent},
		{"Good upper", 4.9, StandingGood},
		{"Good lower", 0.1, StandingGood},
		{"Neutral exact", 0.0, StandingNeutral},
		{"Bad upper", -0.1, StandingBad},
		{"Bad lower", -4.9, StandingBad},
		{"Terrible upper", -5.0, StandingTerrible},
		{"Terrible min", -10.0, StandingTerrible},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetermineStandingType(tt.standing)
			if result != tt.expected {
				t.Errorf("DetermineStandingType(%f) = %s; expected %s", tt.standing, result, tt.expected)
			}
		})
	}
}

func TestResolveStanding(t *testing.T) {
	standings := map[int]float64{
		1001: 10.0,  // Character
		2001: 5.0,   // Corp
		3001: -5.0,  // Alliance
		4001: -10.0, // Faction
	}

	aff1 := EsiCharacterAffiliation{
		CharacterID:   1001,
		CorporationID: 2001,
		AllianceID:    3001,
	}
	if val := ResolveStanding(standings, aff1); val != 10.0 {
		t.Errorf("expected 10.0 (char standing), got %f", val)
	}

	aff2 := EsiCharacterAffiliation{
		CharacterID:   1002,
		CorporationID: 2001,
		AllianceID:    3001,
	}
	if val := ResolveStanding(standings, aff2); val != 5.0 {
		t.Errorf("expected 5.0 (corp standing), got %f", val)
	}

	aff3 := EsiCharacterAffiliation{
		CharacterID:   1002,
		CorporationID: 2002,
		AllianceID:    3001,
	}
	if val := ResolveStanding(standings, aff3); val != -5.0 {
		t.Errorf("expected -5.0 (alliance standing), got %f", val)
	}

	aff4 := EsiCharacterAffiliation{
		CharacterID:   1002,
		CorporationID: 2002,
		AllianceID:    0,
		FactionID:     4001,
	}
	if val := ResolveStanding(standings, aff4); val != -10.0 {
		t.Errorf("expected -10.0 (faction standing), got %f", val)
	}

	aff5 := EsiCharacterAffiliation{
		CharacterID:   9999,
		CorporationID: 8888,
		AllianceID:    7777,
	}
	if val := ResolveStanding(standings, aff5); val != 0.0 {
		t.Errorf("expected 0.0 (neutral default), got %f", val)
	}
}

func TestEsiStandingID(t *testing.T) {
	s1 := EsiStanding{ContactID: 12345}
	if s1.ID() != 12345 {
		t.Errorf("expected 12345, got %d", s1.ID())
	}

	s2 := EsiStanding{FromID: 67890}
	if s2.ID() != 67890 {
		t.Errorf("expected 67890, got %d", s2.ID())
	}
}

func TestGenerate2FACode(t *testing.T) {
	code, err := Generate2FACode()
	if err != nil {
		t.Fatalf("unexpected error generating 2FA code: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code, got length %d (%s)", len(code), code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Fatalf("expected numeric digit, got character '%c' in code %s", c, code)
		}
	}
}

func TestHasRole(t *testing.T) {
	roles := []string{"111", "222", "333"}
	if !HasRole(roles, "222") {
		t.Errorf("expected true for existing role 222")
	}
	if HasRole(roles, "444") {
		t.Errorf("expected false for missing role 444")
	}
}
