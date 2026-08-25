ALTER TABLE migration_batches ADD COLUMN state_revision INTEGER NOT NULL DEFAULT 0 CHECK (state_revision >= 0);
