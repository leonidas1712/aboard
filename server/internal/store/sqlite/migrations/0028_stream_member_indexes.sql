CREATE INDEX members_active_human_boards ON members (human_id, board_id)
    WHERE kind = 'human' AND status = 'active';

CREATE INDEX members_in_board_order ON members (board_id);

CREATE INDEX members_agent_owners ON members (board_id, human_id)
    WHERE kind = 'agent';
