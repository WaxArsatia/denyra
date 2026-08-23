ALTER TABLE completion_evidence ADD COLUMN target_release_mbid TEXT;

CREATE TABLE candidate_workflow (
 candidate_id TEXT PRIMARY KEY REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
 target_release_mbid TEXT,
 canonical_release_json BLOB,
 release_match_json BLOB,
 technical_result_json BLOB,
 warnings_json BLOB,
 download_id TEXT,
 updated_at TEXT NOT NULL
);
