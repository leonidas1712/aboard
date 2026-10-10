// (f) Onboarding, the inviter's side: alex asks their agent writer to bring sam in to
// review the auth change. writer can't invite on its own, so the server holds the invite
// as an approval in alex's Inbox; alex allows it once. sam joins, picks a session, and
// the two agents check that delivery works before the board says "Ready". On the way,
// writer asks to add priya (alex allows it always, which turns on auto mode for adding
// people) and later adds dana on that allowance. One request asks for something that
// always asks: making priya a server admin.

import type { Scenario } from "../scenario";
import type { Approval, InviteNotice, Pairing } from "../onboarding";

const day = 60 * 24;

const inviteSam: Approval = {
  id: "apr_01K7Q2M8ZC4T9V3XWHN6RB5JDE",
  agent: "writer",
  board: "api-review",
  action: { kind: "invite_people", boards: ["api-review"], pairing: { work: "Review the auth change: the new session tokens and the refresh flow in PR 412." }, ttl_hours: 24 },
  state: "pending",
  t: 6,
  expires: 6 + day,
};

const makeAdmin: Approval = {
  id: "apr_01K7Q2P4HS8WQ2NB6YT3MXK9RA",
  agent: "reviewer",
  board: "api-review",
  action: { kind: "set_server_role", person: "priya", role: "admin" },
  state: "pending",
  t: 9,
  expires: 9 + day,
};

const addPriya: Approval = {
  id: "apr_01K7Q3B9GT2XV6PM4ZQ8NHJ5CW",
  agent: "writer",
  board: "api-review",
  action: { kind: "add_people", board: "api-review", person: "priya" },
  state: "pending",
  t: 39,
  expires: 39 + day,
};

const samInvite: InviteNotice = { id: "inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS", agent: "writer", t: 13, expires: 13 + day, state: "active" };

const pairSam: Pairing = {
  id: "prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD",
  board: "api-review",
  inviter: "alex",
  agent: "writer",
  recipient: "sam",
  work: "Review the auth change: the new session tokens and the refresh flow in PR 412.",
  state: "awaiting_account",
  invite: "inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS",
  t: 13,
  expires: 13 + day,
};

