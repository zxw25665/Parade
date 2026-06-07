package app

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
)

func TestDeriveTeamConvID_Deterministic(t *testing.T) {
	teamID := "test-team-123"
	id1 := DeriveTeamConvID(teamID)
	id2 := DeriveTeamConvID(teamID)
	if id1 != id2 {
		t.Errorf("same teamID should produce same convID: %s != %s", id1, id2)
	}
	if _, err := uuid.Parse(id1); err != nil {
		t.Errorf("output is not a valid UUID: %v", err)
	}
}

func TestDeriveTeamConvID_DifferentTeams(t *testing.T) {
	id1 := DeriveTeamConvID("team-a")
	id2 := DeriveTeamConvID("team-b")
	if id1 == id2 {
		t.Errorf("different teams should produce different convIDs: %s", id1)
	}
}

func TestDerivePrivateConvID_Deterministic(t *testing.T) {
	id1 := DerivePrivateConvID("pubkey-alice", "pubkey-bob")
	id2 := DerivePrivateConvID("pubkey-alice", "pubkey-bob")
	if id1 != id2 {
		t.Errorf("same peers should produce same convID: %s != %s", id1, id2)
	}
	if _, err := uuid.Parse(id1); err != nil {
		t.Errorf("output is not a valid UUID: %v", err)
	}
}

func TestDerivePrivateConvID_Commutative(t *testing.T) {
	id1 := DerivePrivateConvID("alice", "bob")
	id2 := DerivePrivateConvID("bob", "alice")
	if id1 != id2 {
		t.Errorf("order should not matter: %s != %s", id1, id2)
	}
}

func TestDerivePrivateConvID_DifferentPeers(t *testing.T) {
	id1 := DerivePrivateConvID("alice", "bob")
	id2 := DerivePrivateConvID("alice", "carol")
	if id1 == id2 {
		t.Errorf("different peers should produce different convIDs: %s", id1)
	}
}

func TestDerivePrivateVsTeam_NoCollision(t *testing.T) {
	teamID := "some-team-id"
	privID := DerivePrivateConvID("some-team-id", "bob")
	teamConvID := DeriveTeamConvID(teamID)
	if privID == teamConvID {
		t.Errorf("private and team convIDs should not collide: %s", privID)
	}
}

func TestConvID_IsUUIDv5(t *testing.T) {
	id := DeriveTeamConvID("foo")
	parsed, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("not a valid UUID: %v", err)
	}
	if parsed.Version() != 5 {
		t.Errorf("expected UUID v5, got v%d", parsed.Version())
	}
}

func TestDerivePrivateConvID_SamePeerRejected(t *testing.T) {
	// Should still produce a valid UUID even with same key
	id := DerivePrivateConvID("same", "same")
	if _, err := uuid.Parse(id); err != nil {
		t.Errorf("output is not a valid UUID: %v", err)
	}
	if id == "" {
		t.Error("should produce non-empty UUID even with identical keys")
	}
}

// verify the derivation uses SHA-256, not raw input
func TestDeriveTeamConvID_UsesHash(t *testing.T) {
	// If raw input were used, hash of the same data would differ
	h := sha256.Sum256([]byte("team:hello"))
	direct := uuid.NewHash(sha256.New(), conversationNS, h[:], 5).String()
	derived := DeriveTeamConvID("hello")
	if direct != derived {
		t.Errorf("derivation differs from expected SHA-256 hash method: %s != %s", direct, derived)
	}
}
