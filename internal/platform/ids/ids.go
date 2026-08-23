// Package ids creates opaque cryptographic identifiers.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewToken(byteCount int) (string, error) {
	if byteCount <= 0 {
		return "", fmt.Errorf("token byte count must be positive")
	}
	random := make([]byte, byteCount)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func HashToken(token string) [sha256.Size]byte { return sha256.Sum256([]byte(token)) }
