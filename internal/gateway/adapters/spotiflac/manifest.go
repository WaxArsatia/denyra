package spotiflac

import (
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

type ExtensionIdentity struct {
	ID, Version, SHA256, MinAppVersion string
	RequiredRuntimeFeatures            []string
	Types                              []string
}

type RuntimeManifest struct {
	EngineVersion, EngineSHA256 string
	NodeVersion, NodeSHA256     string
	Extensions                  []ExtensionIdentity
}

func (manifest RuntimeManifest) Providers() []string {
	providers := make([]string, len(manifest.Extensions))
	for index, extension := range manifest.Extensions {
		providers[index] = "ext:" + extension.ID
	}
	return providers
}

type Installation struct {
	EnginePath                  string
	NodePath                    string
	InstalledExtensionDirectory string
	Manifest                    RuntimeManifest
}

type VerifiedInstallation struct {
	Installation Installation
	VerifiedAt   time.Time
}

func (installation Installation) Verify(ctx context.Context, commandTimeout time.Duration, now time.Time) (VerifiedInstallation, error) {
	if commandTimeout <= 0 {
		return VerifiedInstallation{}, fmt.Errorf("verification command timeout must be positive")
	}
	nodeOutput, err := runVersionCommand(ctx, commandTimeout, installation.NodePath, "--version")
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("verify Node runtime: %w", err)
	}
	engineOutput, err := runVersionCommand(ctx, commandTimeout, installation.EnginePath, "--help")
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("verify SpotiFLAC runtime: %w", err)
	}
	engineHash, err := regularFileSHA256(installation.EnginePath)
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("hash SpotiFLAC runtime: %w", err)
	}
	nodeHash, err := regularFileSHA256(installation.NodePath)
	if err != nil {
		return VerifiedInstallation{}, fmt.Errorf("hash Node runtime: %w", err)
	}
	extensions, err := discoverExtensions(installation.InstalledExtensionDirectory)
	if err != nil {
		return VerifiedInstallation{}, err
	}
	installation.Manifest = RuntimeManifest{
		EngineVersion: parseEngineVersion(engineOutput),
		EngineSHA256:  engineHash,
		NodeVersion:   strings.TrimPrefix(strings.TrimSpace(nodeOutput), "v"),
		NodeSHA256:    nodeHash,
		Extensions:    extensions,
	}
	if installation.Manifest.NodeVersion == "" {
		return VerifiedInstallation{}, fmt.Errorf("Node runtime did not report a version")
	}
	return VerifiedInstallation{Installation: installation, VerifiedAt: now.UTC()}, nil
}

func runVersionCommand(ctx context.Context, timeout time.Duration, path string, argument string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("runtime path is empty")
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, path, argument).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %s %s: %w", path, argument, err)
	}
	return string(output), nil
}

func discoverExtensions(root string) ([]ExtensionIdentity, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read installed extensions: %w", err)
	}
	extensions := make([]ExtensionIdentity, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("inspect installed extension %s: %w", entry.Name(), err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("installed extension entry %s is not a regular directory", entry.Name())
		}
		manifestPath := filepath.Join(root, entry.Name(), "manifest.json")
		identity, err := decodeExtensionManifest(manifestPath)
		if err != nil {
			return nil, fmt.Errorf("extension %s manifest: %w", entry.Name(), err)
		}
		if identity.ID != entry.Name() {
			return nil, fmt.Errorf("extension %s manifest name is %q", entry.Name(), identity.ID)
		}
		if _, duplicate := seen[identity.ID]; duplicate {
			return nil, fmt.Errorf("duplicate extension provider %q", identity.ID)
		}
		seen[identity.ID] = struct{}{}
		extensions = append(extensions, identity)
	}
	if len(extensions) == 0 {
		return nil, fmt.Errorf("installed extensions contain no download provider")
	}
	sort.Slice(extensions, func(left, right int) bool { return extensions[left].ID < extensions[right].ID })
	return extensions, nil
}

func decodeExtensionManifest(path string) (ExtensionIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ExtensionIdentity{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ExtensionIdentity{}, fmt.Errorf("manifest is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ExtensionIdentity{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return ExtensionIdentity{}, fmt.Errorf("decode JSON: %w", err)
	}
	var identity ExtensionIdentity
	for name, destination := range map[string]any{
		"name": &identity.ID, "version": &identity.Version, "minAppVersion": &identity.MinAppVersion,
		"requiredRuntimeFeatures": &identity.RequiredRuntimeFeatures, "type": &identity.Types,
	} {
		value, found := raw[name]
		if !found {
			return ExtensionIdentity{}, fmt.Errorf("missing required field %s", name)
		}
		if err := json.Unmarshal(value, destination); err != nil {
			return ExtensionIdentity{}, fmt.Errorf("invalid field %s: %w", name, err)
		}
	}
	if strings.TrimSpace(identity.ID) == "" || strings.TrimSpace(identity.Version) == "" || strings.TrimSpace(identity.MinAppVersion) == "" || len(identity.RequiredRuntimeFeatures) == 0 {
		return ExtensionIdentity{}, fmt.Errorf("required compatibility fields are empty")
	}
	if !contains(identity.Types, "download_provider") {
		return ExtensionIdentity{}, fmt.Errorf("no download provider type")
	}
	identity.SHA256, err = regularFileSHA256(path)
	if err != nil {
		return ExtensionIdentity{}, err
	}
	return identity, nil
}

func regularFileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseEngineVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		index := strings.Index(lower, "version")
		if index < 0 {
			continue
		}
		value := strings.TrimSpace(line[index+len("version"):])
		value = strings.TrimLeft(value, ":=v ")
		if value != "" {
			return strings.Fields(value)[0]
		}
	}
	return "unreported"
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
