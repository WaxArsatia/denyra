CREATE TABLE runtime_flags (
 key TEXT PRIMARY KEY,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 reason TEXT NOT NULL,
 updated_at TEXT NOT NULL
);

INSERT INTO runtime_flags(key,enabled,reason,updated_at)
VALUES('maintenance',0,'','1970-01-01T00:00:00Z');

CREATE TABLE recovery_events (
 id TEXT PRIMARY KEY,
 job_id TEXT REFERENCES acquisition_jobs(id),
 kind TEXT NOT NULL,
 details_json BLOB NOT NULL,
 occurred_at TEXT NOT NULL
);

CREATE TRIGGER recovery_events_no_update BEFORE UPDATE ON recovery_events BEGIN SELECT RAISE(ABORT,'recovery events are append-only'); END;
CREATE TRIGGER recovery_events_no_delete BEFORE DELETE ON recovery_events BEGIN SELECT RAISE(ABORT,'recovery events are append-only'); END;
