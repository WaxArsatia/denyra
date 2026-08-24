CREATE TABLE slskd_completion_events (
 id TEXT PRIMARY KEY,
 event_version INTEGER NOT NULL,
 transfer_id TEXT NOT NULL,
 batch_id TEXT,
 local_filename TEXT NOT NULL,
 remote_filename TEXT NOT NULL,
 transfer_state TEXT NOT NULL,
 event_timestamp TEXT NOT NULL,
 payload_json BLOB NOT NULL,
 payload_sha256 TEXT NOT NULL CHECK(length(payload_sha256)=64),
 received_at TEXT NOT NULL
);

CREATE TRIGGER slskd_completion_events_no_update BEFORE UPDATE ON slskd_completion_events BEGIN SELECT RAISE(ABORT,'slskd completion events are append-only'); END;
CREATE TRIGGER slskd_completion_events_no_delete BEFORE DELETE ON slskd_completion_events BEGIN SELECT RAISE(ABORT,'slskd completion events are append-only'); END;
