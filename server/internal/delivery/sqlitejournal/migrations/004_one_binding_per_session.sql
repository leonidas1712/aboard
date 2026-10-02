-- A session is bound to at most one agent. Journals written before this could hold
-- several bindings for one session; each session keeps only its most recent one, and
-- the others end as if the session had moved to that agent. The ended agents' unread
-- messages stay on their server for the next session that resumes them.

DELETE FROM bindings
WHERE EXISTS (
    SELECT 1 FROM bindings AS newer
    WHERE newer.harness = bindings.harness
      AND newer.session_id = bindings.session_id
      AND (newer.bound_at > bindings.bound_at
           OR (newer.bound_at = bindings.bound_at
               AND (newer.server, newer.board, newer.agent) > (bindings.server, bindings.board, bindings.agent)))
);

CREATE UNIQUE INDEX bindings_one_per_session ON bindings (harness, session_id);
