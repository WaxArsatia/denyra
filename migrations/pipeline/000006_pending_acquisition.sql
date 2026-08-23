CREATE TABLE pending_acquisition_candidates (
 candidate_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('slskd','spotiflac')),
 source_locator TEXT NOT NULL,
 download_id TEXT,
 gateway_config_snapshot_id TEXT NOT NULL,
 registration_json BLOB NOT NULL,
 registration_sha256 TEXT NOT NULL,
 registered_at TEXT NOT NULL,
 UNIQUE(job_id,source,source_locator)
);
CREATE TRIGGER pending_acquisition_candidates_no_update BEFORE UPDATE ON pending_acquisition_candidates BEGIN SELECT RAISE(ABORT,'pending acquisition registration is immutable'); END;
CREATE TRIGGER pending_acquisition_candidates_no_delete BEFORE DELETE ON pending_acquisition_candidates BEGIN SELECT RAISE(ABORT,'pending acquisition registration is immutable'); END;
