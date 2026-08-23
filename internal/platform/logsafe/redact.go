// Package logsafe removes credentials before structured or subprocess output is logged.
package logsafe

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const replacement = "[REDACTED]"

var textSecrets = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)[^\s,;]+`),
	regexp.MustCompile(`(?i)((?:password|token|session|csrf|api[_-]?key)\s*[=:]\s*)[^\s,;]+`),
}

func Redact(value any, additionalKeys []string) any {
	sensitive := map[string]struct{}{}
	for _, key := range []string{"password", "authorization", "bearer", "token", "session", "csrf", "apikey", "secret"} {
		sensitive[key] = struct{}{}
	}
	for _, key := range additionalKeys {
		sensitive[normalizeKey(key)] = struct{}{}
	}
	return redactValue(value, sensitive)
}

func redactValue(value any, sensitive map[string]struct{}) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if _, secret := sensitive[normalizeKey(key)]; secret {
				result[key] = replacement
			} else {
				result[key] = redactValue(child, sensitive)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, child := range typed {
			result[index] = redactValue(child, sensitive)
		}
		return result
	default:
		return typed
	}
}

func normalizeKey(key string) string {
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return replacer.Replace(strings.ToLower(key))
}

func RedactText(text string) string {
	for _, expression := range textSecrets {
		text = expression.ReplaceAllString(text, `${1}`+replacement)
	}
	return text
}

func String(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%T", value)
	}
	return string(encoded)
}
