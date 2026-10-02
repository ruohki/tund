"use client";

import { useState, type ReactNode } from "react";
import Link from "next/link";
import { ChevronDown, Columns2, Download, ExternalLink, Loader2, PencilLine, RotateCcw, X } from "lucide-react";
import type { RequestDetail as Detail, RequestSummary } from "@/lib/requests";
import { formatBytes, formatDateTime, formatDuration } from "@/lib/format";
import { buttonClass, cn, StatusCode } from "../ui";
import { CopyButton } from "../client-ui";
import { BodyView, KeyValueTable } from "./body-view";
import { toCurl } from "./curl";
import { ReplayEditor } from "./replay-editor";
import { useDisplayTimeZone } from "@/lib/use-hydrated";

function Section({
  title,
  count,
  children,
  defaultOpen = true,
}: {
  title: string;
  count?: number;
  children: ReactNode;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div className="border-b border-line">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="flex w-full items-center gap-1.5 px-4 py-2 text-left text-[13px] font-medium text-ink hover:bg-surface-2"
      >
        <ChevronDown size={14} className={cn("text-muted transition-transform", !open && "-rotate-90")} />
        {title}
        {count != null ? <span className="font-normal text-muted tabular">{count}</span> : null}
      </button>
      {open ? <div className="border-t border-line">{children}</div> : null}
    </div>
  );
}

function Fact({ label, children, mono }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-[12px] text-muted">{label}</dt>
      <dd className={cn("truncate text-[13px] text-ink", mono && "font-mono text-[12.5px]")}>{children}</dd>
    </div>
  );
}

