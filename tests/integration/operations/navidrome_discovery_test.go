package operations_test

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func TestNavidromeDiscoveryPoliciesCannotMutateSyntheticLibrary(t *testing.T) {
	root := t.TempDir()
	track := filepath.Join(root, "01.flac")
	lyrics := filepath.Join(root, "01.lrc")
	if err := os.WriteFile(track, []byte("synthetic-audio-evidence"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lyrics, []byte("[00:00.00] synthetic lyrics\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	beforeTrack, beforeLyrics := fileHash(t, track), fileHash(t, lyrics)
	if beforeTrack != fileHash(t, track) || beforeLyrics != fileHash(t, lyrics) {
		t.Fatal("discovery changed library checksums")
	}
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}
