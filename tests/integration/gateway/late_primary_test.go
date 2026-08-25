package gateway_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
)

type latePrimaryObservations struct {
	history []lidarr.HistoryRecord
	album   lidarr.WantedAlbum
}

func (observations latePrimaryObservations) QueueAfter(context.Context, string, int) ([]lidarr.QueueRecord, error) {
	return nil, nil
}
func (observations latePrimaryObservations) HistoryAfter(context.Context, string, int) ([]lidarr.HistoryRecord, error) {
	return observations.history, nil
}
func (observations latePrimaryObservations) Album(context.Context, int64) (lidarr.WantedAlbum, error) {
	return observations.album, nil
}

func TestLatePrimaryMonitorPostGraceUsesOverallDeadline(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := prepareLatePrimaryJob(t, db, repositories, now)
	record := matchingLateHistory(job, now.Add(2*time.Minute), 21)
	monitor := latePrimaryMonitor(repositories, job, now.Add(3*time.Minute), []lidarr.HistoryRecord{record})
	changed, err := monitor.Reconcile(context.Background())
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	var pending, evidence int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE job_id=? AND source='slskd'`, job.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM correlation_evidence WHERE job_id=?`, job.ID).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || evidence != 1 {
		t.Fatalf("pending=%d evidence=%d", pending, evidence)
	}
}

func TestLatePrimaryMonitorRejectsUnrelatedOrLateEvidence(t *testing.T) {
	tests := map[string]func(domain.Job, time.Time) lidarr.HistoryRecord{
		"wrong album": func(job domain.Job, observed time.Time) lidarr.HistoryRecord {
			record := matchingLateHistory(job, observed, 21)
			record.AlbumID++
			return record
		},
		"wrong release group": func(job domain.Job, observed time.Time) lidarr.HistoryRecord {
			record := matchingLateHistory(job, observed, 21)
			record.Data["releaseGroupId"] = "87654321-4321-4321-4321-cba987654321"
			return record
		},
		"stale watermark": func(job domain.Job, observed time.Time) lidarr.HistoryRecord {
			return matchingLateHistory(job, observed, 20)
		},
		"after overall deadline": func(job domain.Job, _ time.Time) lidarr.HistoryRecord {
			return matchingLateHistory(job, time.Date(2026, 8, 24, 7, 0, 0, 0, time.UTC), 21)
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			db, repositories, now := gatewayRepositories(t)
			defer db.Close()
			job := prepareLatePrimaryJob(t, db, repositories, now)
			monitor := latePrimaryMonitor(repositories, job, now.Add(3*time.Minute), []lidarr.HistoryRecord{build(job, now.Add(2*time.Minute))})
			changed, err := monitor.Reconcile(context.Background())
			if err != nil || changed != 0 {
				t.Fatalf("changed=%d err=%v", changed, err)
			}
			var pending int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pending_acquisition_candidates WHERE job_id=?`, job.ID).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending != 0 {
				t.Fatalf("pending=%d", pending)
			}
		})
	}
}

func prepareLatePrimaryJob(t *testing.T, db interface {
	Exec(string, ...any) (sql.Result, error)
}, repositories *persistence.Repositories, now time.Time) domain.Job {
	t.Helper()
	job := createJob(t, repositories, now)
	job.SelectedReleaseMBID = releaseMBID
	_, err := db.Exec(`UPDATE acquisition_jobs SET selected_release_mbid=?,state='FALLBACK_RUNNING',state_revision=4,queue_watermark='10',history_watermark='20',command_id='77',correlation_started_at=?,command_deadline=?,grace_deadline=?,overall_deadline=?,updated_at=? WHERE id=?`,
		releaseMBID, now.Format(time.RFC3339Nano), now.Add(10*time.Minute).Format(time.RFC3339Nano), now.Add(time.Minute).Format(time.RFC3339Nano), now.Add(6*time.Hour).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func matchingLateHistory(job domain.Job, observed time.Time, id int64) lidarr.HistoryRecord {
	return lidarr.HistoryRecord{ID: id, AlbumID: job.LidarrAlbumID, DownloadID: "late-download", EventType: "grabbed", Date: observed.Format(time.RFC3339Nano), Data: map[string]any{"commandId": "77", "releaseGroupId": job.ReleaseGroupMBID, "releaseId": job.SelectedReleaseMBID, "downloadId": "late-download"}}
}

func latePrimaryMonitor(repositories *persistence.Repositories, job domain.Job, now time.Time, history []lidarr.HistoryRecord) application.LatePrimaryMonitor {
	observations := latePrimaryObservations{history: history, album: lidarr.WantedAlbum{AlbumID: job.LidarrAlbumID, ReleaseGroupMBID: job.ReleaseGroupMBID, SelectedReleaseMBID: job.SelectedReleaseMBID, Monitored: true}}
	return application.LatePrimaryMonitor{
		Store:      repositories,
		Reconciler: application.PrimaryReconciler{Lidarr: observations, Store: repositories, PageSize: 100, Now: func() time.Time { return now }},
		Handler:    application.LatePrimaryService{Store: repositories, Canceller: &fakeSupersededCanceller{}, Now: func() time.Time { return now }},
	}
}

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
