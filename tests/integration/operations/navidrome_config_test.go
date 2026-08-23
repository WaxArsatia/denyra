package operations_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestNavidromePinnedReadOnlyConfiguration(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy/config/navidrome.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := toml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{`MusicFolder = "/music"`, `DataFolder = "/data"`, `CacheFolder = "/cache"`, `Scanner.ScanOnStartup = true`, `Scanner.WatcherWait = "5s"`, `Scanner.Schedule = "@every 1m"`, `Plugins.AutoReload = false`, `LyricsPriority = ".ttml,.elrc,.lrc,embedded,nd-lyrics"`} {
		if !strings.Contains(text, required) {
			t.Errorf("missing %s", required)
		}
	}
	lyrics, err := os.ReadFile(filepath.Join(root, "deploy/config/navidrome-lyrics.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(lyrics), `WriteToMusicFolder = false`) || !strings.Contains(string(lyrics), `"lrclib"`) {
		t.Fatal("runtime lyrics policy can write to library or lacks LRCLIB")
	}
	compose, err := os.ReadFile(filepath.Join(root, "deploy/compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "target: /music\n        read_only: true") {
		t.Fatal("Navidrome /music is not read-only")
	}
	dockerfile, err := os.ReadFile(filepath.Join(root, "deploy/docker/navidrome.Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "navidrome:0.63.2@sha256:") || !strings.Contains(string(dockerfile), "nd-lyrics.ndp") || !strings.Contains(string(dockerfile), "sha256:a9196e") {
		t.Fatal("Navidrome/plugin image is not exact and verified")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
