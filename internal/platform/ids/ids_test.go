package ids_test

import (
	"encoding/base64"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/ids"
)

func TestNewTokenUsesRequestedRandomByteCount(t *testing.T) {
	t.Parallel()
	first, err := ids.NewToken(32)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	second, err := ids.NewToken(32)
	if err != nil {
		t.Fatalf("NewToken second: %v", err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("decoded token length = %d, want 32", len(decoded))
	}
	if first == second {
		t.Fatal("two random tokens were equal")
	}
	if ids.HashToken(first) == ids.HashToken(second) {
		t.Fatal("different tokens produced equal hashes")
	}
}

func TestNewTokenRejectsNonPositiveSize(t *testing.T) {
	t.Parallel()
	if _, err := ids.NewToken(0); err == nil {
		t.Fatal("NewToken accepted zero bytes")
	}
}
