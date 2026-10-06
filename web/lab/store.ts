// The lab's own state: which scenario and step is on screen, and the lab clock. It lives
// in the address (?lab=<scenario>&step=<n>), so a reload, a link or a screenshot comes
// back to the same moment.
//
// The clock: when the page loads, the chosen step is "now". Moving forward to a later
// step moves Date.now() forward by the time between the steps, so every relative time on
// the page (the real UI's included) reads as it would at that step. Moving back reloads.

import { useSyncExternalStore } from "react";
import { type Scenario, type Snapshot, snapshot } from "./scenario";
import { scenarios } from "./scenarios";

const realNow = Date.now.bind(Date);
let offset = 0;

/** startClock makes Date.now() follow the lab clock. */
export function startClock() {
  Date.now = () => realNow() + offset;
}

function params(): URLSearchParams {
  return new URLSearchParams(typeof window === "undefined" ? "" : window.location.search);
}

function pickScenario(): Scenario {
  const id = params().get("lab");
  return scenarios.find((s) => s.id === id) ?? scenarios[0];
}

function pickStep(s: Scenario): number {
  const n = Number(params().get("step") ?? s.steps.length);
  return Math.min(s.steps.length, Math.max(1, Number.isFinite(n) ? Math.round(n) : 1)) - 1;
}

export const scenario = pickScenario();
const first = pickStep(scenario);
/** base is the real time, in ms, of the scenario's minute 0. */
export const base = realNow() - scenario.steps[first].at * 60_000;

/** at turns scenario minutes into a time in ms on the lab clock. */
export function at(t: number): number {
  return base + t * 60_000;
}

type State = { step: number; snap: Snapshot; playing: boolean };
let state: State = { step: first, snap: snapshot(scenario, first), playing: params().get("play") === "1" };
const listeners = new Set<() => void>();
const stepListeners = new Set<(s: Snapshot) => void>();

function set(next: Partial<State>) {
  state = { ...state, ...next };
  for (const l of listeners) l();
}

/** useLab is the lab's state, for React. */
export function useLab(): State {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => state,
    () => state,
  );
}

export function current(): State {
  return state;
}

/** onStep runs fn each time the lab moves to a later step. */
export function onStep(fn: (s: Snapshot) => void) {
  stepListeners.add(fn);
}

/** labHref is the address of a lab page: the current scenario and step unless changed. */
export function labHref(changes: Record<string, string | null>): string {
  const p = params();
  p.set("lab", scenario.id);
  p.set("step", String(state.step + 1));
  for (const [k, v] of Object.entries(changes)) {
    if (v === null) p.delete(k);
    else p.set(k, v);
  }
  return `${window.location.pathname}?${p.toString()}`;
}

/** goTo shows step k (0-based): live when it is later, by reloading when it is earlier. */
export function goTo(k: number) {
  if (k === state.step) return;
  if (k < state.step) {
    window.location.href = labHref({ step: String(k + 1), play: null });
    return;
  }
  offset += (scenario.steps[k].at - scenario.steps[state.step].at) * 60_000;
  const snap = snapshot(scenario, k);
  set({ step: k, snap });
  history.replaceState(history.state, "", labHref({ play: null }));
  for (const l of stepListeners) l(snap);
}

// What is on screen beside the record: the board's view (Conversation or Tasks), what
// the side panel shows (the board's Work, a task or a file), a thread to bring into
// view, and the asks the person answered here. ?view=tasks, ?task=ID and ?artifact=ID
// open the page there.

export type View = "conversation" | "tasks" | "files";
export type Panel = { kind: "work" } | { kind: "task"; id: string } | { kind: "artifact"; id: string; from: string | null };
/** filter is a task the conversation is narrowed to, or null. */
type Ui = { view: View; panel: Panel; thread: string | null; full: boolean; answered: Record<string, string>; filter: string | null; n: number };

function firstPanel(): Panel {
  const task = params().get("task");
  const artifact = params().get("artifact");
  if (artifact) return { kind: "artifact", id: artifact, from: task };
  if (task) return { kind: "task", id: task };
  return { kind: "work" };
}

let ui: Ui = {
  view: params().get("view") === "tasks" ? "tasks" : params().get("view") === "files" ? "files" : "conversation",
  panel: firstPanel(),
  thread: null,
  full: false,
  answered: {},
  filter: params().get("filter"),
  n: 0,
};
const uiListeners = new Set<() => void>();

function setUi(next: Partial<Ui>) {
  ui = { ...ui, ...next, n: ui.n + 1 };
  for (const l of uiListeners) l();
}

/** useUi is what is on screen beside the record. */
export function useUi(): Ui {
  return useSyncExternalStore(
    (l) => {
      uiListeners.add(l);
      return () => uiListeners.delete(l);
    },
    () => ui,
    () => ui,
  );
}

export const showView = (view: View) => setUi({ view });
/** openTask opens a task in the side panel. */
export const openTask = (id: string) => setUi({ panel: { kind: "task", id } });
/** openArtifact opens a file in the side panel; from is the task to go back to. */
export const openArtifact = (id: string, from: string | null = null) => setUi({ panel: { kind: "artifact", id, from }, full: false });
/** showWork brings the side panel back to the board's work. */
export const showWork = () => setUi({ panel: { kind: "work" } });
/** fullScreen shows the open file over the whole page, or stops. */
export const fullScreen = (on: boolean) => setUi({ full: on });
/** openThread shows a thread in the conversation, by its first message. */
export const openThread = (root: string) => setUi({ view: "conversation", thread: root });
/** filterTo narrows the conversation to one task's threads and messages, or stops with null. */
export const filterTo = (task: string | null) => setUi({ filter: task, view: "conversation" });
/** answered records the person's answer to an ask, so it leaves the Inbox. */
export const markAnswered = (ask: string, answer: string) => setUi({ answered: { ...ui.answered, [ask]: answer } });

let timer: ReturnType<typeof setInterval> | null = null;

/** play moves through the remaining steps, one every few seconds; from the last step it starts over. */
export function play(on: boolean) {
  if (timer) clearInterval(timer);
  timer = null;
  if (!on) return set({ playing: false });
  if (state.step === scenario.steps.length - 1) {
    window.location.href = labHref({ step: "1", play: "1" });
    return;
  }
  set({ playing: true });
  timer = setInterval(() => {
    goTo(state.step + 1);
    if (state.step >= scenario.steps.length - 1) play(false);
  }, 4000);
}

// A page opened with play=1 (Play pressed on the last step starts over from step 1)
// resumes playing once it loads.
if (state.playing) {
  state = { ...state, playing: false };
  setTimeout(() => play(true), 0);
}
