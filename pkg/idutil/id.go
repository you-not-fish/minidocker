// Package idutil provides utilities for container ID generation and manipulation.
//
// Container IDs in minidocker follow Docker conventions:
//   - Full ID: 64-character hexadecimal string
//   - Short ID: First 12 characters of the full ID
//   - Minimum prefix for lookup: 3 characters
package idutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

const (
	// FullIDLength is the length of a full container ID (64 hex characters = 32 bytes).
	FullIDLength = 64

	// ShortIDLength is the standard short ID length (12 characters, Docker convention).
	ShortIDLength = 12

	// MinPrefixLength is the minimum prefix length for ID lookup.
	MinPrefixLength = 3
)

// GenerateID generates a random 64-character hexadecimal container ID.
// This follows Docker's container ID convention.
func GenerateID() string {
	bytes := make([]byte, 32) // 32 bytes = 64 hex characters
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to predictable ID if random generation fails.
		// This should never happen in practice.
		return "0000000000000000000000000000000000000000000000000000000000000000"
	}
	return hex.EncodeToString(bytes)
}

// ShortID returns the first 12 characters of the container ID.
// This is the standard "short ID" format used by Docker.
func ShortID(id string) string {
	if len(id) >= ShortIDLength {
		return id[:ShortIDLength]
	}
	return id
}

// ValidatePrefix checks if the given prefix is valid for container ID lookup.
// Returns an error if the prefix is too short (less than 3 characters).
func ValidatePrefix(prefix string) error {
	if len(prefix) < MinPrefixLength {
		return fmt.Errorf("container ID prefix must be at least %d characters", MinPrefixLength)
	}
	return nil
}

// IsFullID checks if the given string is a full container ID (64 characters).
func IsFullID(id string) bool {
	return len(id) == FullIDLength
}
