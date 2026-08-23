package operations_test

import (
	"errors"
	"github.com/waxarsatia/denyra/internal/platform/logsafe"
	"strings"
	"testing"
)

func TestLoggingRedactsNestedAndTextSecrets(t *testing.T) {
	value := map[string]any{"request": map[string]any{"password": "hunter22", "csrf_token": "csrf-value"}, "error": errors.New("Authorization: Bearer bearer-value api_key=query-value")}
	encoded := logsafe.String(value)
	for _, secret := range []string{"hunter22", "csrf-value", "bearer-value", "query-value"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("secret leaked: %s", encoded)
		}
	}
	if !strings.Contains(encoded, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %s", encoded)
	}
}
