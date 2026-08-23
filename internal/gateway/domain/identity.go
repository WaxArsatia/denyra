package domain

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func CanonicalMBID(value string) (string, error) {
	if value != strings.ToLower(value) || len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return "", fmt.Errorf("MBID must be lowercase canonical UUID")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	if err != nil || len(decoded) != 16 {
		return "", fmt.Errorf("MBID must be lowercase canonical UUID")
	}
	return value, nil
}
