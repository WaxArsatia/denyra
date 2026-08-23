package clock_test

import (
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/platform/clock"
)

func TestSystemClockReturnsUTC(t *testing.T) {
	t.Parallel()
	if got := (clock.System{}).Now().Location(); got != time.UTC {
		t.Fatalf("location = %v, want UTC", got)
	}
}

func TestFakeClockReturnsInjectedTimeAsUTC(t *testing.T) {
	t.Parallel()
	local := time.Date(2026, 8, 24, 12, 0, 0, 0, time.FixedZone("test", 7*60*60))
	if got := (clock.Fake{Time: local}).Now(); !got.Equal(local) || got.Location() != time.UTC {
		t.Fatalf("Now() = %v (%v), want equivalent UTC", got, got.Location())
	}
}
