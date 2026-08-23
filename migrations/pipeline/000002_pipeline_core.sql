CREATE TABLE roles (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT NOT NULL,
    password_changed_at TEXT NOT NULL,
    disabled_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE user_roles (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    granted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    granted_at TEXT NOT NULL,
    PRIMARY KEY (user_id, role_id)
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    csrf_hash BLOB NOT NULL CHECK (length(csrf_hash) = 32),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    revocation_reason TEXT
);

CREATE TABLE submissions (
    id TEXT PRIMARY KEY,
    source_path TEXT NOT NULL UNIQUE,
    sealed_fingerprint TEXT,
    status TEXT NOT NULL,
    state_revision INTEGER NOT NULL DEFAULT 0 CHECK (state_revision >= 0),
    submitted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    submitted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE candidates (
    candidate_id TEXT PRIMARY KEY,
    source TEXT NOT NULL CHECK (source IN ('slskd','spotiflac','other','manual')),
    release_directory TEXT NOT NULL,
    config_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id) ON DELETE RESTRICT,
    acquisition_evidence_id TEXT NOT NULL,
    gateway_job_id TEXT,
    state TEXT NOT NULL,
    state_revision INTEGER NOT NULL CHECK (state_revision >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (source, acquisition_evidence_id)
);

CREATE TRIGGER candidates_identity_immutable
BEFORE UPDATE ON candidates
WHEN OLD.candidate_id <> NEW.candidate_id
  OR OLD.source <> NEW.source
  OR OLD.config_snapshot_id <> NEW.config_snapshot_id
  OR OLD.acquisition_evidence_id <> NEW.acquisition_evidence_id
  OR COALESCE(OLD.gateway_job_id, '') <> COALESCE(NEW.gateway_job_id, '')
  OR OLD.created_at <> NEW.created_at
BEGIN
    SELECT RAISE(ABORT, 'candidate identity is immutable');
END;

CREATE TABLE candidate_files (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    relative_path TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    mtime_ns INTEGER NOT NULL,
    device INTEGER NOT NULL,
    inode INTEGER NOT NULL,
    sha256_before TEXT,
    sha256_after TEXT,
    created_at TEXT NOT NULL,
    UNIQUE (candidate_id, relative_path)
);

CREATE TABLE validation_results (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    scope TEXT NOT NULL,
    subject TEXT NOT NULL,
    classification TEXT NOT NULL,
    code TEXT NOT NULL,
    evidence_json BLOB NOT NULL,
    evidence_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE track_matches (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    candidate_file_id TEXT NOT NULL REFERENCES candidate_files(id) ON DELETE RESTRICT,
    release_mbid TEXT NOT NULL,
    recording_mbid TEXT NOT NULL,
    release_track_mbid TEXT NOT NULL,
    medium_position INTEGER NOT NULL CHECK (medium_position > 0),
    track_position INTEGER NOT NULL CHECK (track_position > 0),
    reference_duration_ms INTEGER,
    observed_duration_ms INTEGER NOT NULL,
    status TEXT NOT NULL,
    evidence_json BLOB NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (candidate_id, candidate_file_id)
);

CREATE TABLE metadata_snapshots (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    candidate_file_id TEXT REFERENCES candidate_files(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('ORIGINAL','CANONICAL','FINAL','BEETS_ADVISORY')),
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (candidate_id, candidate_file_id, kind, sha256)
);

CREATE TABLE mutations (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    candidate_file_id TEXT NOT NULL REFERENCES candidate_files(id) ON DELETE RESTRICT,
    before_snapshot_id TEXT NOT NULL REFERENCES metadata_snapshots(id) ON DELETE RESTRICT,
    after_snapshot_id TEXT REFERENCES metadata_snapshots(id) ON DELETE RESTRICT,
    invocation_json BLOB NOT NULL,
    diff_json BLOB NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE enrichments (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    candidate_file_id TEXT REFERENCES candidate_files(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('LYRICS','ARTWORK')),
    provider TEXT NOT NULL,
    classification TEXT NOT NULL,
    evidence_json BLOB NOT NULL,
    evidence_sha256 TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE import_intents (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL UNIQUE REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    target_release_mbid TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    release_manifest_json BLOB NOT NULL,
    download_id TEXT,
    status TEXT NOT NULL,
    response_json BLOB,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE idempotency_records (
    key TEXT PRIMARY KEY,
    scope TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_status INTEGER,
    response_body BLOB,
    created_at TEXT NOT NULL,
    completed_at TEXT
);

CREATE TABLE leases (
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    acquired_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    config_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id) ON DELETE RESTRICT,
    resource_revision INTEGER NOT NULL CHECK (resource_revision >= 0),
    PRIMARY KEY (resource_type, resource_id)
);

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    candidate_id TEXT REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    actor TEXT NOT NULL,
    action TEXT NOT NULL,
    reason TEXT NOT NULL,
    target_release_mbid TEXT,
    job_id TEXT,
    state_revision INTEGER,
    details_json BLOB NOT NULL,
    occurred_at TEXT NOT NULL
);

CREATE TABLE state_transitions (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    actor TEXT NOT NULL,
    reason TEXT NOT NULL,
    previous_state TEXT NOT NULL,
    new_state TEXT NOT NULL,
    previous_revision INTEGER NOT NULL,
    revision INTEGER NOT NULL,
    occurred_at TEXT NOT NULL,
    UNIQUE (candidate_id, revision)
);

CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events BEGIN SELECT RAISE(ABORT, 'audit events are append-only'); END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events BEGIN SELECT RAISE(ABORT, 'audit events are append-only'); END;
CREATE TRIGGER state_transitions_no_update BEFORE UPDATE ON state_transitions BEGIN SELECT RAISE(ABORT, 'state transitions are append-only'); END;
CREATE TRIGGER state_transitions_no_delete BEFORE DELETE ON state_transitions BEGIN SELECT RAISE(ABORT, 'state transitions are append-only'); END;
CREATE TRIGGER metadata_snapshots_no_update BEFORE UPDATE ON metadata_snapshots BEGIN SELECT RAISE(ABORT, 'metadata snapshots are immutable'); END;
CREATE TRIGGER metadata_snapshots_no_delete BEFORE DELETE ON metadata_snapshots BEGIN SELECT RAISE(ABORT, 'metadata snapshots are immutable'); END;
CREATE TRIGGER validation_results_no_update BEFORE UPDATE ON validation_results BEGIN SELECT RAISE(ABORT, 'validation evidence is immutable'); END;
CREATE TRIGGER validation_results_no_delete BEFORE DELETE ON validation_results BEGIN SELECT RAISE(ABORT, 'validation evidence is immutable'); END;

CREATE INDEX candidates_state_updated ON candidates(state, updated_at);
CREATE INDEX validation_candidate_created ON validation_results(candidate_id, created_at);
CREATE INDEX audit_occurred ON audit_events(occurred_at, id);
CREATE INDEX sessions_user_active ON sessions(user_id, revoked_at, expires_at);
