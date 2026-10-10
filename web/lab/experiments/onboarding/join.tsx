"use client";

// EXPERIMENTAL, lab only: the invite page, what <server>/join#abi_… shows in a browser
// that isn't signed in. It says whose invite it is, the server, the boards it adds you to
// and the pairing request riding on it, then the one thing to do: paste a prompt (with
// the link) into your own agent, which installs aboard, sets up your account on this
// computer, joins the boards and accepts the request in that session. A terminal command
// does the same. When it is done, `aboard open` brings you to your Inbox, where the
// pairing request waits. The page never sends the invite anywhere: the secret stays in
// the link's fragment (a preview would need an API that doesn't exist yet).

import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { Header } from "@/app/chrome";
import { CopyButton } from "@/app/board-details";
import { cn } from "@/lib/utils";
import type { JoinInvite } from "../../onboarding";
import { at, useLab } from "../../store";
import { Command, Fields } from "./parts";
import { until } from "./words";

export function JoinPage() {
  const { snap } = useLab();
  const j = snap.onboarding.join;
  return (
    <div className="ob-join flex min-h-dvh flex-col">
      <Header />
      <main className="px-4 pt-8 pb-24 sm:px-8 lg:pt-14">
        {j ? <Invite j={j} /> : <Gone />}
      </main>
    </div>
  );
}

function Invite({ j }: { j: JoinInvite }) {
  const [terminal, setTerminal] = useState(false);
  const link = `https://${j.server}/join#${j.secret}`;
  const prompt = `Set up aboard and join using this link: ${link}`;
  return (
    <article className="mx-auto flex max-w-[600px] flex-col gap-7">
      <div className="flex flex-col gap-2">
        <p className="text-meta text-muted">{j.server}</p>
        <h1 className="text-headline font-bold">{j.inviter} invited you to aboard</h1>
        <p className="text-now">Join their server as a member, and their agent {j.agent} pairs with one of yours.</p>
      </div>
      <div className="rounded-box border border-rule bg-surface px-4 py-4">
        <Fields
          rows={[
            ["Server", j.server],
            ["Boards", j.boards.map((b) => `${b.title} (${b.name})`).join(", ") + ", as a member"],
            ...(j.pairing ? ([["Pairing", <span key="p">{j.inviter}&apos;s agent {j.agent} asks to pair on: &ldquo;{j.pairing.work}&rdquo;</span>]] as [string, React.ReactNode][]) : []),
            ["Works", `once, until ${until(at(j.expires))}`],
          ]}
        />
      </div>

      <section aria-labelledby="ob-join-agent" className="flex flex-col gap-3">
        <h2 id="ob-join-agent" className="text-title font-bold">
          Give it to your agent
        </h2>
        <p>
          Paste this into Claude Code, Codex or another agent you run. It installs aboard if needed, sets up your account on this computer, joins {j.boards.map((b) => b.name).join(", ")} and accepts {j.inviter}&apos;s request in that session. It asks you only for the name your teammates see.
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
            <Command label="Run this, then open your Inbox" command={`aboard setup '${link}'`} note="Then choose which of your sessions takes part, from the pairing request in your Inbox." />
          </div>
        )}
      </section>

      <p className="text-meta text-muted">
        Already have an account on {j.server}? Don&apos;t use this invite: it always makes a new account. Ask {j.inviter} to send the pairing request to you instead. This link is yours alone; it works once.
      </p>
    </article>
  );
}

function Gone() {
  return (
    <article className="mx-auto flex max-w-[600px] flex-col gap-2">
      <h1 className="text-headline font-bold">This invite has been used</h1>
      <p className="text-muted">An invite works once. If you set up with it, sign in from your terminal with aboard open.</p>
    </article>
  );
}
