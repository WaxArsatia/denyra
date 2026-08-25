package persistence_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/domain"
	"github.com/waxarsatia/denyra/internal/pipeline/persistence"
	denysqlite "github.com/waxarsatia/denyra/internal/platform/sqlite"
	"github.com/waxarsatia/denyra/migrations"
)

func TestUploadSessionSummariesAggregateWithoutManifests(t *testing.T) {
	db, repository, now := uploadSummaryRepository(t)
	seedUploadSessions(t, repository, now, 50, 100)

	summaries, err := repository.UploadSessionSummaries(context.Background(), "admin-1", 100)
	if err != nil || len(summaries) != 50 {
		t.Fatalf("summaries=%d err=%v", len(summaries), err)
	}
	for _, summary := range summaries {
		if summary.FileCount != 100 || summary.CompleteCount != 50 || summary.Status != domain.UploadSessionOpen {
			t.Fatalf("invalid aggregate: %+v", summary)
		}
	}

	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT s.id,COUNT(e.id) FROM upload_sessions s LEFT JOIN upload_entries e ON e.session_id=s.id WHERE s.actor=? AND s.status<>'DELETED' GROUP BY s.id ORDER BY s.updated_at DESC,s.id DESC LIMIT ?`, "admin-1", 100)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if !strings.Contains(plan.String(), "upload_sessions_actor_status_updated") || !strings.Contains(plan.String(), "upload_entries_session_status") {
		t.Fatalf("summary query misses session or entry index: %s", plan.String())
	}
}

func BenchmarkUploadSessionSummaries50(b *testing.B) {
	_, repository, now := uploadSummaryRepository(b)
	seedUploadSessions(b, repository, now, 50, 100)
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := repository.UploadSessionSummaries(context.Background(), "admin-1", 100); err != nil {
			b.Fatal(err)
		}
	}
}

func uploadSummaryRepository(t testing.TB) (*sql.DB, *persistence.Repositories, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := denysqlite.Open(ctx, filepath.Join(t.TempDir(), "pipeline.db"), denysqlite.Options{BusyTimeout: 5 * time.Second, MaxOpenConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	serviceMigrations, err := migrations.For("pipeline")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if err := denysqlite.Migrate(ctx, db, serviceMigrations, now); err != nil {
		t.Fatal(err)
	}
	return db, persistence.New(db, func() time.Time { return now }), now
}

func seedUploadSessions(t testing.TB, repository *persistence.Repositories, now time.Time, sessionCount, entryCount int) {
	t.Helper()
	for sessionIndex := 0; sessionIndex < sessionCount; sessionIndex++ {
		files := make([]domain.UploadFileSpec, entryCount)
		for entryIndex := range files {
			status := domain.UploadEntryPending
			if entryIndex%2 == 0 {
				status = domain.UploadEntryComplete
			}
			files[entryIndex] = domain.UploadFileSpec{ID: fmt.Sprintf("entry-%03d-%03d", sessionIndex, entryIndex), RelativePath: fmt.Sprintf("%03d.flac", entryIndex), MediaType: "audio/flac", SizeBytes: 1, Status: status}
		}
		session := domain.UploadSession{ID: fmt.Sprintf("session-%03d", sessionIndex), SubmissionID: fmt.Sprintf("submission-%03d", sessionIndex), Actor: "admin-1", Status: domain.UploadSessionOpen, Files: files, CreatedAt: now, UpdatedAt: now.Add(time.Duration(sessionIndex) * time.Second)}
		if err := repository.CreateUploadSession(context.Background(), session); err != nil {
			t.Fatal(err)
		}
	}
}
