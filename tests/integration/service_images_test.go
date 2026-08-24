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
	checks := []struct {
		name       string
		image      string
		entrypoint string
		arguments  []string
	}{
		{name: "gateway node", image: gateway, entrypoint: "node", arguments: []string{"--version"}},
		{name: "gateway engine", image: gateway, entrypoint: "/opt/spotiflac/spotiflac", arguments: []string{"--help"}},
		{name: "gateway providers", image: gateway, entrypoint: "/bin/sh", arguments: []string{"-c", `for provider in tidal-web qobuz-web deezer; do manifest="/opt/spotiflac/runtime-home/.spotiflac/extensions/$provider/manifest.json"; test -s "$manifest" && grep -q 'download_provider' "$manifest"; done`}},
		{name: "pipeline python", image: pipeline, entrypoint: "python", arguments: []string{"--version"}},
		{name: "pipeline beets", image: pipeline, entrypoint: "beet", arguments: []string{"version"}},
		{name: "pipeline ffprobe", image: pipeline, entrypoint: "ffprobe", arguments: []string{"-version"}},
		{name: "pipeline flac", image: pipeline, entrypoint: "flac", arguments: []string{"--version"}},
		{name: "pipeline metaflac", image: pipeline, entrypoint: "metaflac", arguments: []string{"--version"}},
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
	for _, fragment := range []string{"FROM golang:1.27", "FROM node:24-trixie-slim", "ARG DENYRA_RELEASE_REFRESH"} {
		if !strings.Contains(gateway, fragment) {
			t.Errorf("gateway missing %q", fragment)
		}
	}
	for _, fragment := range []string{"FROM golang:1.27", "FROM python:3.14-slim", "beets>=2,<3"} {
		if !strings.Contains(pipeline, fragment) {
			t.Errorf("pipeline missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"@sha256:", "Python-3.", "node-v", "--require-hashes", "dependencies.lock", "build-provenance", "debian.sources"} {
		if strings.Contains(gateway+pipeline, forbidden) {
			t.Errorf("obsolete strict input remained: %q", forbidden)
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
