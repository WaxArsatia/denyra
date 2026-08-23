ALTER TABLE idempotency_records ADD COLUMN request_body BLOB;

CREATE TRIGGER idempotency_request_immutable
BEFORE UPDATE ON idempotency_records
WHEN OLD.key <> NEW.key
  OR OLD.scope <> NEW.scope
  OR OLD.request_hash <> NEW.request_hash
  OR COALESCE(hex(OLD.request_body), '') <> COALESCE(hex(NEW.request_body), '')
  OR OLD.created_at <> NEW.created_at
BEGIN
    SELECT RAISE(ABORT, 'idempotency request is immutable');
END;
