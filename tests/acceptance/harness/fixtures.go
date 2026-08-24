package harness

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func SyntheticFLAC(t *testing.T, directory, basename string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, basename+".flac")
	command := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "flac", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate synthetic FLAC: %v\n%s", err, output)
	}
	return path
}

func SHA256(t *testing.T, path string) [32]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(content)
}
