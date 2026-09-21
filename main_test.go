package main

import (
	"testing"
)

func TestGetCommitHash(t *testing.T) {
	hash := getCommitHash()
	if hash == "" {
		t.Errorf("expected non-empty commit hash, got empty string")
	}
}
