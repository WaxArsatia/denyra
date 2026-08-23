package domain

import (
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/unicode/norm"
)

func NormalizeTagValue(value string) (string, error) {
	value = strings.TrimSpace(norm.NFC.String(value))
	if value == "" {
		return "", fmt.Errorf("tag value cannot be empty")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("tag value contains a forbidden control character")
	}
	return value, nil
}

func NormalizeValues(values []string, sortAndDeduplicate bool) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized, err := NormalizeTagValue(value)
		if err != nil {
			return nil, err
		}
		if sortAndDeduplicate {
			key := strings.ToUpper(normalized)
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
		}
		result = append(result, normalized)
	}
	if sortAndDeduplicate {
		slices.SortFunc(result, func(left, right string) int { return strings.Compare(strings.ToUpper(left), strings.ToUpper(right)) })
	}
	return result, nil
}
