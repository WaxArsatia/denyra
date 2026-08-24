ALTER TABLE submissions ADD COLUMN ingress TEXT NOT NULL DEFAULT 'sftp' CHECK (ingress IN ('sftp','browser'));
ALTER TABLE submissions ADD COLUMN provenance_json BLOB NOT NULL DEFAULT '{}';
ALTER TABLE submissions ADD COLUMN preview_fingerprint TEXT;
ALTER TABLE submissions ADD COLUMN decision_json BLOB;

CREATE TABLE upload_sessions (
    id TEXT PRIMARY KEY,
    submission_id TEXT NOT NULL UNIQUE,
    actor TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('OPEN','FINALIZED','DELETED')),
    state_revision INTEGER NOT NULL DEFAULT 0 CHECK (state_revision >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE upload_entries (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES upload_sessions(id) ON DELETE RESTRICT,
    relative_path TEXT NOT NULL,
    normalized_path TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    status TEXT NOT NULL CHECK (status IN ('PENDING','COMPLETE')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (session_id, normalized_path)
);

CREATE TABLE submission_previews (
    submission_id TEXT PRIMARY KEY REFERENCES submissions(id) ON DELETE RESTRICT,
    tree_fingerprint TEXT NOT NULL,
    preview_json BLOB NOT NULL,
    decision_json BLOB,
    updated_at TEXT NOT NULL
);

CREATE INDEX upload_sessions_actor_status_updated ON upload_sessions(actor, status, updated_at);
CREATE INDEX upload_entries_session_status ON upload_entries(session_id, status);
