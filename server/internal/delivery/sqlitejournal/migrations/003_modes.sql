-- Each agent's delivery mode: auto, humans or off. An agent without a row is auto.

CREATE TABLE modes (
    server     TEXT NOT NULL,
    board      TEXT NOT NULL,
    agent      TEXT NOT NULL,
    mode       TEXT NOT NULL,
    PRIMARY KEY (server, board, agent)
);
