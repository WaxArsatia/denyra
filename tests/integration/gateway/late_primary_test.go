package gateway_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type fakeSupersededCanceller struct {
	calls int
	err   error
}

func (canceller *fakeSupersededCanceller) CancelSuperseded(string) error {
	canceller.calls++
	return canceller.err
}

func TestHandoffLatePrimaryCancelsOnlyIncompleteFallback(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	prepareFallbackState(t, repositories, job, now)
	canceller := &fakeSupersededCanceller{}
	service := application.LatePrimaryService{Store: repositories, Canceller: canceller, Now: func() time.Time { return now.Add(time.Minute) }}
	if err := service.Handle(context.Background(), job.ID, latePrimaryEvidence(job, now)); err != nil {
		t.Fatal(err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryActive || canceller.calls != 1 {
		t.Fatalf("state=%s cancel calls=%d", stored.State, canceller.calls)
	}
	var pending, completed, acknowledged int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE job_id=? AND source='slskd'`, job.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM candidates WHERE job_id=?`, job.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE job_id=? AND effect_type='SPOTIFLAC_CANCEL' AND status='ACKNOWLEDGED'`, job.ID).Scan(&acknowledged); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || completed != 0 || acknowledged != 1 {
		t.Fatalf("pending=%d completed=%d acknowledged=%d", pending, completed, acknowledged)
	}
}

func TestHandoffLatePrimaryRetainsCompletedFallbackAsDualCandidate(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	prepareFallbackState(t, repositories, job, now)
	completedAt := now.Add(time.Minute)
	fallback := persistence.Candidate{ID: "fallback-candidate", JobID: job.ID, Source: "spotiflac", SourceLocator: "/data/downloads/spotiflac/job-1", CompletedAt: &completedAt, OutputSHA256: strings.Repeat("a", 64), OutputManifest: []byte(`[]`), Provenance: []byte(`{}`), ProvenanceSHA256: strings.Repeat("b", 64), CreatedAt: completedAt}
	if err := repositories.InsertCandidate(context.Background(), fallback); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: 4, To: domain.StateArbitrating, Actor: "test", Reason: "fallback complete", OccurredAt: completedAt}); err != nil {
		t.Fatal(err)
	}
	canceller := &fakeSupersededCanceller{}
	service := application.LatePrimaryService{Store: repositories, Canceller: canceller, Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if err := service.Handle(context.Background(), job.ID, latePrimaryEvidence(job, now)); err != nil {
		t.Fatal(err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateDualCandidate || canceller.calls != 0 {
		t.Fatalf("state=%s cancel calls=%d", stored.State, canceller.calls)
	}
	if _, err := repositories.Candidate(context.Background(), fallback.ID); err != nil {
		t.Fatalf("completed fallback was not retained: %v", err)
	}
}

func TestHandoffLatePrimaryCancelsNoCandidateRetryWithoutCancellingTransfer(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	prepareFallbackState(t, repositories, job, now)
	retryAt := now.Add(24 * time.Hour)
	if _, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: 4, To: domain.StateNoCandidate, Actor: "test", Reason: "all providers returned legitimate no-result", NextRetryAt: &retryAt, OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
	canceller := &fakeSupersededCanceller{}
	service := application.LatePrimaryService{Store: repositories, Canceller: canceller, Now: func() time.Time { return now.Add(time.Minute) }}
	if err := service.Handle(context.Background(), job.ID, latePrimaryEvidence(job, now)); err != nil {
		t.Fatal(err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryActive || stored.NextRetryAt != nil || canceller.calls != 0 {
		t.Fatalf("state=%s retry=%v cancel calls=%d", stored.State, stored.NextRetryAt, canceller.calls)
	}
}

func TestHandoffCancelFailureLeavesFallbackActiveAndAuditable(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	prepareFallbackState(t, repositories, job, now)
	canceller := &fakeSupersededCanceller{err: errors.New("signal failed")}
	service := application.LatePrimaryService{Store: repositories, Canceller: canceller, Now: func() time.Time { return now.Add(time.Minute) }}
	if err := service.Handle(context.Background(), job.ID, latePrimaryEvidence(job, now)); err == nil {
		t.Fatal("cancel failure reported success")
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateFallbackRunning {
		t.Fatalf("state=%s", stored.State)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM external_effects WHERE job_id=? AND effect_type='SPOTIFLAC_CANCEL'`, job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "INTENDED" {
		t.Fatalf("cancel effect status=%s", status)
	}
}

func TestHandoffLatePrimaryDoesNotReviveCancelledWantedJob(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	prepareFallbackState(t, repositories, job, now)
	if _, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{
		JobID:      job.ID,
		Expected:   4,
		To:         domain.StateCancelled,
		Actor:      "test",
		Reason:     "album is no longer wanted",
		OccurredAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	canceller := &fakeSupersededCanceller{}
	service := application.LatePrimaryService{Store: repositories, Canceller: canceller, Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if err := service.Handle(context.Background(), job.ID, latePrimaryEvidence(job, now)); err == nil {
		t.Fatal("late primary revived a cancelled Wanted job")
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateCancelled || canceller.calls != 0 {
		t.Fatalf("state=%s cancel calls=%d", stored.State, canceller.calls)
	}
	var pending, effects int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE job_id=?`, job.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_effects WHERE job_id=? AND effect_type='SPOTIFLAC_CANCEL'`, job.ID).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if pending != 0 || effects != 0 {
		t.Fatalf("cancelled job gained pending candidates=%d effects=%d", pending, effects)
	}
}

func latePrimaryEvidence(job domain.Job, now time.Time) []persistence.CorrelationEvidence {
	return []persistence.CorrelationEvidence{{JobID: job.ID, AlbumID: job.LidarrAlbumID, ReleaseGroupMBID: job.ReleaseGroupMBID, CommandID: "77", DownloadID: "primary-download", SourceKind: "history", SourceRecordID: "99", Watermark: "20", ObservedAt: now.Add(time.Minute), Evidence: []byte(`{"id":99}`), EvidenceSHA256: strings.Repeat("c", 64)}}
}
