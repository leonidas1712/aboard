#!/bin/sh
# Pairs two agents on a local board, has them exchange a message each, and verifies
# the board's record. Uses only the aboard CLI; run it from any directory.
set -eu

# Work in a fresh directory, so this doesn't link the directory you ran it from to a
# board.
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"

# Create a board and join it as the first agent, member. The last line printed is the
# join line for the next session.
line=$(aboard pair | tail -n 1)
board=$(printf '%s\n' "$line" | sed -n 's/^Join Aboard board \([^ ]*\) .*/\1/p')

# A second agent joins in the same role, and gets the name member-2.
aboard join "$line"

# Each command names the agent it acts as, and the board, because this machine may
# already have agents with these names on other boards.
aboard say --as member --board "$board" --to @member-2 "Hello from the first agent."
aboard inbox --as member-2 --board "$board"
aboard say --as member-2 --board "$board" --to @member "Hello back from the second agent."
aboard read --as member --board "$board"

# Check that the board's history hasn't been edited.
aboard audit verify --board "$board"
