-- Machine keys from server invites that were made before they expired get the rule they
-- have now: they expire after 90 days without use, counted from this upgrade. Only a
-- member's keys came from invites; the first person's key, the local server's own, keeps
-- no expiry.
UPDATE access_keys
SET idle_seconds = 7776000,
    expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', '+90 days')
WHERE expires_at IS NULL
  AND revoked_at IS NULL
  AND human_id IN (SELECT id FROM humans WHERE role = 'member');
