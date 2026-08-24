CREATE TABLE unmanaged_releases (
    candidate_id TEXT PRIMARY KEY REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    approved_plan_json BLOB NOT NULL,
    evidence_json BLOB NOT NULL,
    state TEXT NOT NULL,
    state_revision INTEGER NOT NULL CHECK (state_revision >= 0),
    final_path TEXT,
    manifest_json BLOB NOT NULL DEFAULT '[]',
    fingerprint TEXT,
    status TEXT NOT NULL CHECK (status IN ('PREPARED','IMPORTING','IMPORTED','REVIEW_REQUIRED')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE unmanaged_import_intents (
    id TEXT PRIMARY KEY,
    candidate_id TEXT NOT NULL UNIQUE REFERENCES candidates(candidate_id) ON DELETE RESTRICT,
    idempotency_key TEXT NOT NULL UNIQUE,
    approved_plan_json BLOB NOT NULL,
    evidence_json BLOB NOT NULL,
    final_path TEXT,
    manifest_json BLOB NOT NULL DEFAULT '[]',
    fingerprint TEXT,
    status TEXT NOT NULL CHECK (status IN ('PENDING','MUTATING','LAYOUT_READY','PUBLISHED','VERIFIED','COMPLETED','REVIEW_REQUIRED')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX unmanaged_releases_status_updated ON unmanaged_releases(status, updated_at);
CREATE INDEX unmanaged_import_intents_status_updated ON unmanaged_import_intents(status, updated_at);
