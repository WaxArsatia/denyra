ALTER TABLE candidate_approvals ADD COLUMN config_snapshot_id TEXT NOT NULL DEFAULT '';
ALTER TABLE candidate_approvals ADD COLUMN musicbrainz_release_id TEXT NOT NULL DEFAULT '';
ALTER TABLE candidate_approvals ADD COLUMN warnings_json BLOB NOT NULL DEFAULT '[]';
ALTER TABLE candidate_approvals ADD COLUMN pipeline_state_revision INTEGER NOT NULL DEFAULT 0;
ALTER TABLE candidate_approvals ADD COLUMN request_json BLOB NOT NULL DEFAULT '{}';
ALTER TABLE candidate_approvals ADD COLUMN request_sha256 TEXT NOT NULL DEFAULT '';

CREATE TRIGGER candidate_approvals_no_update BEFORE UPDATE ON candidate_approvals BEGIN SELECT RAISE(ABORT,'candidate approval is immutable'); END;
CREATE TRIGGER candidate_approvals_no_delete BEFORE DELETE ON candidate_approvals BEGIN SELECT RAISE(ABORT,'candidate approval is immutable'); END;

ALTER TABLE idempotency_records ADD COLUMN request_body BLOB;
