package migrations_test

import (
	"testing"

	"github.com/waxarsatia/denyra/migrations"
)

func TestEmbeddedMigrationsAreOrderedAndComplete(t *testing.T) {
	for _, service := range []string{"gateway", "pipeline"} {
		loaded, err := migrations.For(service)
		if err != nil {
			t.Fatalf("For(%s): %v", service, err)
		}
		if len(loaded) == 0 || loaded[0].Sequence != 1 || loaded[0].Name != "foundation" {
			t.Fatalf("unexpected %s migrations: %+v", service, loaded)
		}
	}
	if _, err := migrations.For("unknown"); err == nil {
		t.Fatal("unknown service migrations accepted")
	}
}
