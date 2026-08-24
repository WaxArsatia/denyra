package pipeline_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/adapters/media"
	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestMutationIsPicardCompatibleIdempotentAndPreservesAudioFrames(t *testing.T) {
	root := t.TempDir()
	work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidate := filepath.Join(work, "candidate-1")
	if err := os.MkdirAll(candidate, 0o750); err != nil {
		t.Fatal(err)
	}
	fixtures := generateFLACFixtures(t)
	track := filepath.Join(candidate, "01.flac")
	copyFile(t, filepath.Join(fixtures, "mono-16-44100.flac"), track)
	runCommand(t, "metaflac", "--set-tag=CUSTOM_FIELD=preserve me", "--set-tag=MUSICBRAINZ_RECORDINGID=legacy", track)
	cover := filepath.Join(root, "cover.jpg")
	runCommand(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=16x16", "-frames:v", "1", cover)
	runCommand(t, "metaflac", "--import-picture-from="+cover, track)

	tags := canonicalMutationTags(t)
	service := realMutationService(work, quarantine, media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: media.Runner{}})
	first, err := service.MutateRelease(context.Background(), "candidate-1", map[string]domain.TagSet{"01.flac": tags})
	if err != nil || !first.Approved || first.Quarantined || len(first.Files) != 1 {
		t.Fatalf("first mutation = %+v, %v", first, err)
	}
	evidence := first.Files[0]
	if evidence.BeforeSHA256 == evidence.AfterSHA256 || evidence.AudioMD5 == "" || evidence.RemovedPictures != 1 {
		t.Fatalf("mutation evidence = %+v", evidence)
	}
	if !reflect.DeepEqual(evidence.AfterTags["ARTIST"], []string{"First Artist", "Second Artist"}) || !reflect.DeepEqual(evidence.AfterTags["MUSICBRAINZ_ARTISTID"], []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}) {
		t.Fatalf("repeated values were flattened or misaligned: %+v", evidence.AfterTags)
	}
	if !reflect.DeepEqual(evidence.AfterTags["CUSTOM_FIELD"], []string{"preserve me"}) || evidence.AfterTags["MUSICBRAINZ_RECORDINGID"] != nil {
		t.Fatalf("unknown/legacy tag handling = %+v", evidence.AfterTags)
	}

	second, err := service.MutateRelease(context.Background(), "candidate-1", map[string]domain.TagSet{"01.flac": tags})
	if err != nil || !second.Approved || second.Files[0].BeforeSHA256 != second.Files[0].AfterSHA256 || second.Files[0].AfterSHA256 != evidence.AfterSHA256 {
		t.Fatalf("repeat mutation is not idempotent: %+v, %v", second, err)
	}
}

func TestMutationPreservesUTF8TagsUnderPOSIXParentLocale(t *testing.T) {
	root := t.TempDir()
	work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidate := filepath.Join(work, "candidate-utf8")
	if err := os.MkdirAll(candidate, 0o750); err != nil {
		t.Fatal(err)
	}
	fixtures := generateFLACFixtures(t)
	track := filepath.Join(candidate, "08.flac")
	copyFile(t, filepath.Join(fixtures, "mono-16-44100.flac"), track)
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "POSIX")

	tags := canonicalMutationTags(t)
	tags["TITLE"] = []string{"Hati‐Hati di Jalan"}
	service := realMutationService(work, quarantine, media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: media.Runner{}})
	result, err := service.MutateRelease(context.Background(), "candidate-utf8", map[string]domain.TagSet{"08.flac": tags})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Approved || len(result.Files) != 1 {
		t.Fatalf("mutation result=%+v", result)
	}
	if got := result.Files[0].AfterTags["TITLE"]; !reflect.DeepEqual(got, []string{"Hati‐Hati di Jalan"}) {
		t.Fatalf("UTF-8 title=%q", got)
	}
}