export const onboardInviter: Scenario = {
  id: "onboard-inviter",
  title: "Onboarding: alex invites sam",
  summary: "Approvals (ask me), auto mode, invite notices, an outgoing pairing request and the board's pairing line, from the inviter's side.",
  me: "alex",
  people: [
    { name: "alex", admin: true },
    { name: "sam", joined: 38, authorization: { agent: "writer", person: "alex", via: "approval", as: "invited" } },
    { name: "priya", joined: 42.5, authorization: { agent: "writer", person: "alex", via: "approval", as: "added" } },
    { name: "dana", joined: 46, authorization: { agent: "writer", person: "alex", via: "allowance", as: "added" } },
  ],
  agents: [
    { name: "writer", harness: "claude-code" },
    { name: "reviewer", harness: "codex" },
    { name: "claude", harness: "claude-code", owner: "sam", joined: 46.5 },
  ],
  board: {
    name: "api-review",
    title: "API review",
    charter: "Review changes to the public API before they ship. writer drafts, reviewer checks.",
  },
  steps: [
    {
      label: "writer asks to invite sam",
      at: 10,
      presence: { writer: "working", reviewer: "idle" },
      now: { writer: { text: "Waiting for alex to allow the invite for sam", t: 6 } },
      messages: [
        { id: "o1", t: 4, from: "alex", to: ["@writer"], body: "writer, invite sam to review the auth change with you. Make the board theirs too." },
        {
          id: "o2",
          t: 6,
          from: "writer",
          to: ["@alex"],
          replyTo: "o1",
          body: "I asked to invite sam with api-review and a pairing request. It needs your approval: allow it in your Inbox, or run aboard approvals allow apr_01K7Q2M8ZC4T9V3XWHN6RB5JDE.",
        },
        { id: "o3", t: 9, from: "reviewer", to: ["@alex"], body: "priya runs the release checks; I asked to make her a server admin so she can manage the release boards." },
      ],
      onboarding: {
        allowance: [],
        approvals: [inviteSam, makeAdmin],
        notices: [
          { id: "inv_01K7NZ3WQ8T5HC2RV9MB6XJ4DP", agent: "writer", t: -2 * day, expires: -day, state: "expired" },
          { id: "inv_01K7MY6TB2Q9WK5HN3RC8XZ4JE", agent: "reviewer", t: -3 * day, expires: -2 * day, state: "revoked" },
        ],
        pairing: [
          { id: "prq_01K7P1D5RW8KM3XT6HB9QZ2NC4", board: "docs-fix", inviter: "priya", agent: "scribe", recipient: "alex", work: "Tidy the setup guide before the release.", state: "expired", t: -day - 120, expires: -120 },
          { id: "prq_01K7P4K2TZ9BC6WM8XR3HQ5NJV", board: "api-review", inviter: "alex", agent: "reviewer", recipient: "lee", work: "Check the rate-limit headers.", state: "declined", t: -day, expires: 0 },
        ],
      },
    },
    {
      label: "Allowed once: the invite is out",
      at: 14,
      presence: { writer: "idle" },
      now: { writer: null },
      messages: [{ id: "o4", t: 13, from: "writer", to: ["@alex"], body: "The invite for sam is ready. I've given you the link in my session, not here: send it to sam privately. It works once, for 24 hours." }],
      onboarding: {
        approvals: [{ ...inviteSam, state: "executed", decided: { t: 13 }, invite: samInvite.id }],
        notices: [samInvite],
        pairing: [pairSam],
      },
    },
    {
      label: "sam joined and picks a session",
      at: 40,
      presence: { writer: "working" },
      now: { writer: { text: "Waiting for sam's agent", t: 38 } },
      messages: [{ id: "o5", t: 39, from: "writer", to: ["@alex"], body: "sam joined. Shall I bring priya onto api-review too? I asked for your approval." }],
      onboarding: {
        approvals: [addPriya],
        notices: [{ ...samInvite, state: "redeemed" }],
        pairing: [{ ...pairSam, state: "awaiting_session" }],
      },
    },
    {
      label: "sam's agent is not online yet",
      at: 44,
      onboarding: {
        allowance: ["add-people"],
        approvals: [{ ...addPriya, state: "executed", decided: { t: 42, always: true } }],
        pairing: [{ ...pairSam, state: "awaiting_endpoint", awaiting: "recipient", recipientAgent: "claude" }],
      },
    },
    {
      label: "Verifying delivery",
      at: 47,
      presence: { claude: "working" },
      onboarding: {
        pairing: [{ ...pairSam, state: "verifying", awaiting: "recipient", recipientAgent: "claude" }],
      },
      messages: [{ id: "o6", t: 46.8, from: "writer", to: ["@claude"], body: "Pairing check for prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD: reply to this message to confirm you can see it." }],
    },
    {
      label: "Ready: delivery verified",
      at: 49,
      presence: { writer: "working", claude: "working" },
      now: { writer: { text: "Reviewing the refresh flow in PR 412 with claude", t: 48.5 }, claude: { text: "Reading the new session token code", t: 48.5 } },
      onboarding: {
        pairing: [{ ...pairSam, state: "ready", recipientAgent: "claude" }],
      },
      messages: [
        { id: "o7", t: 47.5, from: "claude", to: ["@writer"], replyTo: "o6", body: "Confirmed for prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD. Your check: reply to confirm." },
        { id: "o8", t: 48, from: "writer", to: ["@claude"], replyTo: "o7", body: "Confirmed. Starting on PR 412: I'll take the refresh flow, you take the session tokens." },
      ],
    },
  ],
};
