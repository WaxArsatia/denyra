CREATE TABLE acquisition_jobs (
 id TEXT PRIMARY KEY, lidarr_album_id INTEGER NOT NULL CHECK(lidarr_album_id>0), release_group_mbid TEXT NOT NULL,
 selected_release_mbid TEXT, selected_release_revision INTEGER NOT NULL DEFAULT 0, config_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id),
 state TEXT NOT NULL, state_revision INTEGER NOT NULL DEFAULT 0, primary_attempt INTEGER NOT NULL DEFAULT 0, fallback_attempt INTEGER NOT NULL DEFAULT 0,
 next_retry_at TEXT, queue_watermark TEXT, history_watermark TEXT, command_id TEXT, correlation_started_at TEXT,
 command_deadline TEXT, grace_deadline TEXT, overall_deadline TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX acquisition_jobs_active_dedup ON acquisition_jobs(lidarr_album_id,release_group_mbid) WHERE state NOT IN ('HANDED_OFF','CANCELLED');
CREATE INDEX acquisition_jobs_ready ON acquisition_jobs(state,next_retry_at,updated_at);
CREATE TRIGGER acquisition_jobs_identity_immutable BEFORE UPDATE ON acquisition_jobs WHEN OLD.id<>NEW.id OR OLD.lidarr_album_id<>NEW.lidarr_album_id OR OLD.release_group_mbid<>NEW.release_group_mbid OR OLD.config_snapshot_id<>NEW.config_snapshot_id OR OLD.created_at<>NEW.created_at BEGIN SELECT RAISE(ABORT,'job identity is immutable'); END;

CREATE TABLE attempts(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),kind TEXT NOT NULL,number INTEGER NOT NULL,started_at TEXT NOT NULL,completed_at TEXT,outcome TEXT,error_class TEXT,details_json BLOB NOT NULL,UNIQUE(job_id,kind,number));
CREATE TABLE provider_results(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),attempt_id TEXT NOT NULL REFERENCES attempts(id),provider TEXT NOT NULL,outcome TEXT NOT NULL,evidence_json BLOB NOT NULL,evidence_sha256 TEXT NOT NULL,started_at TEXT NOT NULL,established_at TEXT,completed_at TEXT,UNIQUE(attempt_id,provider));
CREATE TRIGGER provider_results_no_update BEFORE UPDATE ON provider_results BEGIN SELECT RAISE(ABORT,'provider results are immutable'); END;
CREATE TRIGGER provider_results_no_delete BEFORE DELETE ON provider_results BEGIN SELECT RAISE(ABORT,'provider results are immutable'); END;

CREATE TABLE external_effects(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),effect_type TEXT NOT NULL,idempotency_key TEXT NOT NULL UNIQUE,request_hash TEXT NOT NULL,request_json BLOB NOT NULL,status TEXT NOT NULL,response_json BLOB,response_hash TEXT,created_at TEXT NOT NULL,acknowledged_at TEXT,UNIQUE(job_id,effect_type,idempotency_key));
CREATE TRIGGER external_effect_request_immutable BEFORE UPDATE ON external_effects WHEN OLD.id<>NEW.id OR OLD.job_id<>NEW.job_id OR OLD.effect_type<>NEW.effect_type OR OLD.idempotency_key<>NEW.idempotency_key OR OLD.request_hash<>NEW.request_hash OR hex(OLD.request_json)<>hex(NEW.request_json) OR OLD.created_at<>NEW.created_at BEGIN SELECT RAISE(ABORT,'effect intent is immutable'); END;

CREATE TABLE correlation_evidence(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),album_id INTEGER NOT NULL,release_group_mbid TEXT NOT NULL,release_mbid TEXT,command_id TEXT,download_id TEXT,source_kind TEXT NOT NULL,source_record_id TEXT NOT NULL,watermark TEXT NOT NULL,observed_at TEXT NOT NULL,evidence_json BLOB NOT NULL,evidence_sha256 TEXT NOT NULL,UNIQUE(job_id,source_kind,source_record_id));
CREATE TRIGGER correlation_evidence_no_update BEFORE UPDATE ON correlation_evidence BEGIN SELECT RAISE(ABORT,'correlation evidence is immutable'); END;

CREATE TABLE candidates(candidate_id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),source TEXT NOT NULL CHECK(source IN ('slskd','spotiflac')),source_locator TEXT NOT NULL,download_id TEXT,completed_at TEXT,provenance_json BLOB NOT NULL,provenance_sha256 TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(job_id,source,source_locator));
CREATE TRIGGER candidates_no_update BEFORE UPDATE ON candidates BEGIN SELECT RAISE(ABORT,'acquisition candidate is immutable'); END;
CREATE TRIGGER candidates_no_delete BEFORE DELETE ON candidates BEGIN SELECT RAISE(ABORT,'acquisition candidate is immutable'); END;
CREATE TABLE candidate_approvals(candidate_id TEXT PRIMARY KEY REFERENCES candidates(candidate_id),approved_at TEXT NOT NULL,quality_json BLOB NOT NULL,quality_sha256 TEXT NOT NULL,completion_at TEXT NOT NULL,source TEXT NOT NULL,created_at TEXT NOT NULL);

CREATE TABLE arbitrations(job_id TEXT PRIMARY KEY REFERENCES acquisition_jobs(id),first_approved_at TEXT NOT NULL,deadline TEXT NOT NULL,winner_candidate_id TEXT REFERENCES candidates(candidate_id),winner_locked_at TEXT,reason TEXT,evidence_json BLOB NOT NULL,state_revision INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE UNIQUE INDEX arbitrations_one_winner ON arbitrations(winner_candidate_id) WHERE winner_candidate_id IS NOT NULL;

CREATE TABLE leases(resource_type TEXT NOT NULL,resource_id TEXT NOT NULL,owner_id TEXT NOT NULL,acquired_at TEXT NOT NULL,expires_at TEXT NOT NULL,config_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id),resource_revision INTEGER NOT NULL,PRIMARY KEY(resource_type,resource_id));
CREATE TABLE idempotency_records(key TEXT PRIMARY KEY,scope TEXT NOT NULL,request_hash TEXT NOT NULL,response_status INTEGER,response_body BLOB,created_at TEXT NOT NULL,completed_at TEXT);
CREATE TABLE state_transitions(id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES acquisition_jobs(id),actor TEXT NOT NULL,reason TEXT NOT NULL,previous_state TEXT NOT NULL,new_state TEXT NOT NULL,previous_revision INTEGER NOT NULL,revision INTEGER NOT NULL,occurred_at TEXT NOT NULL,UNIQUE(job_id,revision));
CREATE TRIGGER state_transitions_no_update BEFORE UPDATE ON state_transitions BEGIN SELECT RAISE(ABORT,'state transitions are append-only'); END;
CREATE TRIGGER state_transitions_no_delete BEFORE DELETE ON state_transitions BEGIN SELECT RAISE(ABORT,'state transitions are append-only'); END;
