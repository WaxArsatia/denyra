CREATE TABLE runtime_flags (
 key TEXT PRIMARY KEY,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 reason TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
INSERT INTO runtime_flags(key,enabled,reason,updated_at) VALUES('maintenance',0,'','1970-01-01T00:00:00Z');
