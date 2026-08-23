package spotiflac

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const RegistryCommit = "8fc37551ead10683d7ab54cb4155dc5cca4948e6"

type ExtensionIdentity struct {
	ID, Version, SHA256, MinAppVersion string
	RequiredRuntimeFeatures            []string
}

type RuntimeManifest struct {
	EngineVersion, EngineSHA256     string
	RegistryCommit                  string
	NodeVersion, NodeArtifactSHA256 string
	Extensions                      []ExtensionIdentity
}

func ExpectedManifest() RuntimeManifest {
	features := []string{"signedSession@1", "sessionGrant@1"}
	return RuntimeManifest{
		EngineVersion:      "3.0.8",
		EngineSHA256:       "c008b5b59999f6f740d3f8e0290ce5fe18220dcd736aa903469e5b0ac062334a",
		RegistryCommit:     RegistryCommit,
		NodeVersion:        "24.19.0",
		NodeArtifactSHA256: "14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647",
		Extensions: []ExtensionIdentity{
			{ID: "tidal-web", Version: "1.1.7", SHA256: "0d59043bab8229b5fd5664bc144aee25bfd3e6d031832cdce48b9d9ccef5ed22", MinAppVersion: "4.7.0", RequiredRuntimeFeatures: append([]string(nil), features...)},
			{ID: "qobuz-web", Version: "1.1.0", SHA256: "9e6d14dc37623eed9ac6326c321b17fd802c36e907476f3068f7fcbe14d79f93", MinAppVersion: "4.7.0", RequiredRuntimeFeatures: append([]string(nil), features...)},
			{ID: "deezer", Version: "1.2.0", SHA256: "dfead5b50889d2855b4409c6796421ccb35ffd3cac1e002498924e9a7c5446b3", MinAppVersion: "4.7.0", RequiredRuntimeFeatures: append([]string(nil), features...)},
		},
	}
}

func (manifest RuntimeManifest) Providers() []string {
	providers := make([]string, len(manifest.Extensions))
	for index, extension := range manifest.Extensions {
		providers[index] = "ext:" + extension.ID
	}
	return providers
}

type Installation struct {
	EnginePath, NodePath, ArtifactDirectory, InstalledExtensionDirectory, BuildProvenancePath string
	Manifest                                                                                  RuntimeManifest
}

type VerifiedInstallation struct {
	Installation Installation
	VerifiedAt   time.Time
}

func (installation Installation) Verify(ctx context.Context, commandTimeout time.Duration, now time.Time) (VerifiedInstallation, error) {
	if commandTimeout <= 0 {
		return VerifiedInstallation{}, fmt.Errorf("verification command timeout must be positive")
	}
	manifest := installation.Manifest
	if manifest.EngineVersion == "" || len(manifest.Extensions) == 0 {
		return VerifiedInstallation{}, fmt.Errorf("runtime manifest is incomplete")
	}
	if err := verifyFileHash(installation.EnginePath, manifest.EngineSHA256); err != nil {
		return VerifiedInstallation{}, fmt.Errorf("engine: %w", err)
	}
	entries, err := os.ReadDir(installation.ArtifactDirectory)
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("read extension artifacts: %w", err)
	}
	actualArtifacts := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sflx") {
			actualArtifacts = append(actualArtifacts, entry.Name())
		}
	}
	expectedArtifacts := make([]string, 0, len(manifest.Extensions))
	expectedInstalled := make([]string, 0, len(manifest.Extensions))
	for _, extension := range manifest.Extensions {
		filename := extension.ID + ".sflx"
		expectedArtifacts = append(expectedArtifacts, filename)
		expectedInstalled = append(expectedInstalled, extension.ID)
		artifact := filepath.Join(installation.ArtifactDirectory, filename)
		if err := verifyFileHash(artifact, extension.SHA256); err != nil {
			return VerifiedInstallation{}, fmt.Errorf("extension %s: %w", extension.ID, err)
		}
		if err := verifyExtensionManifest(artifact, filepath.Join(installation.InstalledExtensionDirectory, extension.ID), extension); err != nil {
			return VerifiedInstallation{}, err
		}
	}
	sort.Strings(actualArtifacts)
	sort.Strings(expectedArtifacts)
	if !equalStrings(actualArtifacts, expectedArtifacts) {
		return VerifiedInstallation{}, fmt.Errorf("extension artifact allowlist mismatch: got %v want %v", actualArtifacts, expectedArtifacts)
	}
	installedEntries, err := os.ReadDir(installation.InstalledExtensionDirectory)
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("read installed extensions: %w", err)
	}
	actualInstalled := make([]string, 0, len(installedEntries))
	for _, entry := range installedEntries {
		if !entry.IsDir() {
			return VerifiedInstallation{}, fmt.Errorf("unexpected installed extension entry %s", entry.Name())
		}
		actualInstalled = append(actualInstalled, entry.Name())
	}
	sort.Strings(actualInstalled)
	sort.Strings(expectedInstalled)
	if !equalStrings(actualInstalled, expectedInstalled) {
		return VerifiedInstallation{}, fmt.Errorf("installed extension allowlist mismatch: got %v want %v", actualInstalled, expectedInstalled)
	}
	if err := verifyBuildProvenance(installation.BuildProvenancePath, manifest); err != nil {
		return VerifiedInstallation{}, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, installation.NodePath, "--version").Output()
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("verify Node runtime: %w", err)
	}
	if strings.TrimSpace(string(output)) != "v"+manifest.NodeVersion {
		return VerifiedInstallation{}, fmt.Errorf("Node version mismatch: %q", strings.TrimSpace(string(output)))
	}
	return VerifiedInstallation{Installation: installation, VerifiedAt: now.UTC()}, nil
}