export function RequestDetail({
  detail,
  loading,
  onReplay,
  onReplayed,
  replaying,
  replayError,
  onCompare,
  onClose,
}: {
  detail: Detail;
  loading: boolean;
  onReplay: () => void;
  /** A replay sent from the editor went through. */
  onReplayed: (res: { summary: RequestSummary | null; requestId: string }) => void;
  replaying: boolean;
  replayError: string | null;
  /** Start picking a second request to compare this one with. */
  onCompare?: () => void;
  onClose?: () => void;
}) {
  const tz = useDisplayTimeZone();
  const [editing, setEditing] = useState(false);
  return (
    <div className={cn("flex min-h-0 flex-1 flex-col transition-opacity", loading && "opacity-60")}>
      <div className="border-b border-line px-4 py-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2.5">
              <span className="font-mono text-[13px] font-medium text-ink">{detail.method}</span>
              <StatusCode status={detail.status} error={detail.error} showText />
              {detail.replayOf ? (
                <span className="rounded-[4px] bg-surface-3 px-1.5 text-[11.5px] text-ink-2">Replay</span>
              ) : null}
              {detail.teamSlug ? (
                <span className="rounded-[4px] border border-line-strong px-1.5 text-[11.5px] text-ink-2">
                  Team {detail.teamSlug}
                </span>
              ) : null}
              {loading ? <Loader2 size={13} className="animate-spin text-muted" /> : null}
            </div>
            <p className="mt-1 break-all font-mono text-[12.5px] text-ink-2">
              <span className="text-muted">{detail.url.slice(0, detail.url.length - detail.path.length)}</span>
              <span className="text-ink">{detail.path}</span>
            </p>
          </div>
          {onClose ? (
            <button
              type="button"
              onClick={onClose}
              aria-label="Close details"
              className="grid h-7 w-7 shrink-0 place-items-center rounded-[5px] text-muted hover:bg-surface-3 hover:text-ink"
            >
              <X size={16} />
            </button>
          ) : null}
        </div>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          <button type="button" onClick={onReplay} disabled={replaying} className={buttonClass("primary", "sm")}>
            {replaying ? <Loader2 size={13} className="animate-spin" /> : <RotateCcw size={13} />}
            Replay
          </button>
          <button
            type="button"
            onClick={() => setEditing((e) => !e)}
            aria-expanded={editing}
            className={buttonClass(editing ? "secondary" : "ghost", "sm", editing ? "bg-surface-3" : undefined)}
          >
            <PencilLine size={13} />
            Edit &amp; replay
          </button>
          <CopyButton value={toCurl(detail)} label="Copy as cURL" variant="secondary" />
          {onCompare ? (
            <button type="button" onClick={onCompare} className={buttonClass("ghost", "sm")} title="Pick another request to compare with this one">
              <Columns2 size={13} />
              Compare
            </button>
          ) : null}
          <a href={`/api/requests/har?id=${detail.id}`} download className={buttonClass("ghost", "sm")} title="Download as HAR (HTTP Archive)">
            <Download size={13} />
            HAR
          </a>
          {detail.method === "GET" ? (
            <a href={detail.url} target="_blank" rel="noreferrer" className={buttonClass("ghost", "sm")}>
              <ExternalLink size={13} />
              Open URL
            </a>
          ) : null}
        </div>
        {replayError ? <p className="mt-2 text-[12.5px] text-danger">{replayError}</p> : null}
        {detail.error ? (
          <p className="mt-2 rounded-[5px] border border-danger/30 bg-danger-wash px-2.5 py-1.5 text-[12.5px] text-danger">
            {detail.error}
          </p>
        ) : null}

        <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4">
          <Fact label="Duration">{formatDuration(detail.durationMs)}</Fact>
          <Fact label="Time to first byte">{formatDuration(detail.ttfbMs)}</Fact>
          <Fact label="Request body">{formatBytes(detail.reqSize)}</Fact>
          <Fact label="Response body">{formatBytes(detail.respSize)}</Fact>
          <Fact label="Received">{formatDateTime(detail.startedAt, tz)}</Fact>
          <Fact label="Client IP" mono>
            {detail.remoteAddr || "—"}
          </Fact>
          <Fact label="Protocol" mono>
            {detail.proto || "—"}
          </Fact>
          <Fact label="Tunnel">
            {detail.tunnel ? (
              <Link href={`/inspect?host=${encodeURIComponent(detail.hostname)}`} className="hover:underline" title={detail.tunnel.localAddr}>
                {detail.tunnel.name}
                <span className="text-muted">{detail.tunnel.online ? "" : " (offline)"}</span>
              </Link>
            ) : (
              "—"
            )}
          </Fact>
        </dl>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto scroll-thin">
        {editing ? (
          <ReplayEditor
            detail={detail}
            onCancel={() => setEditing(false)}
            onReplayed={(res) => {
              setEditing(false);
              onReplayed(res);
            }}
          />
        ) : null}
        <h3 className="border-b border-line bg-surface-2 px-4 py-1.5 text-[12px] font-semibold text-ink-2">Request</h3>
        {detail.query.length ? (
          <Section title="Query parameters" count={detail.query.length}>
            <KeyValueTable rows={detail.query} />
          </Section>
        ) : null}
        <Section title="Headers" count={detail.request.headers.length}>
          <KeyValueTable rows={detail.request.headers} />
        </Section>
        <Section title="Body" defaultOpen={detail.request.body.kind !== "empty"}>
          <BodyView body={detail.request.body} requestId={detail.id} part="req" />
        </Section>

        <h3 className="border-b border-line bg-surface-2 px-4 py-1.5 text-[12px] font-semibold text-ink-2">Response</h3>
        {detail.status === 0 ? (
          <p className="px-4 py-3 text-[13px] text-muted">No response was received from the local service.</p>
        ) : (
          <>
            <Section title="Headers" count={detail.response.headers.length}>
              <KeyValueTable rows={detail.response.headers} />
            </Section>
            <Section title="Body">
              <BodyView body={detail.response.body} requestId={detail.id} part="resp" />
            </Section>
          </>
        )}
      </div>
    </div>
  );
}
