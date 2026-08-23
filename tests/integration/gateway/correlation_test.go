package gateway_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/gateway/adapters/lidarr"
	"github.com/waxarsatia/denyra/internal/gateway/application"
	"github.com/waxarsatia/denyra/internal/gateway/domain"
	"github.com/waxarsatia/denyra/internal/gateway/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
)

func preparePrimaryReconciliation(t *testing.T, repositories *persistence.Repositories, job domain.Job, now time.Time) {
	t.Helper()
	requested, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: job.Revision, To: domain.StatePrimarySearchRequested, Actor: "test", Reason: "wanted", OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	running, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: requested.Revision, To: domain.StatePrimarySearchRunning, Actor: "test", Reason: "command accepted", OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.SetSearchContext(context.Background(), job.ID, running.Revision, "10", "20", "77", now, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := repositories.SetGraceDeadline(context.Background(), job.ID, running.Revision, now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.UpdateState(context.Background(), persistence.TransitionCommand{JobID: job.ID, Expected: running.Revision, To: domain.StatePrimaryReconciling, Actor: "test", Reason: "command successful", OccurredAt: now}); err != nil {
		t.Fatal(err)
	}
}

func TestCorrelationDelayedHistoryVisibilitySurvivesReopen(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	job := createJob(t, repositories, now)
	job.SelectedReleaseMBID = releaseMBID
	if _, err := db.Exec(`UPDATE acquisition_jobs SET selected_release_mbid=? WHERE id=?`, releaseMBID, job.ID); err != nil {
		t.Fatal(err)
	}
	preparePrimaryReconciliation(t, repositories, job, now)

	var historyPolls int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/queue":
			fmt.Fprint(writer, `{"records":[{"id":99,"albumId":999,"downloadId":"unrelated"},{"id":11,"albumId":42}],"totalRecords":2}`)
		case "/api/v1/history":
			historyPolls++
			if historyPolls == 1 {
				fmt.Fprint(writer, `{"records":[],"totalRecords":0}`)
				return
			}
			fmt.Fprint(writer, `{"records":[{"id":21,"albumId":42,"eventType":"grabbed","data":{"commandId":"77"}}],"totalRecords":1}`)
		case "/api/v1/album/42":
			fmt.Fprintf(writer, `{"id":42,"foreignAlbumId":%q,"releaseId":%q,"monitored":true}`, releaseGroupMBID, releaseMBID)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	databasePath := sqlitePath(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := denysqlite.Open(context.Background(), databasePath, denysqlite.Options{BusyTimeout: time.Second, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repositories = persistence.New(db, nil)
	clock := &advancingClock{value: now}
	service := application.PrimaryReconciler{
		Lidarr:       lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20},
		Store:        repositories,
		Policy:       domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		PageSize:     100,
		PollInterval: 2 * time.Second,
		Pause:        clock.Pause,
		Now:          clock.Now,
	}
	if err := service.Run(context.Background(), job.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StatePrimaryActive {
		t.Fatalf("state = %s", stored.State)
	}
	var source, recordID string
	var downloadID sql.NullString
	if err := db.QueryRow(`SELECT source_kind,source_record_id,download_id FROM correlation_evidence WHERE job_id=?`, job.ID).Scan(&source, &recordID, &downloadID); err != nil {
		t.Fatal(err)
	}
	if source != "history" || recordID != "21" || downloadID.Valid {
		t.Fatalf("evidence = source=%q record=%q download=%+v", source, recordID, downloadID)
	}
}

func TestCorrelationFullGraceWithoutGrabAuthorizesFallback(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	preparePrimaryReconciliation(t, repositories, job, now)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/api/v1/queue" || request.URL.Path == "/api/v1/history" {
			fmt.Fprint(writer, `{"records":[],"totalRecords":0}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	clock := &advancingClock{value: now}
	service := application.PrimaryReconciler{
		Lidarr:       lidarr.Client{BaseURL: server.URL, APIKey: "test-key", HTTP: server.Client(), ResponseLimit: 1 << 20},
		Store:        repositories,
		Policy:       domain.RetryPolicy{Primary: []time.Duration{time.Minute}, Fallback: []time.Duration{5 * time.Minute}, NoCandidate: 24 * time.Hour},
		PageSize:     100,
		PollInterval: 2 * time.Second,
		Pause:        clock.Pause,
		Now:          clock.Now,
	}
	if err := service.Run(context.Background(), job.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	stored, err := repositories.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != domain.StateFallbackRunning || !clock.Now().Equal(now.Add(time.Minute)) {
		t.Fatalf("fallback job=%+v time=%s", stored, clock.Now())
	}
}

func TestCorrelationEvidenceIsIdempotentButImmutable(t *testing.T) {
	db, repositories, now := gatewayRepositories(t)
	defer db.Close()
	job := createJob(t, repositories, now)
	evidence := persistence.CorrelationEvidence{JobID: job.ID, AlbumID: 42, ReleaseGroupMBID: releaseGroupMBID, SourceKind: "history", SourceRecordID: "21", Watermark: "20", ObservedAt: now, Evidence: []byte(`{"id":21}`), EvidenceSHA256: "hash-a"}
	if err := repositories.InsertCorrelationEvidence(context.Background(), evidence); err != nil {
		t.Fatal(err)
	}
	if err := repositories.InsertCorrelationEvidence(context.Background(), evidence); err != nil {
		t.Fatalf("duplicate evidence: %v", err)
	}
	evidence.EvidenceSHA256 = "hash-b"
	if err := repositories.InsertCorrelationEvidence(context.Background(), evidence); err == nil {
		t.Fatal("changed evidence accepted under the same source identity")
	}
}

func sqlitePath(t *testing.T, db *sql.DB) string {
	t.Helper()
	var sequence int
	var name, path string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(path)
}