func verifyFileHash(path, expected string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA-256 mismatch: got %s want %s", actual, expected)
	}
	return nil
}

func verifyExtensionManifest(archivePath, installedPath string, expected ExtensionIdentity) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open extension %s: %w", expected.ID, err)
	}
	defer reader.Close()
	var manifestData []byte
	archiveFiles := make(map[string][]byte)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		clean := filepath.Clean(file.Name)
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || file.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("extension %s contains unsafe path %q", expected.ID, file.Name)
		}
		stream, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(stream)
		closeErr := stream.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		archiveFiles[filepath.ToSlash(clean)] = data
		if filepath.ToSlash(clean) == "manifest.json" {
			manifestData = data
		}
	}
	if len(manifestData) == 0 {
		return fmt.Errorf("extension %s has no manifest", expected.ID)
	}
	var value struct {
		Name                    string   `json:"name"`
		Version                 string   `json:"version"`
		MinAppVersion           string   `json:"minAppVersion"`
		RequiredRuntimeFeatures []string `json:"requiredRuntimeFeatures"`
		Type                    []string `json:"type"`
	}
	if err := json.Unmarshal(manifestData, &value); err != nil {
		return fmt.Errorf("decode extension %s manifest: %w", expected.ID, err)
	}
	if value.Name != expected.ID || value.Version != expected.Version || value.MinAppVersion != expected.MinAppVersion || !equalStrings(value.RequiredRuntimeFeatures, expected.RequiredRuntimeFeatures) || !contains(value.Type, "download_provider") {
		return fmt.Errorf("extension %s manifest compatibility mismatch", expected.ID)
	}
	var installedFiles []string
	err = filepath.WalkDir(installedPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == installedPath || entry.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("installed extension %s contains non-regular file", expected.ID)
		}
		relative, err := filepath.Rel(installedPath, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		expectedData, found := archiveFiles[relative]
		if !found {
			return fmt.Errorf("installed extension %s contains unexpected file %s", expected.ID, relative)
		}
		actualData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(actualData, expectedData) {
			return fmt.Errorf("installed extension %s file %s differs from verified artifact", expected.ID, relative)
		}
		installedFiles = append(installedFiles, relative)
		return nil
	})
	if err != nil {
		return err
	}
	expectedFiles := make([]string, 0, len(archiveFiles))
	for name := range archiveFiles {
		expectedFiles = append(expectedFiles, name)
	}
	sort.Strings(expectedFiles)
	sort.Strings(installedFiles)
	if !equalStrings(installedFiles, expectedFiles) {
		return fmt.Errorf("installed extension %s file set mismatch", expected.ID)
	}
	return nil
}

func verifyBuildProvenance(path string, manifest RuntimeManifest) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read build provenance: %w", err)
	}
	var value struct {
		Artifacts []struct {
			ID      string `json:"id"`
			Version string `json:"version"`
			SHA256  string `json:"sha256"`
		} `json:"artifacts"`
		Registries []struct {
			ID     string `json:"id"`
			Commit string `json:"commit"`
		} `json:"registries"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode build provenance: %w", err)
	}
	expected := map[string][2]string{
		"spotiflac-engine": {manifest.EngineVersion, manifest.EngineSHA256},
		"node-runtime":     {manifest.NodeVersion, manifest.NodeArtifactSHA256},
	}
	for _, extension := range manifest.Extensions {
		expected["spotiflac-ext-"+extension.ID] = [2]string{extension.Version, extension.SHA256}
	}
	for _, artifact := range value.Artifacts {
		if identity, found := expected[artifact.ID]; found && artifact.Version == identity[0] && artifact.SHA256 == identity[1] {
			delete(expected, artifact.ID)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("build provenance artifact mismatch: %v", expected)
	}
	for _, registry := range value.Registries {
		if registry.ID == "spotiflac-extension-registry" && registry.Commit == manifest.RegistryCommit {
			return nil
		}
	}
	return fmt.Errorf("build provenance registry mismatch")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
