ALTER TABLE import_intents ADD COLUMN plan_json BLOB;

CREATE TRIGGER import_intent_identity_immutable
BEFORE UPDATE ON import_intents
WHEN OLD.id <> NEW.id
  OR OLD.candidate_id <> NEW.candidate_id
  OR OLD.idempotency_key <> NEW.idempotency_key
  OR OLD.target_release_mbid <> NEW.target_release_mbid
  OR OLD.request_hash <> NEW.request_hash
  OR hex(OLD.release_manifest_json) <> hex(NEW.release_manifest_json)
  OR COALESCE(hex(OLD.plan_json), '') <> COALESCE(hex(NEW.plan_json), '')
  OR COALESCE(OLD.download_id, '') <> COALESCE(NEW.download_id, '')
  OR OLD.created_at <> NEW.created_at
BEGIN
    SELECT RAISE(ABORT, 'import intent identity is immutable');
END;
