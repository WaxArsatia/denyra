package media_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
)

func TestLocalArtworkChoosesCoverSidecarCaseInsensitivelyAndBoundsBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Folder.png"), []byte("folder"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "COVER.JPG"), []byte("cover"), 0o640); err != nil {
		t.Fatal(err)
	}
	local := media.Artwork{MaxBytes: 5}
	body, path, err := local.Sidecar(root)
	if err != nil || !bytes.Equal(body, []byte("cover")) || filepath.Base(path) != "COVER.JPG" {
		t.Fatalf("body=%q path=%q err=%v", body, path, err)
	}
	if err := os.WriteFile(filepath.Join(root, "COVER.JPG"), []byte("oversized"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := local.Sidecar(root); err == nil {
		t.Fatal("oversized sidecar accepted")
	}
}
