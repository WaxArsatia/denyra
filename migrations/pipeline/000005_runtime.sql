CREATE TABLE completion_evidence (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL UNIQUE REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    gateway_config_snapshot_id TEXT NOT NULL,
    source_path TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    provenance_json BLOB NOT NULL,
    provenance_sha256 TEXT NOT NULL,
    received_at TEXT NOT NULL
);

CREATE TRIGGER completion_evidence_no_update BEFORE UPDATE ON completion_evidence BEGIN SELECT RAISE(ABORT, 'completion evidence is immutable'); END;
CREATE TRIGGER completion_evidence_no_delete BEFORE DELETE ON completion_evidence BEGIN SELECT RAISE(ABORT, 'completion evidence is immutable'); END;

CREATE TABLE recovery_findings (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    path TEXT,
    classification TEXT NOT NULL,
    details_json BLOB NOT NULL,
    observed_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE INDEX recovery_findings_open ON recovery_findings(resolved_at, observed_at);
