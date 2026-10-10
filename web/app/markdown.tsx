"use client";

// Markdown draws a Markdown file's text as React elements: headings (#, ##, ###), lists
// (- and 1.), fenced code, pipe tables, paragraphs, **bold**, `code` and [links](https://…). Nothing in the text is ever
// run or inserted as HTML. A task the text cites (CHK-16) links to the task.

import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { TaskLinks } from "./task-ui";

function inline(text: string): ReactNode[] {
  return text.split(/(\*\*[^*]+\*\*|`[^`]+`|\[[^\]]+\]\(https?:\/\/[^\s)]+\))/g).map((part, i) => {
    const link = part.match(/^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/);
    if (link)
      return (
        <a key={i} href={link[2]} target="_blank" rel="noopener noreferrer" className="text-link underline underline-offset-[3px] hover:no-underline">
          {link[1]}
        </a>
      );
    if (part.startsWith("**") && part.endsWith("**") && part.length > 4) return <strong key={i}>{part.slice(2, -2)}</strong>;
    if (part.startsWith("`") && part.endsWith("`") && part.length > 2) return <code key={i}>{part.slice(1, -1)}</code>;
    return <TaskLinks key={i} text={part} />;
  });
}

type Align = "left" | "center" | "right" | undefined;
type Block =
  | { kind: "h1" | "h2" | "h3" | "p" | "pre"; text: string }
  | { kind: "ul" | "ol"; items: string[] }
  | { kind: "table"; head: string[]; align: Align[]; rows: string[][] };

// cells splits one table row on its unescaped pipes; the outer pipes are optional and \| is a pipe in the text.
function cells(line: string): string[] {
  let l = line.trim();
  if (l.startsWith("|")) l = l.slice(1);
  if (/(^|[^\\])(\\\\)*\|$/.test(l) && l.endsWith("|")) l = l.slice(0, -1);
  return l.split(/(?<!\\)\|/).map((c) => c.replace(/\\\|/g, "|").trim());
}

const separator = /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/;

function alignOf(cell: string): Align {
  const l = cell.startsWith(":");
  const r = cell.endsWith(":");
  return l && r ? "center" : r ? "right" : l ? "left" : undefined;
}

function blocks(md: string): Block[] {
  const out: Block[] = [];
  let fence: string[] | null = null;
  const lines = md.replace(/\r\n?/g, "\n").split("\n");
  for (let n = 0; n < lines.length; n++) {
    const line = lines[n];
    if (fence) {
      if (/^\s*```/.test(line)) {
        out.push({ kind: "pre", text: fence.join("\n") });
        fence = null;
      } else fence.push(line);
      continue;
    }
    const l = line.trimEnd();
    // A table is a header row with a pipe, then a separator row with as many cells, then body rows.
    const next = lines[n + 1];
    if (l.includes("|") && next !== undefined && next.includes("-") && separator.test(next)) {
      const head = cells(l);
      const marks = cells(next);
      if (head.length === marks.length && next.includes("|")) {
        const rows: string[][] = [];
        n += 2;
        while (n < lines.length && lines[n].trim() && lines[n].includes("|")) {
          const row = cells(lines[n]);
          rows.push(head.map((_, i) => row[i] ?? ""));
          n++;
        }
        n--;
        out.push({ kind: "table", head, align: marks.map(alignOf), rows });
        continue;
      }
    }
    const last = out.at(-1);
    const h = l.match(/^(#{1,6}) (.*)$/);
    const ul = l.match(/^\s*[-*+] (.*)$/);
    const ol = l.match(/^\s*\d+[.)] (.*)$/);
    if (/^\s*```/.test(l)) fence = [];
    else if (!l.trim()) out.push({ kind: "p", text: "" });
    else if (h) out.push({ kind: (["h1", "h2", "h3"] as const)[Math.min(h[1].length, 3) - 1], text: h[2] });
    else if (ul) last?.kind === "ul" ? last.items.push(ul[1]) : out.push({ kind: "ul", items: [ul[1]] });
    else if (ol) last?.kind === "ol" ? last.items.push(ol[1]) : out.push({ kind: "ol", items: [ol[1]] });
    else if (last?.kind === "p" && last.text) last.text += ` ${l.trim()}`;
    else out.push({ kind: "p", text: l.trim() });
  }
  if (fence) out.push({ kind: "pre", text: fence.join("\n") });
  return out.filter((b) => b.kind !== "p" || b.text);
}

export function Markdown({ text, className }: { text: string; className?: string }) {
  return (
    <div className={cn("markdown flex flex-col gap-2 break-words", className)}>
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
              <h5 key={i} className="text-meta font-bold">
                {inline(b.text)}
              </h5>
            );
          case "pre":
            return (
              <pre key={i} className="overflow-x-auto rounded-control px-3 py-2 text-meta">
                <code>{b.text}</code>
              </pre>
            );
          case "table":
            return (
              <div key={i} className="max-w-full overflow-x-auto" tabIndex={0} role="region" aria-label="Table">
                <table className="border-collapse text-meta">
                  <thead>
                    <tr>
                      {b.head.map((c, j) => (
                        <th key={j} scope="col" style={{ textAlign: b.align[j] }} className="border border-rule bg-selected px-2 py-1 text-left font-bold align-bottom">
                          {inline(c)}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {b.rows.map((row, r) => (
                      <tr key={r}>
                        {row.map((c, j) => (
                          <td key={j} style={{ textAlign: b.align[j] }} className="border border-rule px-2 py-1 align-top">
                            {inline(c)}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          case "p":
            return <p key={i}>{inline(b.text)}</p>;
          case "ul":
          case "ol": {
            const List = b.kind;
            return (
              <List key={i} className={cn("flex flex-col gap-1 pl-5", b.kind === "ul" ? "list-disc" : "list-decimal")}>
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
