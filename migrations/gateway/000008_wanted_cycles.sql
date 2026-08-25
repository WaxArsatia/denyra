CREATE TABLE acquisition_wanted_cycles (
 job_id TEXT PRIMARY KEY REFERENCES acquisition_jobs(id),
 lidarr_album_id INTEGER NOT NULL CHECK(lidarr_album_id>0),
 release_group_mbid TEXT NOT NULL,
 opened_at TEXT NOT NULL,
 closed_at TEXT
);
CREATE UNIQUE INDEX acquisition_wanted_cycles_open_dedup
 ON acquisition_wanted_cycles(lidarr_album_id,release_group_mbid)
 WHERE closed_at IS NULL;
CREATE INDEX acquisition_wanted_cycles_open_job
 ON acquisition_wanted_cycles(job_id)
 WHERE closed_at IS NULL;
CREATE TRIGGER acquisition_wanted_cycles_identity_immutable
 BEFORE UPDATE ON acquisition_wanted_cycles
 WHEN OLD.job_id<>NEW.job_id
   OR OLD.lidarr_album_id<>NEW.lidarr_album_id
   OR OLD.release_group_mbid<>NEW.release_group_mbid
   OR OLD.opened_at<>NEW.opened_at
 BEGIN SELECT RAISE(ABORT,'wanted cycle identity is immutable'); END;
