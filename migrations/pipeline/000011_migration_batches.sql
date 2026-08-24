CREATE TABLE migration_batches (
    id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    actor TEXT NOT NULL,
    selection_json BLOB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('RUNNING','COMPLETED')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE migration_items (
    id TEXT PRIMARY KEY,
    batch_id TEXT NOT NULL REFERENCES migration_batches(id) ON DELETE RESTRICT,
    unmanaged_candidate_id TEXT NOT NULL REFERENCES unmanaged_releases(candidate_id) ON DELETE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('CHECK_PENDING','CHECKING','NO_MATCH','AMBIGUOUS','EXACT_MATCH','CONFIRMED','LIDARR_CATALOG_READY','IMPORT_SUBMITTED','RECONCILING','MIGRATED','FAILED_RETRYABLE')),
    state_revision INTEGER NOT NULL CHECK (state_revision >= 0),
    resume_state TEXT,
    approved_release_mbid TEXT,
    request_evidence_json BLOB,
    response_evidence_json BLOB,
    migration_evidence_json BLOB,
    idempotency_key TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (batch_id, unmanaged_candidate_id)
);

CREATE TABLE migration_item_errors (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES migration_items(id) ON DELETE RESTRICT,
    state TEXT NOT NULL,
    error_text TEXT NOT NULL,
    evidence_json BLOB,
    occurred_at TEXT NOT NULL
);

CREATE INDEX migration_batches_status_updated ON migration_batches(status, updated_at);
CREATE INDEX migration_items_batch_state ON migration_items(batch_id, state, updated_at);
CREATE INDEX migration_items_ready ON migration_items(state, updated_at);
CREATE INDEX migration_item_errors_item_time ON migration_item_errors(item_id, occurred_at);

CREATE TRIGGER migration_item_identity_immutable BEFORE UPDATE OF batch_id, unmanaged_candidate_id, idempotency_key ON migration_items
BEGIN SELECT RAISE(ABORT, 'migration item identity is immutable'); END;
CREATE TRIGGER migration_item_request_evidence_immutable BEFORE UPDATE OF request_evidence_json ON migration_items
WHEN OLD.request_evidence_json IS NOT NULL AND NEW.request_evidence_json <> OLD.request_evidence_json
BEGIN SELECT RAISE(ABORT, 'migration request evidence is immutable'); END;
CREATE TRIGGER migration_item_response_evidence_immutable BEFORE UPDATE OF response_evidence_json ON migration_items
WHEN OLD.response_evidence_json IS NOT NULL AND NEW.response_evidence_json <> OLD.response_evidence_json
BEGIN SELECT RAISE(ABORT, 'migration response evidence is immutable'); END;
CREATE TRIGGER migration_item_errors_no_update BEFORE UPDATE ON migration_item_errors
BEGIN SELECT RAISE(ABORT, 'migration item errors are append-only'); END;
CREATE TRIGGER migration_item_errors_no_delete BEFORE DELETE ON migration_item_errors
BEGIN SELECT RAISE(ABORT, 'migration item errors are append-only'); END;
