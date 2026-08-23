CREATE TABLE config_snapshots (
    id TEXT PRIMARY KEY,
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE build_provenance (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    canonical_json BLOB NOT NULL,
    sha256 TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);
