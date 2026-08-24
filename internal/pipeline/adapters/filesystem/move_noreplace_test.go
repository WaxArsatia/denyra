package filesystem_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	denyrafs "github.com/waxarsatia/denyra/internal/pipeline/adapters/filesystem"
)

func TestMoveNoReplaceNeverOverwritesExistingTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := denyrafs.MoveNoReplace(source, target); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(root, "second")
	if err := os.Mkdir(second, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	err := denyrafs.MoveNoReplace(second, target)
	if !errors.Is(err, denyrafs.ErrTargetExists) {
		t.Fatalf("collision error=%v", err)
	}
	body, readErr := os.ReadFile(filepath.Join(target, "keep"))
	if readErr != nil || string(body) != "original" {
		t.Fatalf("target changed: %q err=%v", body, readErr)
	}
}
