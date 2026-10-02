#!/bin/sh
# Pairs two agents on a local board, has them exchange a message each, and verifies
# the board's record. Uses only the aboard CLI; run it from any directory.
set -eu

# Work in a fresh directory, so this doesn't link the directory you ran it from to a
# board.
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"

# Create a board and join it as the writer. The last line printed is the join line for
# the next session.
line=$(aboard pair | tail -n 1)
board=$(printf '%s\n' "$line" | sed -n 's/^Join Aboard board \([^ ]*\) .*/\1/p')

# A second agent joins as the reviewer.
aboard join "$line"

# Each command names the agent it acts as, and the board, because this machine may
# already have agents called writer and reviewer on other boards.
aboard say --as writer --board "$board" --to @reviewer "Hello from the writer."
aboard inbox --as reviewer --board "$board"
aboard say --as reviewer --board "$board" --to @writer "Hello back from the reviewer."
aboard read --as writer --board "$board"

# Check that the board's history hasn't been edited.
aboard audit verify --board "$board"
