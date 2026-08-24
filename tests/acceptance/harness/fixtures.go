package harness

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type AlbumFixture struct {
	Root         string
	Tracks       []string
	Artwork      string
	Lyrics       []string
	PictureCount int
}

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

func SyntheticAlbum(t *testing.T, root, artist, album string) AlbumFixture {
	t.Helper()
	for _, command := range []string{"ffmpeg", "flac", "metaflac"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("%s is required for deterministic acceptance fixtures: %v", command, err)
		}
	}
	directory := filepath.Join(root, artist, album+" (2024)")
	track := SyntheticFLAC(t, directory, "01 - Acceptance Tone")
	artwork := filepath.Join(directory, "cover.jpg")
	generate := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0x315a67:s=64x64:d=0.1", "-frames:v", "1", artwork)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate synthetic artwork: %v\n%s", err, output)
	}
	tags := []string{
		"--set-tag=ALBUMARTIST=" + artist,
		"--set-tag=ARTIST=" + artist,
		"--set-tag=ALBUM=" + album,
		"--set-tag=TITLE=Acceptance Tone",
		"--set-tag=TRACKNUMBER=1",
		"--set-tag=DISCNUMBER=1",
		"--set-tag=DATE=2024",
		"--import-picture-from=" + artwork,
		track,
	}
	if output, err := exec.Command("metaflac", tags...).CombinedOutput(); err != nil {
		t.Fatalf("tag synthetic FLAC: %v\n%s", err, output)
	}
	if output, err := exec.Command("flac", "-t", "--silent", track).CombinedOutput(); err != nil {
		t.Fatalf("verify synthetic FLAC: %v\n%s", err, output)
	}
	pictures, err := exec.Command("metaflac", "--list", "--block-type=PICTURE", track).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect synthetic artwork: %v\n%s", err, pictures)
	}
	pictureCount := strings.Count(string(pictures), "METADATA block #")
	if pictureCount == 0 {
		t.Fatalf("synthetic FLAC has no embedded picture:\n%s", pictures)
	}
	lyrics := strings.TrimSuffix(track, filepath.Ext(track)) + ".lrc"
	if err := os.WriteFile(lyrics, []byte("[00:00.00] deterministic acceptance tone\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return AlbumFixture{Root: directory, Tracks: []string{track}, Artwork: artwork, Lyrics: []string{lyrics}, PictureCount: pictureCount}
}

func RunGoTestMatrix(packages ...string) ([]byte, error) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, err
	}
	pattern := "Upload|Unmanaged|Migration|Catalog|AdminUI|Restore|Backup"
	arguments := append([]string{"test", "-count=1", "-run", pattern}, packages...)
	command := exec.Command("go", arguments...)
	command.Dir = repository
	output, runErr := command.CombinedOutput()
	if runErr != nil {
		return output, fmt.Errorf("go test %s: %w", strings.Join(packages, " "), runErr)
	}
	return output, nil
}
