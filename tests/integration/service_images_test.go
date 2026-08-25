package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceImagesContainRequiredRuntimes(t *testing.T) {
	if os.Getenv("DENYRA_TEST_IMAGE_SMOKE") != "1" {
		t.Skip("set DENYRA_TEST_IMAGE_SMOKE=1 after building local images")
	}
	imageTag := os.Getenv("DENYRA_IMAGE_TAG")
	if imageTag == "" {
		imageTag = "local"
	}
	gateway := "denyra/acquisition-gateway:" + imageTag
	pipeline := "denyra/media-pipeline:" + imageTag
	lidarr := "denyra/lidarr:" + imageTag
	navidrome := "denyra/navidrome:" + imageTag
	checks := []struct {
		name       string
		image      string
		entrypoint string
		arguments  []string
	}{
		{name: "gateway node", image: gateway, entrypoint: "node", arguments: []string{"--version"}},
		{name: "gateway engine", image: gateway, entrypoint: "/opt/spotiflac/spotiflac", arguments: []string{"--help"}},
		{name: "gateway codecs", image: gateway, entrypoint: "/bin/sh", arguments: []string{"-c", `ffmpeg -version >/dev/null && flac --version >/dev/null`}},
		{name: "gateway embedded bridge", image: gateway, entrypoint: "/bin/sh", arguments: []string{"-c", `test -s /opt/spotiflac/build-contents.txt && grep -Fq 'SpotiFLAC/extensions/_bridge.js' /opt/spotiflac/build-contents.txt`}},
		{name: "gateway providers", image: gateway, entrypoint: "/bin/sh", arguments: []string{"-c", `for provider in tidal-web qobuz-web deezer; do manifest="/opt/spotiflac/runtime-home/.spotiflac/extensions/$provider/manifest.json"; test -s "$manifest" && grep -q 'download_provider' "$manifest"; done`}},
		{name: "gateway provider permissions", image: gateway, entrypoint: "/bin/sh", arguments: []string{"-c", `test "$(id -u)" = 1000 && test -r /opt/spotiflac/runtime-home/.spotiflac/extensions/tidal-web/manifest.json && ! test -w /opt/spotiflac/runtime-home/.spotiflac/extensions/tidal-web`}},
		{name: "pipeline python", image: pipeline, entrypoint: "python", arguments: []string{"--version"}},
		{name: "pipeline beets", image: pipeline, entrypoint: "beet", arguments: []string{"version"}},
		{name: "pipeline ffprobe", image: pipeline, entrypoint: "ffprobe", arguments: []string{"-version"}},
		{name: "pipeline flac", image: pipeline, entrypoint: "flac", arguments: []string{"--version"}},
		{name: "pipeline metaflac", image: pipeline, entrypoint: "metaflac", arguments: []string{"--version"}},
		{name: "lidarr plugin", image: lidarr, entrypoint: "/bin/sh", arguments: []string{"-c", "test -s /defaults/denyra-plugins/Lidarr.Plugin.Slskd/Lidarr.Plugin.Slskd.dll"}},
		{name: "navidrome binary", image: navidrome, entrypoint: "/app/navidrome", arguments: []string{"--version"}},
		{name: "navidrome lyrics plugin", image: navidrome, entrypoint: "/bin/sh", arguments: []string{"-c", "test -r /plugins/nd-lyrics.ndp"}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			arguments := []string{"run", "--rm", "--entrypoint", check.entrypoint, check.image}
			arguments = append(arguments, check.arguments...)
			if output, err := exec.Command("docker", arguments...).CombinedOutput(); err != nil {
				t.Fatalf("runtime check failed: %v\n%s", err, output)
			}
		})
	}
}

func TestApplicationDockerfilesUseOfficialRuntimeLines(t *testing.T) {
	gateway := readDockerfile(t, "gateway.Dockerfile")
	pipeline := readDockerfile(t, "pipeline.Dockerfile")
	for _, fragment := range []string{
		"FROM golang:1.27", "FROM python:3.12-slim", "FROM node:24-trixie-slim",
		"SPOTIFLAC_COMMIT=0306da57dd855175549af119d95539e51ffbd801",
		"SPOTIFLAC_SOURCE_SHA256=66301166b55020ec5ad46082f5c7acbe4811112aed996cd36b1fd8220e1b3754",
		"SPOTIFLAC_EXTENSION_COMMIT=6a4227aec696cd98d6fa9d25d92b1a38f9ae1a07",
		"TIDAL_WEB_SHA256=d346f3e5fdb6f349d8f6ede1310d1961862936f64b3dabbbb4fba868cea31a9a",
		"QOBUZ_WEB_SHA256=3b5fab92608ada9eefe94dcc11a11bef79204804dee3ced2d86809df0d5306cd",
		"DEEZER_SHA256=f6a2505da67c25d4ddfc5f233fdc14e127031f7e15a39fc9d2d0b82fc4b1fd60",
		"--add-data=SpotiFLAC/extensions/_bridge.js:SpotiFLAC/extensions",
		"pyi-archive_viewer", "ffmpeg", "flac",
		"org.opencontainers.image.spotiflac.commit", "org.opencontainers.image.spotiflac-extension.commit",
	} {
		if !strings.Contains(gateway, fragment) {
			t.Errorf("gateway missing %q", fragment)
		}
	}
	for _, fragment := range []string{"FROM golang:1.27", "FROM python:3.14-slim", "beets>=2,<3"} {
		if !strings.Contains(pipeline, fragment) {
			t.Errorf("pipeline missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"releases/latest", "SpotiFLAC-Extension/main", "@sha256:", "Python-" + "3.", "node-" + "v", "--require-hashes", "debian.sources"} {
		if strings.Contains(gateway+pipeline, forbidden) {
			t.Errorf("obsolete strict input remained: %q", forbidden)
		}
	}
}

func TestDerivedDockerfilesFollowCompatibleChannels(t *testing.T) {
	for name, want := range map[string]string{
		"lidarr.Dockerfile":    "FROM lscr.io/linuxserver/lidarr:nightly",
		"navidrome.Dockerfile": "FROM deluan/navidrome:latest",
	} {
		text := readDockerfile(t, name)
		if !strings.Contains(text, want) || strings.Contains(text, "@sha256:") {
			t.Errorf("%s does not follow its compatible channel", name)
		}
		if !strings.Contains(text, "ARG DENYRA_RELEASE_REFRESH") {
			t.Errorf("%s cannot refresh latest plugin assets", name)
		}
		if !strings.Contains(text, `io.denyra.project="denyra"`) {
			t.Errorf("%s cannot participate in scoped Denyra image cleanup", name)
		}
	}
}

func readDockerfile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "deploy", "docker", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}
