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
