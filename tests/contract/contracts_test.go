package contract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/contracts"
)

func TestCandidateAcceptedGoldenJSON(t *testing.T) {
	t.Parallel()
	value := contracts.CandidateAccepted{
		RequestID: "req-1", JobID: "job-1", CandidateID: "candidate-1", ConfigSnapshotID: "config-1",
		Source: contracts.SourceSpotiFLAC, Path: "/data/downloads/spotiflac/job-1", CompletionAt: mustTime("2026-08-24T10:00:00Z"),
		Provenance: contracts.AcquisitionProvenance{Provider: "ext:tidal-web", EngineVersion: "3.0.8", OutputSHA256: strings.Repeat("a", 64)},
	}
	assertGolden(t, "candidate_accepted.json", value)
}

func TestCandidateWinnerGoldenJSON(t *testing.T) {
	t.Parallel()
	value := contracts.CandidateWinner{
		RequestID: "req-2", JobID: "job-1", CandidateID: "candidate-1", ConfigSnapshotID: "config-1",
		WinnerLockedAt: mustTime("2026-08-24T10:30:00Z"), Reason: "quality vector and provenance priority",
		Quality:       contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96000, QualityWarningCount: 0},
		StateRevision: 7,
	}
	assertGolden(t, "candidate_winner.json", value)
}

func TestCandidateApprovedGoldenJSON(t *testing.T) {
	t.Parallel()
	value := contracts.CandidateApproved{
		RequestID: "approval-1", JobID: "job-1", CandidateID: "candidate-1", ConfigSnapshotID: "pipeline-config-1",
		MusicBrainzReleaseID: "abcdefab-1234-5678-9abc-abcdefabcdef", ApprovedAt: mustTime("2026-08-24T10:20:00Z"),
		Quality:       contracts.QualityVector{IdentityRank: 4, EditionRank: 2, SourceConfidence: 90, BitDepth: 24, SampleRate: 96_000, QualityWarningCount: 1},
		Warnings:      []contracts.Warning{{Class: contracts.QualityWarning, Code: "LOSSY_HEURISTIC", Message: "possible lossy source"}, {Class: contracts.NonBlockingWarning, Code: "LYRICS_MISSING", Message: "lyrics unavailable"}},
		StateRevision: 7,
	}
	assertGolden(t, "candidate_approved.json", value)
}

func TestStrictDecodeRejectsUnknownFieldsAndOversize(t *testing.T) {
	t.Parallel()
	valid := `{"request_id":"req","job_id":"job","candidate_id":"candidate","config_snapshot_id":"config","source":"SLSKD","path":"/data/downloads/slskd/job","completion_at":"2026-08-24T10:00:00Z","provenance":{"provider":"slskd","engine_version":"0.26.0","output_sha256":"` + strings.Repeat("a", 64) + `"}}`
	for name, payload := range map[string]string{
		"unknown":  strings.Replace(valid, `"request_id":"req"`, `"request_id":"req","unknown":true`, 1),
		"oversize": valid + strings.Repeat(" ", 16),
	} {
		t.Run(name, func(t *testing.T) {
			var decoded contracts.CandidateAccepted
			limit := int64(len(valid))
			if err := contracts.DecodeStrictJSON(strings.NewReader(payload), limit, &decoded); err == nil {
				t.Fatal("DecodeStrictJSON accepted invalid payload")
			}
		})
	}
}

func TestIdempotencyReuseWithDifferentRequestHashConflicts(t *testing.T) {
	t.Parallel()
	record := contracts.IdempotencyRecord{Key: "key-1", Operation: "candidate.accept", RequestHash: strings.Repeat("a", 64)}
	if err := record.ValidateReuse("candidate.accept", strings.Repeat("a", 64)); err != nil {
		t.Fatalf("same request rejected: %v", err)
	}
	if err := record.ValidateReuse("candidate.accept", strings.Repeat("b", 64)); err != contracts.ErrIdempotencyConflict {
		t.Fatalf("different hash error = %v", err)
	}
	if err := record.ValidateReuse("candidate.winner", strings.Repeat("a", 64)); err != contracts.ErrIdempotencyConflict {
		t.Fatalf("different operation error = %v", err)
	}
}

func assertGolden(t *testing.T, name string, value any) {
	t.Helper()
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("golden", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Fatalf("golden mismatch for %s\ngot:\n%s\nwant:\n%s", name, got, want)
	}
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}
