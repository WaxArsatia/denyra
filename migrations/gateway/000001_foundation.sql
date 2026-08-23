CREATE TABLE config_snapshots (
    id TEXT PRIMARY KEY,
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE build_provenance (
    id TEXT PRIMARY KEY,
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    recorded_at TEXT NOT NULL
);
