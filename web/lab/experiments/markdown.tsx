// EXPERIMENTAL, lab only: a small Markdown renderer for the brief and Markdown
// artifacts. Headings (#, ##, ###), lists (- and 1.), paragraphs, **bold** and `code`,
// drawn as React elements, so nothing in the text is ever run as HTML. A task the text
// cites (T5) links to the task, so a claim leads to its evidence.

import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

type Cite = (ref: string) => (() => void) | null;

let cite: Cite | undefined;

function inline(text: string): ReactNode[] {
  return text.split(/(\*\*[^*]+\*\*|`[^`]+`|\bT\d+\b)/g).map((part, i) => {
    const go = /^T\d+$/.test(part) ? cite?.(part) : null;
    if (go) {
      return (
        <button key={i} type="button" onClick={go} className="font-bold text-link underline decoration-1 underline-offset-[3px] hover:no-underline" title={`Open ${part}`}>
          {part}
        </button>
      );
    }
    if (part.startsWith("**") && part.endsWith("**")) return <strong key={i}>{part.slice(2, -2)}</strong>;
    if (part.startsWith("`") && part.endsWith("`")) return <code key={i}>{part.slice(1, -1)}</code>;
    return part;
  });
}

type Block = { kind: "h1" | "h2" | "h3" | "p" } & { text: string } | { kind: "ul" | "ol"; items: string[] };

function blocks(md: string): Block[] {
  const out: Block[] = [];
  for (const line of md.split("\n")) {
    const l = line.trimEnd();
    const last = out.at(-1);
    const h = l.match(/^(#{1,3}) (.*)$/);
    const ul = l.match(/^\s*[-*] (.*)$/);
    const ol = l.match(/^\s*\d+\. (.*)$/);
    if (!l.trim()) out.push({ kind: "p", text: "" });
    else if (h) out.push({ kind: (["h1", "h2", "h3"] as const)[h[1].length - 1], text: h[2] });
    else if (ul) last?.kind === "ul" ? last.items.push(ul[1]) : out.push({ kind: "ul", items: [ul[1]] });
    else if (ol) last?.kind === "ol" ? last.items.push(ol[1]) : out.push({ kind: "ol", items: [ol[1]] });
    else if (last?.kind === "p" && last.text) last.text += ` ${l.trim()}`;
    else out.push({ kind: "p", text: l.trim() });
  }
  return out.filter((b) => b.kind !== "p" || b.text);
}

export function Markdown({ text, className, refs }: { text: string; className?: string; refs?: Cite }) {
  cite = refs;
  return (
    <div className={cn("markdown flex flex-col gap-2", className)}>
      {blocks(text).map((b, i) => {
        switch (b.kind) {
          case "h1":
            return (
              <h3 key={i} className="text-title font-bold">
                {inline(b.text)}
              </h3>
            );
          case "h2":
            return (
              <h4 key={i} className="pt-1 font-bold">
                {inline(b.text)}
              </h4>
            );
          case "h3":
            return (
              <h5 key={i} className="text-meta font-bold text-muted">
                {inline(b.text)}
              </h5>
            );
          case "p":
            return <p key={i}>{inline(b.text)}</p>;
          case "ul":
          case "ol": {
            const List = b.kind;
            return (
              <List key={i} className={cn("flex flex-col gap-1 pl-5 marker:text-muted", b.kind === "ul" ? "list-disc" : "list-decimal")}>
                {b.items.map((item, j) => (
                  <li key={j}>{inline(item)}</li>
                ))}
              </List>
            );
          }
        }
      })}
    </div>
  );
}
