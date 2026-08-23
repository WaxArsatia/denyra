package filesystem_test

import (
	"path/filepath"
	"testing"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
)

func FuzzScanRejectsNonCanonicalRoots(f *testing.F) {
	f.Add("relative")
	f.Add("/tmp/../tmp")
	f.Add("")
	f.Fuzz(func(t *testing.T, path string) {
		if filepath.IsAbs(path) && filepath.Clean(path) == path {
			t.Skip()
		}
		if _, err := denyrafs.Scan(path); err == nil {
			t.Fatalf("unsafe root accepted: %q", path)
		}
	})
}
