ALTER TABLE boards ADD COLUMN lifecycle TEXT NOT NULL DEFAULT 'active' CHECK (lifecycle IN ('active', 'archived', 'deleted'));

-- Old creation responses could retain code secrets after expiry or revocation.
-- Deleting rows does not erase prior backups or SQLite storage remnants.
DELETE FROM idempotency
WHERE status = 201
  AND CASE WHEN json_valid(CAST(body AS TEXT)) THEN
    json_type(CAST(body AS TEXT), '$.code') = 'text'
    AND json_extract(CAST(body AS TEXT), '$.code') <> ''
    AND json_extract(CAST(body AS TEXT), '$.id') IN (
      SELECT id FROM join_codes WHERE id GLOB 'jc_*'
    )
  ELSE 0 END;
