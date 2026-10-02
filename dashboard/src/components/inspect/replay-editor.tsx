"use client";

import { useEffect, useId, useState, useTransition } from "react";
import { Loader2, Send } from "lucide-react";
import type { RequestDetail as Detail, RequestSummary } from "@/lib/requests";
import { replayRequestAction, replayTargetsAction, type ReplayOverride } from "@/app/actions/requests";
import { buttonClass, cn, Input, Select, Textarea } from "../ui";

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const FRAMING = new Set(["content-length", "transfer-encoding", "connection"]);

/** Header lines "Name: value" → header map; null and an error for a bad line. */
function parseHeaders(text: string): { headers: Record<string, string[]> } | { error: string } {
  const headers: Record<string, string[]> = {};
  for (const [i, raw] of text.split("\n").entries()) {
    const line = raw.trim();
    if (!line) continue;
    const at = line.indexOf(":");
    if (at <= 0) return { error: `Header line ${i + 1} needs the form “Name: value”.` };
    const name = line.slice(0, at).trim();
    (headers[name] ??= []).push(line.slice(at + 1).trim());
  }
  return { headers };
}

/**
 * Edits a captured request and replays it: another method, path, headers or
 * body, or through another of the user's online tunnels.
 */
export function ReplayEditor({
  detail,
  onReplayed,
  onCancel,
}: {
  detail: Detail;
  onReplayed: (res: { summary: RequestSummary | null; requestId: string }) => void;
  onCancel: () => void;
}) {
  const id = useId();
  const [targets, setTargets] = useState<string[] | null>(null);
  const [host, setHost] = useState(detail.hostname);
  const [method, setMethod] = useState(detail.method);
  const [path, setPath] = useState(detail.path);
  // Framing headers are recomputed for the new body; leave them out.
  const [headers, setHeaders] = useState(() =>
    detail.request.headers
      .filter(([k]) => !FRAMING.has(k.toLowerCase()))
      .map(([k, v]) => `${k}: ${v}`)
      .join("\n"),
  );
  const reqBody = detail.request.body;
  const bodyEditable = !reqBody.truncated && ["empty", "json", "text", "form"].includes(reqBody.kind);
  const [body, setBody] = useState(() => (bodyEditable ? (reqBody.text ?? "") : ""));
  const [error, setError] = useState<string | null>(null);
  const [pending, start] = useTransition();

  useEffect(() => {
    replayTargetsAction()
      .then(setTargets)
      .catch(() => setTargets([]));
  }, []);

  // A full URL pasted into the path field picks the tunnel too.
  const onPath = (value: string) => {
    if (/^https?:\/\//i.test(value.trim())) {
      try {
        const u = new URL(value.trim());
        setHost(u.hostname.toLowerCase());
        setPath(u.pathname + u.search);
        return;
      } catch {
        /* keep the raw text; validation explains */
      }
    }
    setPath(value);
  };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const parsed = parseHeaders(headers);
    if ("error" in parsed) {
      setError(parsed.error);
      return;
    }
    const override: ReplayOverride = { method: method.trim().toUpperCase(), path: path.trim() || "/", headers: parsed.headers };
    if (host !== detail.hostname) override.hostname = host;
    if (bodyEditable) override.body = body;
    start(async () => {
      const res = await replayRequestAction(detail.id, override);
      if (!res.ok) {
        setError(res.error);
        return;
      }
      onReplayed(res);
    });
  };

  const hostOptions = [...new Set([...(targets ?? []), detail.hostname, host])].map((h) => ({
    value: h,
    label: targets && !targets.includes(h) ? `${h} (offline)` : h,
  }));

  return (
    <form onSubmit={submit} className="space-y-3 border-b border-line bg-surface-2 px-4 py-3">
      <p className="text-[12.5px] text-ink-2">
        Change the request and send it again. It runs through the chosen tunnel and shows up as a replay.
      </p>
      <div className="flex flex-wrap gap-2">
        <label className="w-32">
          <span className="mb-1 block text-[12px] text-muted">Method</span>
          <Input
            list={`${id}-methods`}
            value={method}
            onChange={(e) => setMethod(e.target.value)}
            className="h-7.5 font-mono text-[12.5px] uppercase"
            spellCheck={false}
          />
          <datalist id={`${id}-methods`}>
            {METHODS.map((m) => (
              <option key={m} value={m} />
            ))}
          </datalist>
        </label>
        <div className="min-w-48 flex-1">
          <span className="mb-1 block text-[12px] text-muted">
            Tunnel
          </span>
          <Select
            aria-label="Tunnel to send the request through"
            value={host}
            onValueChange={setHost}
            mono
            className="h-7.5 w-full text-[12.5px]"
            options={hostOptions}
          />
        </div>
      </div>
      <label className="block">
        <span className="mb-1 block text-[12px] text-muted">Path and query (or paste a full URL)</span>
        <Input value={path} onChange={(e) => onPath(e.target.value)} className="h-7.5 font-mono text-[12.5px]" spellCheck={false} />
      </label>
      <label className="block">
        <span className="mb-1 block text-[12px] text-muted">Headers, one “Name: value” per line</span>
        <Textarea
          value={headers}
          onChange={(e) => setHeaders(e.target.value)}
          rows={Math.min(10, Math.max(3, headers.split("\n").length))}
          className="font-mono text-[12px]"
          spellCheck={false}
        />
      </label>
      {bodyEditable ? (
        <label className="block">
          <span className="mb-1 block text-[12px] text-muted">Body</span>
          <Textarea
            value={body}
            onChange={(e) => setBody(e.target.value)}
            rows={Math.min(14, Math.max(3, body.split("\n").length))}
            className="font-mono text-[12px]"
            spellCheck={false}
          />
        </label>
      ) : (
        <p className="text-[12.5px] text-muted">
          The body is {reqBody.truncated ? "only partly captured" : "binary"} and is sent as captured.
        </p>
      )}
      {error ? <p className="text-[12.5px] text-danger">{error}</p> : null}
      <div className="flex items-center gap-2">
        <button type="submit" disabled={pending} className={buttonClass("primary", "sm")}>
          {pending ? <Loader2 size={13} className="animate-spin" /> : <Send size={13} />}
          Send
        </button>
        <button type="button" onClick={onCancel} className={cn(buttonClass("ghost", "sm"))}>
          Cancel
        </button>
      </div>
    </form>
  );
}
