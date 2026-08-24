package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/waxarsatia/denyra/internal/pipeline/application"
	"github.com/waxarsatia/denyra/internal/pipeline/domain"
)

func TestRecordMutationReusesImmutableSnapshotsOnRetry(t *testing.T) {
	db, repository, now := pipelineRepositories(t)
	defer db.Close()
	candidate := createPersistedCandidate(t, repository, now)
	if _, err := db.Exec(`INSERT INTO candidate_files(id,candidate_id,relative_path,size_bytes,mtime_ns,device,inode,sha256_before,created_at) VALUES('file-1',?,'01.flac',1,1,1,1,'before-sha',?)`, candidate.ID, now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	result := application.MutationResult{Files: []application.MutationEvidence{{
		RelativePath: "01.flac",
		BeforeTags:   domain.TagSet{"TITLE": {"Original"}},
		AfterTags:    domain.TagSet{"TITLE": {"Canonical"}},
		BeforeSHA256: "before-sha",
		AfterSHA256:  "after-sha",
	}}}
	if err := repository.RecordMutation(context.Background(), candidate.ID, result, now); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordMutation(context.Background(), candidate.ID, result, now.Add(time.Second)); err != nil {
		t.Fatalf("record identical retry mutation: %v", err)
	}
	var mutations, snapshots int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mutations WHERE candidate_id=?`, candidate.ID).Scan(&mutations); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM metadata_snapshots WHERE candidate_id=?`, candidate.ID).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if mutations != 2 || snapshots != 2 {
		t.Fatalf("mutations=%d snapshots=%d, want 2 retry attempts sharing 2 immutable snapshots", mutations, snapshots)
	}
}
