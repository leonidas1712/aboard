"use client";

// EXPERIMENTAL, lab only: the Now view, a board's home once it has a team and real work.
// The person's job there is deciding and reviewing, not reading chat, so it leads with
// attention: what needs them (blocking asks first, then agents going ahead unless told),
// what changed since they last looked, and what is stuck or stale. Everything on it is
// a fact counted from the board's record, never prose, and each fact links to its
// evidence (a thread, a task, a file). Each item can be acted on in place: an ask is a
// message to the right agent. The agent-written brief sits above it, labelled as
// such; the timeline is one tab away. Fed by the scenario, not the API.

import { ArrowRight } from "lucide-react";
import type { ReactNode } from "react";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import { answeredByMe } from "../fake-api";
import { type ScenarioMessage, rootOf, taskRef } from "../scenario";
import { openArtifact, openTask, openThread, scenario, showView, useLab } from "../store";
import { Ask } from "./ask";
import { active, ago, briefStaleAfter, minutesSince, staleAfter, useNow } from "./common";

const link = "text-link underline decoration-1 underline-offset-[3px] hover:no-underline";

function Evidence({ onClick, children }: { onClick: () => void; children: ReactNode }) {
  return (
    <button type="button" className={cn("inline-flex min-h-8 items-center gap-1 text-meta", link)} onClick={onClick}>
      {children}
      <ArrowRight className="size-3.5" strokeWidth={1.75} aria-hidden />
    </button>
  );
}

function Section({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex flex-col gap-2">
      <h3 className="flex flex-wrap items-baseline gap-x-2 text-meta font-bold text-muted">
        {title}
        {note && <span className="font-normal">{note}</span>}
      </h3>
      {children}
    </section>
  );
}

/** needsOf is what needs the person: unanswered asks to them, tasks waiting on them, agents going ahead. */
export function needsOf(messages: ScenarioMessage[], tasks: ReturnType<typeof useLab>["snap"]["tasks"], now: number) {
  const me = scenario.me;
  const replied = (m: ScenarioMessage) => answeredByMe(m.id) || messages.some((r) => r.replyTo === m.id && r.from === me);
  const asks = messages.filter((m) => m.asks && m.to?.includes(`@${me}`) && !replied(m));
  const named = new Set(asks.flatMap((m) => m.about ?? []));
  const waiting = tasks.filter((t) => t.state === "waiting" && t.waitingOn === me && !named.has(t.id));
  const ahead = messages.filter((m) => m.ahead && !replied(m) && minutesSince(m.t, now) < 240);
  return { asks, waiting, ahead };
}

