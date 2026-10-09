-- Runtime confirmation survives read-position cleanup. Queue acceptance is separate.
ALTER TABLE deliveries ADD COLUMN confirmed_at TEXT NOT NULL DEFAULT '';
UPDATE deliveries SET confirmed_at = updated_at
WHERE state = 'confirmed' AND handoff_id <> '';
