package domain_test

import (
	"testing"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestValidateUploadManifestAcceptsSafeBoundedAlbum(t *testing.T) {
	t.Parallel()
	manifest := domain.UploadManifest{Files: []domain.UploadFileSpec{
		{RelativePath: "OFF GUARD/01 - Track.flac", SizeBytes: 12, MediaType: "audio/flac"},
		{RelativePath: "OFF GUARD/cover.jpg", SizeBytes: 8, MediaType: "image/jpeg"},
	}}
	if err := domain.ValidateUploadManifest(manifest, domain.UploadLimits{MaxFileBytes: 20, MaxSessionBytes: 30, MaxEntries: 3}); err != nil {
		t.Fatalf("safe manifest rejected: %v", err)
	}
}

func TestValidateUploadManifestRejectsUnsafePathsAndNormalizedCollisions(t *testing.T) {
	t.Parallel()
	tests := map[string][]string{
		"parent traversal":  {"../track.flac"},
		"nested traversal":  {"album/../track.flac"},
		"absolute":          {"/album/track.flac"},
		"backslash":         {`album\track.flac`},
		"empty segment":     {"album//track.flac"},
		"dot segment":       {"album/./track.flac"},
		"case collision":    {"Album/Track.flac", "album/track.flac"},
		"unicode collision": {"Album/Cafe\u0301.flac", "Album/Caf\u00e9.flac"},
	}
	for name, paths := range tests {
		t.Run(name, func(t *testing.T) {
			files := make([]domain.UploadFileSpec, len(paths))
			for index, path := range paths {
				files[index] = domain.UploadFileSpec{RelativePath: path, SizeBytes: 1}
			}
			if err := domain.ValidateUploadManifest(domain.UploadManifest{Files: files}, domain.UploadLimits{MaxFileBytes: 10, MaxSessionBytes: 20, MaxEntries: 10}); err == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}

func TestValidateUploadManifestEnforcesEntryAndByteLimits(t *testing.T) {
	t.Parallel()
	tests := map[string]domain.UploadManifest{
		"empty":         {},
		"too many":      {Files: []domain.UploadFileSpec{{RelativePath: "a.flac", SizeBytes: 1}, {RelativePath: "b.flac", SizeBytes: 1}}},
		"file too big":  {Files: []domain.UploadFileSpec{{RelativePath: "a.flac", SizeBytes: 11}}},
		"total too big": {Files: []domain.UploadFileSpec{{RelativePath: "a.flac", SizeBytes: 8}, {RelativePath: "b.flac", SizeBytes: 8}}},
		"negative":      {Files: []domain.UploadFileSpec{{RelativePath: "a.flac", SizeBytes: -1}}},
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if err := domain.ValidateUploadManifest(manifest, domain.UploadLimits{MaxFileBytes: 10, MaxSessionBytes: 15, MaxEntries: 1}); err == nil {
				t.Fatal("out-of-policy manifest accepted")
			}
		})
	}
}
