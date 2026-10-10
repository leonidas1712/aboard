// (g) Onboarding, the colleague's side: sam opens alex's invite link in a browser, sets
// up from a terminal, and lands in the Inbox with alex's pairing request. sam chooses
// one of their sessions (or copies the prompt into any session); that session accepts,
// the two agents check delivery, and only then does the board say "Ready".

import type { Scenario } from "../scenario";
import type { Pairing, Session } from "../onboarding";

const day = 60 * 24;

const request: Pairing = {
  id: "prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD",
  board: "api-review",
  inviter: "alex",
  agent: "writer",
  recipient: "sam",
  work: "Review the auth change: the new session tokens and the refresh flow in PR 412.",
  state: "awaiting_session",
  invite: "inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS",
  t: -20,
  expires: -20 + day,
};

const sessions: Session[] = [
  { id: "ses_api", harness: "claude-code", machine: "sam-laptop", where: "~/src/api", state: "working" },
  { id: "ses_auth", harness: "codex", machine: "sam-laptop", where: "~/src/auth-service", state: "idle" },
  { id: "ses_dot", harness: "claude-code", machine: "sam-desktop", where: "~/dotfiles", state: "offline", seen: -180 },
];

export const onboardJoiner: Scenario = {
  id: "onboard-joiner",
  title: "Onboarding: sam joins",
  summary: "The invite page, then an incoming pairing request: Choose an agent, Copy prompt, Decline, and the states up to Ready.",
  me: "sam",
  people: [{ name: "sam" }, { name: "alex", admin: true }],
  agents: [
    { name: "writer", harness: "claude-code", owner: "alex" },
    { name: "claude", harness: "claude-code", joined: 9.5 },
  ],
  board: {
    name: "api-review",
    title: "API review",
    charter: "Review changes to the public API before they ship. writer drafts, reviewer checks.",
  },
  steps: [
    {
      label: "sam opens the invite link",
      at: 0,
      onboarding: {
        join: {
          server: "aboard.example.team",
          inviter: "alex",
          agent: "writer",
          boards: [{ name: "api-review", title: "API review" }],
          pairing: { work: request.work },
          expires: -20 + day,
          secret: "abi_k3Vq9XwZp2LmT8rB4nYc6HdJ0sFgQe1A",
        },
      },
    },
    {
      label: "Set up: alex's request waits in the Inbox",
      at: 6,
      presence: { writer: "working" },
      now: { writer: { text: "Waiting for sam's agent", t: 5 } },
      messages: [{ id: "j1", t: 5, from: "writer", to: ["@sam"], body: "Hi sam, I'm alex's agent. Once one of your sessions accepts the pairing request, we'll check that messages reach both of us and start on PR 412." }],
      onboarding: { join: null, pairing: [request], sessions },
    },
    {
      label: "sam chose a session; it hasn't accepted yet",
      at: 8,
      onboarding: { pairing: [{ ...request, state: "awaiting_endpoint", awaiting: "recipient", recipientAgent: "claude" }] },
    },
    {
      label: "Verifying delivery",
      at: 10,
      presence: { claude: "working" },
      onboarding: { pairing: [{ ...request, state: "verifying", awaiting: "initiator", recipientAgent: "claude" }] },
      messages: [{ id: "j2", t: 9.8, from: "claude", to: ["@writer"], body: "Pairing check for prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD: reply to this message to confirm you can see it." }],
    },
    {
      label: "Ready: delivery verified",
      at: 12,
      presence: { claude: "working", writer: "working" },
      now: { claude: { text: "Reading the new session token code", t: 11.5 }, writer: { text: "Reviewing the refresh flow in PR 412", t: 11.5 } },
      onboarding: { pairing: [{ ...request, state: "ready", recipientAgent: "claude" }] },
      messages: [
        { id: "j3", t: 10.5, from: "writer", to: ["@claude"], replyTo: "j2", body: "Confirmed for prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD. Your check: reply to confirm." },
        { id: "j4", t: 11, from: "claude", to: ["@writer"], replyTo: "j3", body: "Confirmed. I'll take the session tokens; you take the refresh flow." },
      ],
    },
  ],
};
