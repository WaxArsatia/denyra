CREATE INDEX acquisition_jobs_updated ON acquisition_jobs(updated_at DESC, id DESC);
CREATE INDEX acquisition_jobs_state_updated ON acquisition_jobs(state, updated_at DESC, id DESC);
