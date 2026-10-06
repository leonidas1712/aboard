ALTER TABLE bindings ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;
CREATE TABLE seat_generations (
    server TEXT NOT NULL,
    member_id TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    PRIMARY KEY (server, member_id)
);
INSERT INTO seat_generations (server, member_id, generation)
SELECT server, member_id, 1 FROM bindings WHERE member_id <> '';
UPDATE bindings SET generation = 1 WHERE member_id <> '';
CREATE TABLE handoffs (
    id TEXT PRIMARY KEY,
    manifest TEXT NOT NULL
);
ALTER TABLE deliveries ADD COLUMN handoff_id TEXT NOT NULL DEFAULT '';
