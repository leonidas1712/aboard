-- Each agent's delivery mode as its person set it, a read model of the board's
-- agent.delivery_changed events: the mode, and the seq of the event that set it. Null and
-- 0 for an agent whose mode was never set, which is focused. Not the same as `delivery`,
-- the mode the agent's delivery daemon last reported applying.
ALTER TABLE members ADD COLUMN delivery_setting TEXT CHECK (delivery_setting IN ('focused', 'all', 'humans', 'off'));
ALTER TABLE members ADD COLUMN delivery_setting_seq INTEGER NOT NULL DEFAULT 0;
