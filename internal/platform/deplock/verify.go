package deplock

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hex40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func (l Lock) Validate() error {
	if l.Schema != 1 {
		return fmt.Errorf("dependency lock schema must be 1")
	}
	seen := make(map[string]struct{})
	claim := func(id string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("dependency ID is required")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate dependency ID %q", id)
		}
		seen[id] = struct{}{}
		return nil
	}
	for _, image := range l.Images {
		if err := claim(image.ID); err != nil {
			return err
		}
		if err := validateImage(image); err != nil {
			return fmt.Errorf("image %q: %w", image.ID, err)
		}
	}
	for _, artifact := range l.Artifacts {
		if err := claim(artifact.ID); err != nil {
			return err
		}
		if artifact.Version == "" || artifact.Filename == "" || !hex64.MatchString(artifact.SHA256) {
			return fmt.Errorf("artifact %q has incomplete identity", artifact.ID)
		}
	}
	for _, registry := range l.Registries {
		if err := claim(registry.ID); err != nil {
			return err
		}
		if !strings.Contains(registry.Repository, "/") || !hex40.MatchString(registry.Commit) {
			return fmt.Errorf("registry %q has incomplete identity", registry.ID)
		}
	}
	for _, component := range l.Components {
		if err := claim(component.ID); err != nil {
			return err
		}
		if component.Version == "" || component.Version == "latest" {
			return fmt.Errorf("component %q has invalid version", component.ID)
		}
	}
	return nil
}

func validateImage(image Image) error {
	if image.Platform != "linux/amd64" {
		return fmt.Errorf("platform must be linux/amd64")
	}
	if image.Version == "" || image.Version == "latest" {
		return fmt.Errorf("version is required and must not float")
	}
	if !strings.HasPrefix(image.Digest, "sha256:") || !hex64.MatchString(strings.TrimPrefix(image.Digest, "sha256:")) {
		return fmt.Errorf("digest must be a full sha256 identity")
	}
	if image.Reference == "nightly" || strings.Contains(image.Reference, ":latest") {
		return fmt.Errorf("reference must not float")
	}
	if !strings.Contains(image.Reference, "/") || !strings.Contains(image.Reference, ":") {
		return fmt.Errorf("reference must contain registry, repository, and tag")
	}
	suffix := "@" + image.Digest
	if !strings.HasSuffix(image.Reference, suffix) {
		return fmt.Errorf("reference digest does not match digest field")
	}
	return nil
}
