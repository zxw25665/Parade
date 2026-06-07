package app

import (
	"crypto/sha256"

	"github.com/google/uuid"
)

// Namespace UUIDs for deterministic derivation.
// Different namespaces ensure IDs for different entity types never collide.
var (
	identityNS     = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")
	teamNS         = uuid.MustParse("6ba7b812-9dad-11d1-80b4-00c04fd430c8")
	shareGroupNS   = uuid.MustParse("6ba7b814-9dad-11d1-80b4-00c04fd430c8")
	conversationNS = uuid.MustParse("6ba7b815-9dad-11d1-80b4-00c04fd430c8")
)

// deriveUUID returns a deterministic UUID v5 (SHA-256) from a namespace and data.
func deriveUUID(ns uuid.UUID, data []byte) string {
	return uuid.NewHash(sha256.New(), ns, data, 5).String()
}

// DeriveTeamConvID returns a deterministic conversation ID for a team chat.
// Same team UUID always produces the same conversation ID across all devices.
func DeriveTeamConvID(teamID string) string {
	hash := sha256.Sum256([]byte("team:" + teamID))
	return uuid.NewHash(sha256.New(), conversationNS, hash[:], 5).String()
}

// DerivePrivateConvID returns a deterministic conversation ID for a private chat.
// Input order is commutative: same pair of pubkeys always produces the same ID.
func DerivePrivateConvID(myPubKey, peerPubKey string) string {
	a, b := myPubKey, peerPubKey
	if a > b {
		a, b = b, a
	}
	hash := sha256.Sum256([]byte("private:" + a + ":" + b))
	return uuid.NewHash(sha256.New(), conversationNS, hash[:], 5).String()
}
