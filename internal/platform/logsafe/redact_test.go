package logsafe_test

import (
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/logsafe"
)

func TestRedactRecursivelyRemovesSensitiveValues(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"username":      "admin",
		"authorization": "Bearer visible-secret",
		"nested":        map[string]any{"api_key": "another-secret", "safe": "value"},
		"items":         []any{map[string]any{"csrf": "csrf-secret"}},
	}
	redacted := logsafe.Redact(input, nil)
	printed := logsafe.String(redacted)
	for _, secret := range []string{"visible-secret", "another-secret", "csrf-secret"} {
		if strings.Contains(printed, secret) {
			t.Fatalf("redacted output contains %q", secret)
		}
	}
	if !strings.Contains(printed, "admin") || !strings.Contains(printed, "value") {
		t.Fatalf("safe values were removed: %s", printed)
	}
}

func TestRedactTextMasksSubprocessCredentials(t *testing.T) {
	t.Parallel()
	got := logsafe.RedactText("request failed: Authorization: Bearer abc123 password=hunter2")
	if strings.Contains(got, "abc123") || strings.Contains(got, "hunter2") {
		t.Fatalf("RedactText leaked credential: %s", got)
	}
}
