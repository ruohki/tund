"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { RotateCcw } from "lucide-react";
import { buttonClass } from "./ui";
import { CopyButton } from "./client-ui";

type Hint = { title: string; body: React.ReactNode };

function hintsFor(message: string): Hint[] {
  const m = message.toLowerCase();
  const hints: Hint[] = [];
  if (m.includes("tund_database_url") || m.includes("econnrefused") || m.includes("connect") || m.includes("postgres")) {
    hints.push({
      title: "The database may be unreachable",
      body: (
        <>
          Check that Postgres is running and <code className="font-mono text-[12px]">TUND_DATABASE_URL</code> points at it.
          With Docker: <code className="font-mono text-[12px]">docker compose ps</code>.
        </>
      ),
    });
  }
  if (m.includes("relation") && m.includes("does not exist")) {
    hints.push({
      title: "The schema is missing",
      body: "tund-server creates the tables on startup. Start (or restart) the server once, then reload.",
    });
  }
  if (m.includes("tund-server") || m.includes("internal")) {
    hints.push({
      title: "The edge server didn't answer",
      body: (
        <>
          Make sure tund-server is running and <code className="font-mono text-[12px]">TUND_INTERNAL_URL</code> /{" "}
          <code className="font-mono text-[12px]">TUND_INTERNAL_SECRET</code> match its configuration.
        </>
      ),
    });
  }
  hints.push({
    title: "Try again",
    body: "Temporary problems (a restart, a dropped database connection) usually clear up on retry.",
  });
  hints.push({
    title: "Find it in the logs",
    body: (
      <>
        Server errors are logged with their digest:{" "}
        <code className="font-mono text-[12px]">docker compose logs dashboard | grep &lt;digest&gt;</code>
      </>
    ),
  });
  return hints;
}

export function ErrorView({
  error,
  retry,
  standalone,
}: {
  error: Error & { digest?: string };
  retry: () => void;
  standalone?: boolean;
}) {
  const [path, setPath] = useState("");
  const [time] = useState(() => new Date().toISOString());
  useEffect(() => {
    console.error(error);
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setPath(window.location.pathname + window.location.search);
  }, [error]);

  const message = error.message || "Unknown error";
  const report = [`Path: ${path}`, `Time: ${time}`, error.digest ? `Digest: ${error.digest}` : null, `Message: ${message}`]
    .filter(Boolean)
    .join("\n");

  return (
    <div className={standalone ? "mx-auto max-w-2xl px-6 py-16" : "mx-auto max-w-2xl py-10"}>
      <div className="mb-6 flex items-center gap-3 font-mono text-[12.5px]" aria-hidden>
        <span className="rounded-[5px] border border-line-strong bg-surface px-2 py-1 text-ink">dashboard</span>
        <span className="relative h-[2px] flex-1 rounded bg-line-strong">
          <span className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 bg-page px-1.5 text-[13px] font-semibold text-danger">
            ✕
          </span>
        </span>
        <span className="rounded-[5px] border border-dashed border-line-strong px-2 py-1 text-muted">{path || "…"}</span>
      </div>
      <h1 className="text-[26px] font-semibold tracking-[-0.015em] text-ink">This page failed to load</h1>
      <p className="mt-2 text-[14px] text-ink-2">
        Something went wrong while rendering it. Your tunnels are not affected; they keep running on the edge server.
      </p>

      <div className="mt-6 rounded-lg border border-line bg-surface">
        <div className="flex items-start justify-between gap-3 border-b border-line px-4 py-2.5">
          <p className="text-[13px] font-medium text-ink">Error details</p>
          <CopyButton value={report} label="Copy details" />
        </div>
        <dl className="grid grid-cols-[6rem_minmax(0,1fr)] gap-y-1.5 px-4 py-3 text-[13px]">
          <dt className="text-muted">Message</dt>
          <dd className="break-words font-mono text-[12.5px] text-ink">{message}</dd>
          {error.digest ? (
            <>
              <dt className="text-muted">Digest</dt>
              <dd className="font-mono text-[12.5px] text-ink">{error.digest}</dd>
            </>
          ) : null}
          <dt className="text-muted">Page</dt>
          <dd className="break-all font-mono text-[12.5px] text-ink-2">{path}</dd>
          <dt className="text-muted">Time</dt>
          <dd className="font-mono text-[12.5px] text-ink-2">{time}</dd>
        </dl>
      </div>

      <h2 className="mt-8 text-[15px] font-semibold text-ink">What to try</h2>
      <ul className="mt-3 flex flex-col gap-3">
        {hintsFor(message).map((h) => (
          <li key={h.title} className="border-l-2 border-line-strong pl-3">
            <p className="text-[13.5px] font-medium text-ink">{h.title}</p>
            <p className="text-[13px] text-ink-2">{h.body}</p>
          </li>
        ))}
      </ul>

      <div className="mt-8 flex flex-wrap gap-2">
        <button type="button" onClick={() => retry()} className={buttonClass("primary")}>
          <RotateCcw size={14} /> Try again
        </button>
        {standalone ? (
          // global-error replaces the root layout, so do a full navigation.
          // eslint-disable-next-line @next/next/no-html-link-for-pages
          <a href="/" className={buttonClass("secondary")}>
            Go to overview
          </a>
        ) : (
          <Link href="/" className={buttonClass("secondary")}>
            Go to overview
          </Link>
        )}
      </div>
    </div>
  );
}