func TestUnmanagedMutationPreservesIdentifiersUnknownTagsAudioAndPictures(t *testing.T) {
	root := t.TempDir()
	work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidate := filepath.Join(work, "candidate-unmanaged")
	if err := os.MkdirAll(candidate, 0o750); err != nil {
		t.Fatal(err)
	}
	fixtures := generateFLACFixtures(t)
	track := filepath.Join(candidate, "01.flac")
	copyFile(t, filepath.Join(fixtures, "mono-16-44100.flac"), track)
	runCommand(t, "metaflac", "--set-tag=UPC=123456789012", "--set-tag=ISRC=USAAA2600001", "--set-tag=SOURCE_URL=https://example.invalid/source", "--set-tag=CUSTOM=keep", track)
	cover := filepath.Join(root, "cover.jpg")
	runCommand(t, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=black:s=16x16", "-frames:v", "1", cover)
	runCommand(t, "metaflac", "--import-picture-from="+cover, track)
	runner := media.Runner{MaxOutput: 1 << 20}
	metaflac := media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner}
	before, _, err := metaflac.ReadTags(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	desired := before
	for field, value := range map[string]string{"TITLE": "Untukmu", "ARTIST": "Kaleb J", "ALBUM": "OFF GUARD", "ALBUMARTIST": "Kaleb J", "TRACKNUMBER": "1", "TRACKTOTAL": "1", "DISCNUMBER": "1", "DISCTOTAL": "1"} {
		desired[field] = []string{value}
	}
	service := application.MutationService{WorkRoot: work, QuarantineRoot: quarantine, Tags: metaflac, Integrity: media.FLAC{Binary: "flac", Version: "test", Timeout: 10 * time.Second, Runner: runner}, Checksum: media.SHA256}
	result, err := service.MutateUnmanagedRelease(context.Background(), "candidate-unmanaged", map[string]domain.TagSet{"01.flac": desired})
	if err != nil || !result.Approved || result.Quarantined {
		t.Fatalf("unmanaged mutation=%+v err=%v", result, err)
	}
	after := result.Files[0].AfterTags
	for _, field := range []string{"UPC", "ISRC", "SOURCE_URL", "CUSTOM"} {
		if !reflect.DeepEqual(after[field], before[field]) {
			t.Fatalf("preserved tag %s changed: before=%v after=%v", field, before[field], after[field])
		}
	}
	pictures, _, err := metaflac.PictureCount(context.Background(), track)
	if err != nil || pictures != 1 || result.Files[0].AudioMD5 == "" {
		t.Fatalf("pictures=%d evidence=%+v err=%v", pictures, result.Files[0], err)
	}
}

func TestMutationPostIntegrityFailureQuarantinesWholeRelease(t *testing.T) {
	root := t.TempDir()
	work, quarantine := filepath.Join(root, "work"), filepath.Join(root, "quarantine")
	candidate := filepath.Join(work, "candidate-2")
	if err := os.MkdirAll(candidate, 0o750); err != nil {
		t.Fatal(err)
	}
	fixtures := generateFLACFixtures(t)
	copyFile(t, filepath.Join(fixtures, "mono-16-44100.flac"), filepath.Join(candidate, "01.flac"))
	service := realMutationService(work, quarantine, failingIntegrity{})
	result, err := service.MutateRelease(context.Background(), "candidate-2", map[string]domain.TagSet{"01.flac": canonicalMutationTags(t)})
	if err != nil || result.Approved || !result.Quarantined || result.Path != filepath.Join(quarantine, "candidate-2") {
		t.Fatalf("post-check failure = %+v, %v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(result.Path, "01.flac")); statErr != nil {
		t.Fatalf("complete release not retained in quarantine: %v", statErr)
	}
}

type failingIntegrity struct{}

func (failingIntegrity) Test(context.Context, string) (domain.CommandEvidence, error) {
	return domain.CommandEvidence{Tool: "flac", Version: "test", ExitStatus: 1}, errors.New("injected post-check failure")
}

func realMutationService(work, quarantine string, integrity application.IntegrityTester) application.MutationService {
	runner := media.Runner{MaxOutput: 1 << 20}
	return application.MutationService{
		WorkRoot: work, QuarantineRoot: quarantine,
		Tags:      media.MetaFLAC{Binary: "metaflac", Version: "test", Timeout: 10 * time.Second, Runner: runner},
		Integrity: integrity, Checksum: media.SHA256,
	}
}

func canonicalMutationTags(t *testing.T) domain.TagSet {
	t.Helper()
	tags, err := domain.CanonicalTags(domain.TagInput{
		Title: "Track", Album: "Album", Date: "2026-08", TrackNumber: 1, DiscNumber: 1,
		Artists:      []domain.ArtistCredit{{Name: "First Artist", ArtistMBID: "11111111-1111-1111-1111-111111111111"}, {Name: "Second Artist", ArtistMBID: "22222222-2222-2222-2222-222222222222"}},
		AlbumArtists: []domain.ArtistCredit{{Name: "Various Artists", ArtistMBID: "33333333-3333-3333-3333-333333333333"}},
		Genres:       []string{"Rock", "Ambient"}, ISRCs: []string{"USAAA2600001"},
		RecordingMBID: "44444444-4444-4444-4444-444444444444", ReleaseTrackMBID: "55555555-5555-5555-5555-555555555555",
		ReleaseMBID: "66666666-6666-6666-6666-666666666666", ReleaseGroupMBID: "77777777-7777-7777-7777-777777777777",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tags
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runCommand(t *testing.T, name string, arguments ...string) {
	t.Helper()
	if output, err := exec.Command(name, arguments...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
}
