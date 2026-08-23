package domain_test

import (
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func FuzzParseStateNeverAcceptsArbitraryValues(f *testing.F) {
	f.Add("RECEIVED")
	f.Add("received")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		state, err := domain.ParseState(value)
		if err == nil && (!state.Valid() || string(state) != value) {
			t.Fatalf("ParseState(%q) returned invalid state %q", value, state)
		}
	})
}
