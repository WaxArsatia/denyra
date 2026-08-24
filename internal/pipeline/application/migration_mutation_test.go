package application_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMigrationMutationFailureRestoresExactOriginalBytesAndMetadata(t *testing.T) {
	root := t.TempDir()
	approved := filepath.Join(root, "candidate")
	if err := os.MkdirAll(approved, 0o750); err != nil {
		t.Fatal(err)
	}
	paths := []string{"01.flac", "02.flac"}
	before := map[string][]byte{}
	for _, relative := range paths {
		data := []byte("original-" + relative)
		before[relative] = data
		if err := os.WriteFile(filepath.Join(approved, relative), data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	mutator := &migrationTagMutator{tags: map[string]domain.TagSet{}, failPath: filepath.Join(approved, "02.flac")}
	for _, relative := range paths {
		mutator.tags[filepath.Join(approved, relative)] = domain.TagSet{"TITLE": {"Original"}, "CUSTOM": {"keep"}}
	}
	service := application.MigrationMutationService{ApprovedRoot: root, Tags: mutator, Integrity: migrationPassingIntegrity{}, Checksum: media.SHA256}
	plans := map[string]domain.TagSet{"01.flac": {"TITLE": {"Canonical"}}, "02.flac": {"TITLE": {"Canonical"}}}
	result, err := service.Apply(context.Background(), "candidate", plans)
	if err == nil {
		t.Fatal("injected mutation failure ignored")
	}
	if err := service.Restore(context.Background(), "candidate", result); err != nil {
		t.Fatal(err)
	}
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(approved, relative))
		if err != nil || string(data) != string(before[relative]) {
			t.Fatalf("%s restore data=%q err=%v", relative, data, err)
		}
		if mutator.tags[filepath.Join(approved, relative)]["TITLE"][0] != "Original" || mutator.pictures != 1 || mutator.md5 != "audio-md5" {
			t.Fatalf("%s metadata not restored: %+v", relative, mutator.tags[filepath.Join(approved, relative)])
		}
	}
}

type migrationTagMutator struct {
	tags     map[string]domain.TagSet
	failPath string
	pictures int
	md5      string
}

func (m *migrationTagMutator) ReadTags(_ context.Context, path string) (domain.TagSet, domain.CommandEvidence, error) {
	if data, err := os.ReadFile(path); err == nil && strings.HasPrefix(string(data), "original-") {
		m.tags[path] = domain.TagSet{"TITLE": {"Original"}, "CUSTOM": {"keep"}}
	}
	return cloneMigrationTags(m.tags[path]), domain.CommandEvidence{}, nil
}
func (m *migrationTagMutator) AudioMD5(context.Context, string) (string, domain.CommandEvidence, error) {
	if m.md5 == "" {
		m.md5 = "audio-md5"
	}
	return m.md5, domain.CommandEvidence{}, nil
}
func (m *migrationTagMutator) PictureCount(context.Context, string) (int, domain.CommandEvidence, error) {
	if m.pictures == 0 {
		m.pictures = 1
	}
	return m.pictures, domain.CommandEvidence{}, nil
}
func (m *migrationTagMutator) Apply(context.Context, string, domain.TagSet, bool) ([]domain.CommandEvidence, error) {
	return nil, errors.New("unexpected Apply")
}
func (m *migrationTagMutator) ApplySelected(_ context.Context, path string, tags domain.TagSet, _ []string, _ bool) ([]domain.CommandEvidence, error) {
	if path == m.failPath {
		return nil, errors.New("injected mutation failure")
	}
	current := cloneMigrationTags(m.tags[path])
	for field, values := range tags {
		current[field] = append([]string(nil), values...)
	}
	m.tags[path] = current
	if err := os.WriteFile(path, []byte("mutated"), 0o640); err != nil {
		return nil, err
	}
	return nil, nil
}

func cloneMigrationTags(input domain.TagSet) domain.TagSet {
	result := domain.TagSet{}
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}

type migrationPassingIntegrity struct{}

func (migrationPassingIntegrity) Test(context.Context, string) (domain.CommandEvidence, error) {
	return domain.CommandEvidence{}, nil
}
