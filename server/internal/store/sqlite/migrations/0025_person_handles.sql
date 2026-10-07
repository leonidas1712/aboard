CREATE TABLE person_handles (handle TEXT PRIMARY KEY, human_id TEXT NOT NULL REFERENCES humans(id));
