"use client";

import { Fragment, useMemo, useState } from "react";
import { Download, TriangleAlert, WrapText } from "lucide-react";
import type { BodyView as Body } from "@/lib/requests";
import { formatBytes } from "@/lib/format";
import { CopyButton } from "../client-ui";
import { cn } from "../ui";

/** Minimal JSON highlighter: keys, strings, numbers, literals. */
function highlightJson(text: string) {
  const out: React.ReactNode[] = [];
  const re = /("(?:\\.|[^"\\])*")(\s*:)?|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)|\b(true|false|null)\b/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1]) {
      if (m[2]) {
        out.push(
          <span key={i++} className="text-ink">
            {m[1]}
          </span>,
          m[2],
        );
      } else {
        out.push(
          <span key={i++} className="text-[color:var(--syn-str)]">
            {m[1]}
          </span>,
        );
      }
    } else if (m[3]) {
      out.push(
        <span key={i++} className="text-[color:var(--syn-num)]">
          {m[3]}
        </span>,
      );
    } else if (m[4]) {
      out.push(
        <span key={i++} className="text-[color:var(--syn-lit)]">
          {m[4]}
        </span>,
      );
    }
    last = re.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

export function BodyView({
  body,
  requestId,
  part,
}: {
  body: Body;
  requestId: string;
  part: "req" | "resp";
}) {
  const [wrap, setWrap] = useState(true);
  const [raw, setRaw] = useState(false);
  const highlighted = useMemo(
    () => (body.kind === "json" && body.text && body.text.length < 200_000 ? highlightJson(body.text) : null),
    [body],
  );

  if (body.kind === "empty") {
    return <p className="px-4 py-3 text-[13px] text-muted">No body.</p>;
  }

  const href = `/api/requests/${requestId}/body?part=${part}`;
  const canWrap = body.kind === "json" || body.kind === "text" || (body.kind === "form" && raw);

  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-surface-2 px-4 py-1.5">
        <p className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-0.5 text-[12px] text-muted">
          <span className="truncate font-mono text-ink-2">{body.contentType || "no content-type"}</span>
          <span className="tabular">{formatBytes(body.size)}</span>
          {body.encoding ? <span>decoded from {body.encoding}</span> : null}
        </p>
        <div className="flex items-center gap-0.5">
          {body.kind === "form" ? (
            <button
              type="button"
              onClick={() => setRaw((r) => !r)}
              className="rounded-[4px] px-2 py-0.5 text-[12px] text-muted hover:bg-surface-3 hover:text-ink"
            >
              {raw ? "Table" : "Raw"}
            </button>
          ) : null}
          {canWrap ? (
            <button
              type="button"
              aria-pressed={wrap}
              title={wrap ? "Don't wrap lines" : "Wrap lines"}
              onClick={() => setWrap((w) => !w)}
              className={cn(
                "grid h-7 w-7 place-items-center rounded-[5px] hover:bg-surface-3",
                wrap ? "text-ink" : "text-muted",
              )}
            >
              <WrapText size={14} />
            </button>
          ) : null}
          {body.text ? <CopyButton value={body.text} /> : null}
          <a
            href={href}
            title="Download body"
            aria-label="Download body"
            className="grid h-7 w-7 place-items-center rounded-[5px] text-ink-2 hover:bg-surface-3 hover:text-ink"
          >
            <Download size={14} />
          </a>
        </div>
      </div>

      {body.truncated || body.renderedLimit || body.decodeError ? (
        <div className="flex items-start gap-2 border-b border-line bg-sodium-wash px-4 py-1.5 text-[12px] text-ink-2">
          <TriangleAlert size={13} className="mt-0.5 shrink-0 text-sodium-ink" />
          <span>
            {body.decodeError
              ? body.decodeError
              : body.truncated
                ? `Only the first ${formatBytes(body.captured)} of ${formatBytes(body.size)} were captured.`
                : "Showing the first 512 KB. Download the body to see all of it."}
          </span>
        </div>
      ) : null}

      <div className="max-h-[560px] overflow-auto scroll-thin">
        {body.kind === "image" ? (
          <div className="flex justify-center bg-[repeating-conic-gradient(var(--surface-3)_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-4">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={`${href}&inline=1`} alt="Captured image body" className="max-h-80 max-w-full" />
          </div>
        ) : body.kind === "form" && !raw && body.form ? (
          <KeyValueTable rows={body.form} />
        ) : body.kind === "binary" ? (
          <pre className="p-4 font-mono text-[12px] leading-5 text-ink-2">{body.hex}</pre>
        ) : (
          <pre
            className={cn(
              "p-4 font-mono text-[12px] leading-5 text-ink-2",
              wrap ? "whitespace-pre-wrap break-all" : "whitespace-pre",
            )}
          >
            {highlighted ?? body.text}
          </pre>
        )}
      </div>
    </div>
  );
}

export function KeyValueTable({ rows, empty = "None." }: { rows: [string, string][]; empty?: string }) {
  if (!rows.length) return <p className="px-4 py-3 text-[13px] text-muted">{empty}</p>;
  return (
    <dl className="grid grid-cols-[minmax(8rem,max-content)_minmax(0,1fr)] font-mono text-[12px] leading-5">
      {rows.map(([k, v], i) => (
        <Fragment key={`${k}-${i}`}>
          <dt className="border-b border-line px-4 py-1 text-muted">{k}</dt>
          <dd className="border-b border-line py-1 pr-4 break-all text-ink">{v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}
