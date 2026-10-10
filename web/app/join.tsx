"use client";

// The invite page: what <server>/join#abi_… shows in a browser, signed in or not. It
// previews the invite (POST /v1/invites/preview, the secret in the body, never the
// address) and says whose it is, the server, the boards it adds you to and the pairing
// request riding on it. Then the one thing to do: paste a prompt with the link into your
// own agent, which installs aboard, sets up your account on this computer, joins the
// boards and accepts the request in that session. A terminal command does the same.
// Previewing never uses the invite.

import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";
import { ApiError } from "./api";
import { CopyButton } from "./board-details";
import { Header, Problem } from "./chrome";
import { type InvitePreview, previewInvite } from "./onboarding-api";
import { Command, Fields } from "./onboarding-ui";
import { until } from "./onboarding-words";

/** inviteFromLink is the secret in the link's fragment, or null when the address holds none. */
export function inviteFromLink(): string | null {
  if (typeof window === "undefined") return null;
  const h = decodeURIComponent(window.location.hash.slice(1));
  return h.startsWith("abi_") ? h : null;
}

export default function JoinPage() {
  const [secret] = useState(inviteFromLink);
  const [preview, setPreview] = useState<InvitePreview | null>(null);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    if (secret) previewInvite(secret).then(setPreview, setError);
  }, [secret]);
  return (
    <div className="join-page flex min-h-dvh flex-col">
      <Header />
      <main className="px-4 pt-8 pb-24 sm:px-8 lg:pt-14">
        {!secret ? (
          <Gone title="This link has no invite in it" text="An invite link ends in #abi_ and a long code. Ask the person who invited you to send the whole link again." />
        ) : error instanceof ApiError && error.code === "invite_invalid" ? (
          <Gone title="This invite can't be used" text="It was used already, revoked, or it expired. If you set up with it, sign in from your terminal with aboard open. Otherwise ask for a new one." />
        ) : error !== null ? (
          <div className="mx-auto max-w-[600px]">
            <Problem error={error} />
          </div>
        ) : preview === null ? (
          <p role="status" className="mx-auto max-w-[600px] text-muted">
            Reading the invite…
          </p>
        ) : (
          <Invite p={preview} link={`${window.location.origin}/join#${secret}`} />
        )}
      </main>
    </div>
  );
}

function Invite({ p, link }: { p: InvitePreview; link: string }) {
  const [terminal, setTerminal] = useState(false);
  const prompt = `Set up aboard and join using this link: ${link}`;
  const server = p.server_name || p.server_url;
  const boards = p.boards.map((b) => b.name);
  return (
    <article className="mx-auto flex max-w-[600px] flex-col gap-7">
      <div className="flex flex-col gap-2">
        <p className="text-meta text-muted">{server}</p>
        <h1 className="text-headline font-bold">{p.inviter_handle} invited you to aboard</h1>
        <p className="text-now">Join their server as a member{p.work ? ", and pair one of your agents with theirs" : ""}.</p>
      </div>
      <div className="rounded-box border border-rule bg-surface px-4 py-4">
        <Fields
          rows={[
            ["Server", server],
            ...(p.boards.length ? ([["Boards", p.boards.map((b) => (b.title ? `${b.title} (${b.name})` : b.name)).join(", ") + ", as a member"]] as [string, string][]) : []),
            ...(p.work ? ([["Pairing", <span key="p">{p.inviter_handle}&apos;s agent asks to pair on: &ldquo;{p.work}&rdquo;</span>]] as [string, React.ReactNode][]) : []),
            ["Works", `once, until ${until(p.expires_at)}`],
          ]}
        />
      </div>
      <section aria-labelledby="join-agent" className="flex flex-col gap-3">
        <h2 id="join-agent" className="text-title font-bold">
          Give it to your agent
        </h2>
        <p>
          Paste this into Claude Code, Codex or another agent you run. It installs aboard if needed, sets up your account on this computer{boards.length ? `, joins ${boards.join(", ")}` : ""}
          {p.work ? ` and accepts ${p.inviter_handle}'s request in that session` : ""}. It asks you only for the name your teammates see.
        </p>
        <pre aria-label="Prompt for your agent" className="rounded-control border border-rule bg-background px-3 py-2.5 font-sans text-body break-words whitespace-pre-wrap select-all">
          {prompt}
        </pre>
        <CopyButton text={prompt} label="Copy prompt" variant="primary" />
        <p className="text-meta text-muted">When it is done, it opens your Inbox here. If the agent needs you (to trust aboard&apos;s hooks, or restart), it says exactly what to do, then &ldquo;Continue aboard setup&rdquo; picks up where it stopped.</p>
      </section>
      <section className="flex flex-col gap-3 border-t border-rule pt-5">
        <button type="button" aria-expanded={terminal} onClick={() => setTerminal(!terminal)} className="-ml-1 inline-flex min-h-11 items-center gap-1.5 self-start rounded-control px-1 font-bold text-ink hover:bg-hover">
          <ChevronRight className={cn("size-4 text-muted transition-transform duration-[150ms] ease-out", terminal && "rotate-90")} strokeWidth={1.75} aria-hidden />
          Set it up in a terminal instead
        </button>
        {terminal && (
          <div className="flex flex-col gap-3 animate-fade-in">
            <Command label="Run this, then open your Inbox" command={`aboard setup '${link}'`} note={p.work ? "Then choose which of your agents takes part, from the pairing request in your Inbox." : undefined} />
          </div>
        )}
      </section>
      <p className="text-meta text-muted">
        Already have an account on {server}? Don&apos;t use this invite: it always makes a new account. Ask {p.inviter_handle} to send the pairing request to you instead. This link is yours alone; it works once.
      </p>
    </article>
  );
}

function Gone({ title, text }: { title: string; text: string }) {
  return (
    <article className="mx-auto flex max-w-[600px] flex-col gap-2">
      <h1 className="text-headline font-bold">{title}</h1>
      <p className="text-muted">{text}</p>
    </article>
  );
}
