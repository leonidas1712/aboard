# Team admin preview

These are layout proposals, captured before the production UI is built. People,
boards and conversation text are illustrative. Desktop is 1440 pixels wide;
mobile is 390 pixels wide. They use the board view's font, colours and spacing.

## People

The account menu opens a People page when the server has more than one person,
or the caller is a server admin. A table becomes labelled rows on mobile.
Non-admins see no admin actions. Guests cannot be promoted through the role API;
the production page must not offer that action for guests.

The current API supplies handle, display name and server role. Boards in common
and agent counts can be derived only from boards the caller may read. It supplies
no person last-active timestamp. The previews say so rather than inventing one.

Server role changes and removal reject browser credentials. The proposed
confirmation therefore explains the effect and offers a copyable terminal
command. It must never submit a person access key through the browser. The agreed
implementation uses these handoffs and omits the unavailable last-active column.
Global agent counts and person activity need a separate contract decision.

## Visibility

Board details show visibility and an owner-only change action. A confirmation
explains whole-history and file access before making a private board open.
Making it private preserves existing board membership. The server remains the
permission authority. Archived boards cannot be made open.

The existing board view has no board-creation form, so this proposal adds none.
