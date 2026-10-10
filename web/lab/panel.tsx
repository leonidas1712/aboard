"use client";

// The lab's floating controls: which scenario, which step, and play. Only the lab build
// has them. ?panel=closed starts them folded into one small button.

import { FlaskConical, Pause, Play, X } from "lucide-react";
import { useState } from "react";
import { cn } from "@/lib/utils";
import { scenarios } from "./scenarios";
import { allThemes, setTheme } from "./theme-list";
import { goTo, labHref, play, scenario, useLab } from "./store";

/** marker is on the lab's own element only; `make web-lab-check` makes sure no real build has it. */
export const marker = "aboard-ui-lab";

export function Panel() {
  const { step, playing } = useLab();
  const [open, setOpen] = useState(() => new URLSearchParams(window.location.search).get("panel") !== "closed");
  const steps = scenario.steps;
  const box = "fixed bottom-3 left-3 z-50 border border-field-border bg-surface text-ink";
  if (!open) {
    return (
      <button
        type="button"
        data-lab={marker}
        className={cn(box, "inline-flex min-h-11 items-center gap-2 rounded-control px-3 text-meta font-bold max-sm:right-3 max-sm:bottom-28 max-sm:left-auto")}
        onClick={() => setOpen(true)}
        aria-label="Show the UI lab's controls"
      >
        <FlaskConical className="size-4" strokeWidth={1.75} aria-hidden />
        Lab · {step + 1}/{steps.length}
      </button>
    );
  }
  return (
    <section data-lab={marker} aria-label="UI lab" className={cn(box, "flex w-[340px] max-w-[calc(100vw-1.5rem)] flex-col gap-2 rounded-box px-3.5 py-3")}>
      <div className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-1.5 text-meta font-bold">
          <FlaskConical className="size-4" strokeWidth={1.75} aria-hidden />
          UI lab <span className="font-normal text-muted">· fake data, experimental views</span>
        </h2>
        <button
          type="button"
          className="-mr-2 inline-flex size-9 items-center justify-center rounded-control text-muted hover:bg-selected hover:text-ink"
          aria-label="Fold the lab's controls"
          onClick={() => setOpen(false)}
        >
          <X className="size-4" strokeWidth={1.75} aria-hidden />
        </button>
      </div>
      <label className="flex flex-col gap-1 text-meta">
        <span className="text-muted">Scenario</span>
        <select
          className="min-h-10 rounded-control border border-field-border bg-surface px-2 text-body text-ink"
          value={scenario.id}
          onChange={(e) => {
            const s = scenarios.find((x) => x.id === e.target.value)!;
            window.location.href = labHref({ lab: s.id, step: null, board: null, inbox: null, list: null, view: null, artifact: null, play: null });
          }}
        >
          {scenarios.map((s) => (
            <option key={s.id} value={s.id}>
              {s.title}
            </option>
          ))}
        </select>
      </label>
      <label className="flex items-center gap-2 text-meta">
        <span className="text-muted">Theme</span>
        <select
          className="min-h-9 flex-1 rounded-control border border-field-border bg-surface px-2 text-ink"
          defaultValue={(() => {
            try {
              return JSON.parse(localStorage.getItem("aboard.theme") ?? '"system"');
            } catch {
              return "system";
            }
          })()}
          onChange={(e) => setTheme(e.target.value)}
        >
          {allThemes.map((th) => (
            <option key={th.id} value={th.id}>
              {th.label}
            </option>
          ))}
        </select>
      </label>
      <p className="text-meta text-muted">{scenario.summary}</p>
      {steps.length > 1 && (
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-1" role="group" aria-label="Time steps">
            {steps.map((s, i) => (
              <button
                key={i}
                type="button"
                aria-pressed={i === step}
                title={s.label}
                onClick={() => {
                  play(false);
                  goTo(i);
                }}
                className={cn(
                  "inline-flex size-9 items-center justify-center rounded-control text-meta tabular-nums transition-colors duration-[140ms] ease-out",
                  i === step ? "bg-ink font-bold text-on-ink" : "hover:bg-selected",
                )}
              >
                {i + 1}
              </button>
            ))}
            <button
              type="button"
              onClick={() => play(!playing)}
              className="ml-auto inline-flex min-h-9 items-center gap-1.5 rounded-control px-2.5 text-meta hover:bg-selected"
              aria-label={playing ? "Pause" : "Play through the steps"}
            >
              {playing ? <Pause className="size-4" strokeWidth={1.75} aria-hidden /> : <Play className="size-4" strokeWidth={1.75} aria-hidden />}
              {playing ? "Pause" : "Play"}
            </button>
          </div>
          <p className="text-meta" aria-live="polite">
            <span className="font-bold">Step {step + 1}:</span> {steps[step].label}
          </p>
        </div>
      )}
      <p className="flex flex-wrap gap-x-3 text-meta">
        <a href={labHref({ inbox: "1", board: null, list: null, view: null, task: null, artifact: null })}>Inbox</a>
        <a href={labHref({ board: scenario.board.name, inbox: null, list: null, view: null, task: null, artifact: null })}>Board</a>
        <a href={labHref({ board: scenario.board.name, inbox: null, list: null, view: "tasks", task: null, artifact: null })}>Tasks</a>
        <a href={labHref({ list: "1", board: null, inbox: null, view: null, task: null, artifact: null })}>Board list</a>
        {scenario.id.startsWith("onboard") && (
          <>
            <a href={labHref({ view: "settings", board: null, inbox: null, list: null, join: null, item: null })}>Settings</a>
            {/* The invite page is the colleague's first moment, in the joiner scenario; its link carries the invite. */}
            <a href={`${window.location.pathname}?lab=onboard-joiner&step=1&join=1#${scenarios.find((s) => s.id === "onboard-joiner")?.steps[0].onboarding?.join?.secret ?? ""}`}>Invite page (opened in a browser)</a>
          </>
        )}
      </p>
    </section>
  );
}
