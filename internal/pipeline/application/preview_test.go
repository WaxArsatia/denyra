package application_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestSubmissionPreviewReportsMissingMetadataAndUsesFingerprintCache(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"01.flac", "02.flac"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	store := &previewMemoryStore{record: application.SubmissionRecord{ID: "submission-1", SourcePath: root, Status: "DISCOVERED", Ingress: "browser"}}
	inspector := &previewInspector{tags: map[string]map[string][]string{
		"01.flac": {"ALBUMARTIST": {"Kaleb J"}, "ALBUM": {"OFF GUARD"}, "TITLE": {"First"}, "ARTIST": {"Kaleb J"}, "TRACKNUMBER": {"1/2"}, "DISCNUMBER": {"1/1"}},
		"02.flac": {"ALBUMARTIST": {"Kaleb J"}, "ALBUM": {"OFF GUARD"}, "ARTIST": {"Kaleb J"}, "TRACKNUMBER": {"2/2"}, "DISCNUMBER": {"1/1"}},
	}}
	service := application.SubmissionPreviewService{Store: store, Inspector: inspector}
	preview, err := service.Preview(context.Background(), "submission-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Metadata.AlbumArtist != "Kaleb J" || preview.Metadata.Album != "OFF GUARD" || preview.Metadata.Tracks[1].Title != "" {
		t.Fatalf("preview metadata=%+v", preview.Metadata)
	}
	if !hasMetadataConflict(preview.Conflicts, "TITLE", "02.flac") {
		t.Fatalf("missing-title conflict absent: %+v", preview.Conflicts)
	}
	if inspector.calls != 2 {
		t.Fatalf("inspector calls=%d", inspector.calls)
	}
	second, err := service.Preview(context.Background(), "submission-1", false)
	if err != nil || second.Fingerprint != preview.Fingerprint || inspector.calls != 2 {
		t.Fatalf("cached preview=%+v calls=%d err=%v", second, inspector.calls, err)
	}
}

func TestSubmissionPreviewDoesNotInventMajorityAlbumValue(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, name := range []string{"01.flac", "02.flac", "03.flac"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	store := &previewMemoryStore{record: application.SubmissionRecord{ID: "submission-1", SourcePath: root, Status: "DISCOVERED", Ingress: "sftp"}}
	tags := make(map[string]map[string][]string)
	for index, name := range []string{"01.flac", "02.flac", "03.flac"} {
		album := "OFF GUARD"
		if index == 2 {
			album = "OFF GUARD Deluxe"
		}
		tags[name] = map[string][]string{"ALBUMARTIST": {"Kaleb J"}, "ALBUM": {album}, "TITLE": {name}, "ARTIST": {"Kaleb J"}, "TRACKNUMBER": {string(rune('1' + index))}, "DISCNUMBER": {"1"}}
	}
	preview, err := (application.SubmissionPreviewService{Store: store, Inspector: &previewInspector{tags: tags}}).Preview(context.Background(), "submission-1", false)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Metadata.Album != "" || !hasMetadataConflict(preview.Conflicts, "ALBUM", "") {
		t.Fatalf("conflicting album was invented: %+v conflicts=%+v", preview.Metadata, preview.Conflicts)
	}
}

type previewMemoryStore struct {
	record application.SubmissionRecord
	cached *domain.SubmissionPreview
}

func (s *previewMemoryStore) Submission(context.Context, string) (application.SubmissionRecord, error) {
	return s.record, nil
}

func (s *previewMemoryStore) CachedSubmissionPreview(_ context.Context, _ string, fingerprint string) (domain.SubmissionPreview, bool, error) {
	if s.cached != nil && s.cached.Fingerprint == fingerprint {
		return *s.cached, true, nil
	}
	return domain.SubmissionPreview{}, false, nil
}

func (s *previewMemoryStore) PutSubmissionPreview(_ context.Context, preview domain.SubmissionPreview, _ time.Time) error {
	s.cached = &preview
	return nil
}

func (s *previewMemoryStore) SaveSubmissionDraft(_ context.Context, _ string, _ domain.SubmissionDecision, _ time.Time) error {
	return nil
}

type previewInspector struct {
	tags  map[string]map[string][]string
	calls int
}

func (i *previewInspector) Inspect(_ context.Context, path string) (domain.TechnicalInfo, map[string][]string, domain.CommandEvidence, error) {
	i.calls++
	return domain.TechnicalInfo{Container: "flac", Codec: "flac", Channels: 2, DurationMS: 1000, SampleRate: 44100, BitDepth: 16}, i.tags[filepath.Base(path)], domain.CommandEvidence{}, nil
}

func hasMetadataConflict(conflicts []domain.MetadataConflict, field, path string) bool {
	for _, conflict := range conflicts {
		if conflict.Field == field && (path == "" || conflict.RelativePath == path) {
			return true
		}
	}
	return false
}
