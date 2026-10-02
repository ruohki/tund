"use client";

import { useEffect, useMemo, useState } from "react";
import { ArrowLeftRight, Loader2, X } from "lucide-react";
import type { BodyView, RequestDetail as Detail } from "@/lib/requests";
import { formatBytes, formatDateTime, formatDuration } from "@/lib/format";
import { useDisplayTimeZone } from "@/lib/use-hydrated";
import { buttonClass, cn, StatusCode } from "../ui";
import { diffLines, foldUnchanged, type DiffFold, type DiffLine } from "./diff";

function bodyText(b: BodyView): string {
  switch (b.kind) {
    case "empty":
      return "(no body)";
    case "json":
    case "text":
    case "form":
      return (b.text ?? "") + (b.truncated ? "\n(only the beginning was captured)" : "");
    default:
      return b.hex ? `(binary, ${formatBytes(b.size)})\n${b.hex}` : `(binary, ${formatBytes(b.size)})`;
  }
}

function requestText(d: Detail): string {
  return [`${d.method} ${d.url}`, ...d.request.headers.map(([k, v]) => `${k}: ${v}`), "", bodyText(d.request.body)].join("\n");
}

function responseText(d: Detail): string {
  if (d.status === 0) return `No response${d.error ? `: ${d.error}` : ""}`;
  return [`${d.status}`, ...d.response.headers.map(([k, v]) => `${k}: ${v}`), "", bodyText(d.response.body)].join("\n");
}

async function load(id: string, signal: AbortSignal): Promise<Detail> {
  const res = await fetch(`/api/requests/${id}`, { signal });
  if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error ?? `HTTP ${res.status}`);
  return res.json();
}

function DiffBlock({ title, left, right }: { title: string; left: string; right: string }) {
  const [all, setAll] = useState(false);
  const lines = useMemo(() => diffLines(left, right), [left, right]);
  const changed = lines.filter((l) => l.kind !== "same").length;
  const shown: (DiffLine | DiffFold)[] = all ? lines : foldUnchanged(lines);
  return (
    <section className="border-b border-line">
      <div className="flex items-center justify-between gap-2 bg-surface-2 px-4 py-1.5">
        <h3 className="text-[12px] font-semibold text-ink-2">
          {title}{" "}
          <span className="font-normal text-muted">{changed ? `${changed} changed ${changed === 1 ? "line" : "lines"}` : "identical"}</span>
        </h3>
        {shown.some((l) => l.kind === "fold") || all ? (
          <button type="button" onClick={() => setAll((a) => !a)} className="text-[12px] text-ink-2 hover:text-ink hover:underline">
            {all ? "Fold unchanged lines" : "Show all lines"}
          </button>
        ) : null}
      </div>
      <pre className="relative overflow-x-auto scroll-thin py-1 font-mono text-[12px] leading-[1.55]">
        {shown.map((l, i) =>
          l.kind === "fold" ? (
            <div key={i} className="select-none px-4 text-muted">
              ⋯ {l.count} unchanged {l.count === 1 ? "line" : "lines"}
            </div>
          ) : (
            <div
              key={i}
              className={cn(
                "whitespace-pre px-4",
                l.kind === "del" && "bg-danger-wash text-danger",
                l.kind === "add" && "bg-ok-wash text-ok",
                l.kind === "same" && "text-ink-2",
              )}
            >
              <span className="mr-3 inline-block w-2 select-none text-muted" aria-hidden>
                {l.kind === "del" ? "−" : l.kind === "add" ? "+" : " "}
              </span>
              <span className="sr-only">{l.kind === "del" ? "only in A: " : l.kind === "add" ? "only in B: " : ""}</span>
              {l.text || " "}
            </div>
          ),
        )}
      </pre>
    </section>
  );
}

/** Two captured requests side by side: facts, then line diffs of request and response. */
export function CompareView({
  leftId,
  rightId,
  onSwap,
  onClose,
}: {
  leftId: string;
  rightId: string;
  onSwap: () => void;
  onClose: () => void;
}) {
  const tz = useDisplayTimeZone();
  const [pair, setPair] = useState<{ key: string; a: Detail; b: Detail } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const key = `${leftId}:${rightId}`;

  useEffect(() => {
    const ctrl = new AbortController();
    Promise.all([load(leftId, ctrl.signal), load(rightId, ctrl.signal)])
      .then(([a, b]) => {
        setPair({ key: `${leftId}:${rightId}`, a, b });
        setError(null);
      })
      .catch((err) => {
        if (err.name !== "AbortError") setError(err.message);
      });
    return () => ctrl.abort();
  }, [leftId, rightId]);

  const header = (
    <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-3">
      <div className="flex items-center gap-2">
        <h2 className="text-[14px] font-semibold text-ink">Compare requests</h2>
        {pair?.key !== key && !error ? <Loader2 size={13} className="animate-spin text-muted" /> : null}
      </div>
      <div className="flex items-center gap-1">
        <button type="button" onClick={onSwap} className={buttonClass("ghost", "sm")} title="Swap A and B">
          <ArrowLeftRight size={13} />
          Swap
        </button>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close comparison"
          className="grid h-7 w-7 place-items-center rounded-[5px] text-muted hover:bg-surface-3 hover:text-ink"
        >
          <X size={16} />
        </button>
      </div>
    </div>
  );

  if (error) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {header}
        <p className="m-4 text-[13px] text-danger">Couldn&apos;t load both requests: {error}</p>
      </div>
    );
  }
  if (!pair) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {header}
        <p className="m-auto text-[13px] text-muted">Loading…</p>
      </div>
    );
  }
  const { a, b } = pair;
  const rows: [string, (d: Detail) => React.ReactNode][] = [
    ["Request", (d) => <span className="font-mono text-[12.5px]">{d.method} {d.path}</span>],
    ["Host", (d) => <span className="font-mono text-[12.5px]">{d.hostname}</span>],
    ["Status", (d) => <StatusCode status={d.status} error={d.error} showText />],
    ["Duration", (d) => formatDuration(d.durationMs)],
    ["Response body", (d) => formatBytes(d.respSize)],
    ["Received", (d) => formatDateTime(d.startedAt, tz)],
  ];
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto scroll-thin">
        <table className="w-full table-fixed border-b border-line text-[13px]">
          <thead>
            <tr className="text-left text-[12px] text-muted">
              <th className="w-28 px-4 py-1.5 font-normal" />
              <th className="px-2 py-1.5 font-medium text-danger">A</th>
              <th className="px-2 py-1.5 font-medium text-ok">B</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(([label, cell]) => (
              <tr key={label} className="border-t border-line align-top">
                <th scope="row" className="px-4 py-1.5 text-left text-[12px] font-normal text-muted">
                  {label}
                </th>
                <td className="truncate px-2 py-1.5 text-ink">{cell(a)}</td>
                <td className="truncate px-2 py-1.5 text-ink">{cell(b)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        <DiffBlock title="Request" left={requestText(a)} right={requestText(b)} />
        <DiffBlock title="Response" left={responseText(a)} right={responseText(b)} />
      </div>
    </div>
  );
}
