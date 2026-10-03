-- Each agent's delivery mode, as its owner's delivery daemon last reported it with its
-- presence, so a sender can tell when a message will reach the agent. Bookkeeping like
-- presence: never an event. Null until a daemon reports it.
ALTER TABLE members ADD COLUMN delivery TEXT CHECK (delivery IN ('auto', 'humans', 'off'));
