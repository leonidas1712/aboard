-- Each member's reactions to messages, kept as reaction.added and reaction.removed
-- events are written: a read model that follows from those events in the log. A member
-- reacts to a message with each emoji at most once.
CREATE TABLE reactions (
    message_id TEXT NOT NULL REFERENCES messages (id),
    member_id  TEXT NOT NULL REFERENCES members (id),
    name       TEXT NOT NULL,
    at         TEXT NOT NULL,
    PRIMARY KEY (message_id, member_id, name)
);
