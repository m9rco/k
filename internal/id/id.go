// Package id generates short, URL-safe unique identifiers used for assets,
// tasks, and similar entities. Ids are random (not sequential) so they do not
// leak counts or ordering.
package id

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns a 16-hex-character random id with the given prefix
// (e.g. New("asset") -> "asset_3f9a1c...").
func New(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
