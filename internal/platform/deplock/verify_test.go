package deplock_test

import (
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/platform/deplock"
)

func TestDecodeRejectsInvalidDependencyIdentities(t *testing.T) {
	t.Parallel()

	valid := `{"schema":1,"images":[{"id":"lidarr","reference":"lscr.io/linuxserver/lidarr:nightly@sha256:` + strings.Repeat("a", 64) + `","platform":"linux/amd64","version":"nightly","digest":"sha256:` + strings.Repeat("a", 64) + `"}],"artifacts":[],"registries":[],"components":[]}`
	tests := map[string]string{
		"unknown property": strings.Replace(valid, `"schema":1`, `"schema":1,"surprise":true`, 1),
		"bare nightly":     strings.Replace(valid, `lscr.io/linuxserver/lidarr:nightly@sha256:`+strings.Repeat("a", 64), `nightly`, 1),
		"floating latest":  strings.Replace(valid, `lscr.io/linuxserver/lidarr:nightly@sha256:`+strings.Repeat("a", 64), `lscr.io/linuxserver/lidarr:latest`, 1),
		"short digest":     strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("a", 12), 1),
		"wrong platform":   strings.Replace(valid, `linux/amd64`, `linux/arm64`, 1),
	}

	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := deplock.Decode([]byte(document)); err == nil {
				t.Fatal("Decode accepted invalid lock")
			}
		})
	}
}

func TestDecodeRejectsDuplicateDependencyID(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("b", 64)
	document := `{"schema":1,"images":[],"artifacts":[` +
		`{"id":"asset","version":"1.0.0","filename":"one.tgz","sha256":"` + digest + `"},` +
		`{"id":"asset","version":"1.0.1","filename":"two.tgz","sha256":"` + digest + `"}` +
		`],"registries":[],"components":[]}`
	if _, err := deplock.Decode([]byte(document)); err == nil {
		t.Fatal("Decode accepted duplicate dependency ID")
	}
}

func TestCanonicalJSONIsDeterministic(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("c", 64)
	document := `{"schema":1,"images":[],"artifacts":[{"id":"go","version":"1.27.0","filename":"go.tgz","sha256":"` + digest + `"}],"registries":[],"components":[]}`
	lock, err := deplock.Decode([]byte(document))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	first, err := lock.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	second, err := lock.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON second call: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical bytes differ:\n%s\n%s", first, second)
	}
}

func TestImageReturnsExactLockedIdentity(t *testing.T) {
	digest := strings.Repeat("d", 64)
	document := `{"schema":1,"images":[{"id":"slskd","reference":"ghcr.io/slskd/slskd:0.26.0@sha256:` + digest + `","platform":"linux/amd64","version":"0.26.0","digest":"sha256:` + digest + `"}],"artifacts":[],"registries":[],"components":[]}`
	lock, err := deplock.Decode([]byte(document))
	if err != nil {
		t.Fatal(err)
	}
	image, err := lock.Image("slskd")
	if err != nil {
		t.Fatal(err)
	}
	if image.Version != "0.26.0" || image.Reference == "" || image.Digest == "" {
		t.Fatalf("image=%+v", image)
	}
	if _, err := lock.Image("missing"); err == nil {
		t.Fatal("missing locked image was accepted")
	}
}