export function NowView() {
  const { snap, step } = useLab();
  const now = useNow();
  const { asks, waiting, ahead } = needsOf(snap.messages, snap.tasks, now);
  // The person last looked at the end of the step before this one.
  const looked = step > 0 ? scenario.steps[step - 1].at : -Infinity;
  const since = snap.messages.filter((m) => m.t > looked);
  const done = snap.tasks.filter((t) => t.state === "done" && t.t > looked);
  const files = snap.artifacts.filter((a) => a.t > looked);
  const decisions = since.filter((m) => m.decision);
  const stuck = snap.tasks.filter((t) => (t.state === "waiting" && t.waitingOn !== scenario.me) || (t.state === "open" && minutesSince(t.t, now) > 30));
  const staleLines = Object.entries(snap.now).filter(([, l]) => l && minutesSince(l.t, now) > staleAfter && snap.presence !== undefined);
  const staleFiles = snap.artifacts.filter((a) => a.maintained && minutesSince(a.t, now) > briefStaleAfter);
  const busy = new Set(snap.tasks.filter(active).flatMap((t) => [t.owner, ...(t.with ?? [])]));
  const free = scenario.agents.filter((a) => snap.presence[a.name] === "idle" && !busy.has(a.name));
  const open = snap.tasks.filter((t) => t.state === "open");
  const needs = asks.length + waiting.length;
  return (
    <div className="now-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
      <div className="mx-auto w-full max-w-[848px] px-4 sm:px-6">
      <div className="flex flex-col gap-6 border-t border-rule pt-4 pb-10">
        <Section title="Needs you" note={needs + ahead.length === 0 ? "· nothing right now" : undefined}>
          {(asks.length > 0 || waiting.length > 0) && (
            <ul className="flex flex-col gap-2">
              {asks.map((m) => (
                <li key={m.id} className="need flex flex-col gap-1.5 rounded-box bg-attention px-3.5 py-3 text-ink">
                  <p>
                    <strong>{m.from} asks you</strong>
                    <span className="text-meta"> · {ago(m.t, now)}</span>
                    {(m.about ?? []).map((t) => (
                      <button key={t} type="button" className="ml-2 text-meta font-bold underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openTask(t)}>
                        {taskRef(t)}
                      </button>
                    ))}
                  </p>
                  <p className="line-clamp-3">{m.body}</p>
                  <div className="flex flex-wrap items-center gap-x-4">
                    <button type="button" className="min-h-8 text-meta font-bold underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(m.id)}>
                      Answer in the thread
                    </button>
                  </div>
                </li>
              ))}
              {waiting.map((t) => (
                <li key={t.id} className="need flex flex-col gap-1 rounded-box bg-attention px-3.5 py-3 text-ink">
                  <p>
                    <strong>
                      {taskRef(t.id)} waits on you
                    </strong>
                    : {t.reason ?? t.title}
                  </p>
                  <button type="button" className="min-h-8 self-start text-meta font-bold underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openTask(t.id)}>
                    Open the task
                  </button>
                </li>
              ))}
            </ul>
          )}
          {ahead.length > 0 && (
            <>
              <h4 className="pt-1 text-meta font-bold text-ink">Going ahead unless you say</h4>
              <ul className="flex flex-col gap-2">
                {ahead.map((m) => (
                  <li key={m.id} className="ahead flex flex-col gap-1.5 rounded-box border-2 border-[var(--attention)] bg-surface px-3.5 py-3">
                    <p>
                      <strong>{m.from}</strong>
                      <span className="text-meta text-muted"> · {ago(m.t, now)}</span>
                    </p>
                    <p className="line-clamp-3">{m.body}</p>
                    <div className="flex flex-wrap items-center gap-x-4">
                      <Evidence onClick={() => openThread(m.id)}>Open the thread</Evidence>
                      <Ask label="Hold off" to={m.from} about={m.about} text={`Hold off on this until we've talked: "${m.body.slice(0, 70)}…"`} />
                    </div>
                  </li>
                ))}
              </ul>
            </>
          )}
        </Section>

        <Section title="Since you last looked" note={step > 0 ? `· ${scenario.steps[step - 1].label}` : undefined}>
          <ul className="divide-y divide-rule rounded-box border border-rule bg-surface">
            <Row>
              {count(since.length, "message", "messages")}
              {decisions.length > 0 && <>, {count(decisions.length, "decision", "decisions")}</>}
              <Evidence onClick={() => showView("timeline")}>Read them</Evidence>
            </Row>
            {done.map((t) => (
              <Row key={t.id}>
                <span>
                  <strong className="tabular-nums">{taskRef(t.id)}</strong> done by {t.owner}: {t.title}
                </span>
                <Evidence onClick={() => openTask(t.id)}>Task</Evidence>
              </Row>
            ))}
            {files.map((a) => (
              <Row key={a.id}>
                <span>
                  <strong>{a.name}</strong> {a.version > 1 ? `updated to version ${a.version}` : "added"} by {a.by === scenario.me ? "you" : a.by}
                </span>
                <Evidence onClick={() => openArtifact(a.id)}>Open</Evidence>
              </Row>
            ))}
            {decisions.map((m) => (
              <Row key={m.id}>
                <span>
                  Decided by {m.from}: <span className="line-clamp-1 inline">{m.body}</span>
                </span>
                <Evidence onClick={() => openThread(rootOf(snap.messages, m).id)}>Thread</Evidence>
              </Row>
            ))}
          </ul>
        </Section>

        {(stuck.length > 0 || staleLines.length > 0 || staleFiles.length > 0 || free.length > 0) && (
          <Section title="Stuck, stale or free">
            <ul className="divide-y divide-rule rounded-box border border-rule bg-surface">
              {stuck.map((t) => (
                <Row key={t.id}>
                  <span>
                    <strong className="tabular-nums">{taskRef(t.id)}</strong>{" "}
                    {t.state === "waiting" ? `waits on ${t.waitingOn}` : "is open with no owner"} for {ago(t.t, now).replace(" ago", "")}: {t.title}
                  </span>
                  <Evidence onClick={() => openTask(t.id)}>Task</Evidence>
                </Row>
              ))}
              {staleLines.map(([name, l]) => (
                <Row key={name}>
                  <span>
                    <strong>{name}</strong>&apos;s now line is from {ago(l!.t, now)}: {l!.text}
                  </span>
                  <Ask label="Ask for an update" to={name} text="Your now line looks old. What are you on, and is anything in your way?" />
                </Row>
              ))}
              {staleFiles.map((a) => (
                <Row key={a.id}>
                  <span>
                    <strong>{a.name}</strong>, kept by {a.by}, was last updated {ago(a.t, now)}
                  </span>
                  <Ask label="Ask for a refresh" to={a.by} text={`Please bring ${a.name} up to date with the board, and say what changed.`} />
                </Row>
              ))}
              {free.map((a) => (
                <Row key={a.name}>
                  <span>
                    <strong>{a.name}</strong> is idle and on no task
                  </span>
                  <Ask
                    label="Find it work"
                    to={a.name}
                    about={open.map((t) => t.id)}
                    text={open.length ? `You're free: take one of the open tasks (${open.map((t) => taskRef(t.id)).join(", ")}) and say which.` : "You're free: what would help most next?"}
                  />
                </Row>
              ))}
            </ul>
          </Section>
        )}
      </div>
      </div>
    </div>
  );
}

function Row({ children }: { children: ReactNode }) {
  return <li className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-3.5 py-2">{children}</li>;
}
