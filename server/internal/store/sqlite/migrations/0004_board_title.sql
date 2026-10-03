-- A board's title: free text people read beside its name, which stays its address.
-- Null when the board has none.
ALTER TABLE boards ADD COLUMN title TEXT;
