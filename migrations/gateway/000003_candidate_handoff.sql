CREATE TABLE pending_acquisition_candidates (
 candidate_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),
 source TEXT NOT NULL CHECK(source IN ('slskd','spotiflac')),
 source_locator TEXT NOT NULL,
 download_id TEXT,
 provenance_json BLOB NOT NULL,
 provenance_sha256 TEXT NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(job_id,source,source_locator)
);
CREATE TRIGGER pending_acquisition_candidates_no_update BEFORE UPDATE ON pending_acquisition_candidates BEGIN SELECT RAISE(ABORT,'pending acquisition registration is immutable'); END;
CREATE TRIGGER pending_acquisition_candidates_no_delete BEFORE DELETE ON pending_acquisition_candidates BEGIN SELECT RAISE(ABORT,'pending acquisition registration is immutable'); END;

CREATE TABLE candidate_output_evidence (
 candidate_id TEXT PRIMARY KEY REFERENCES candidates(candidate_id),
 output_sha256 TEXT NOT NULL CHECK(length(output_sha256)=64),
 manifest_json BLOB NOT NULL,
 manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256)=64),
 completed_at TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE TRIGGER candidate_output_evidence_no_update BEFORE UPDATE ON candidate_output_evidence BEGIN SELECT RAISE(ABORT,'candidate output evidence is immutable'); END;
CREATE TRIGGER candidate_output_evidence_no_delete BEFORE DELETE ON candidate_output_evidence BEGIN SELECT RAISE(ABORT,'candidate output evidence is immutable'); END;
