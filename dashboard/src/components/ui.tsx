import Link from "next/link";
import type { ComponentProps, ReactNode } from "react";
import { statusClass, STATUS_TEXT } from "@/lib/format";

import { cn, inputClass } from "./classes";

export { cn, inputClass };
export { Select, type SelectOption, type SelectGroup } from "./select";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

export function buttonClass(variant: Variant = "secondary", size: Size = "md", extra?: string) {
  return cn(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-[5px] font-medium transition-colors",
    "disabled:pointer-events-none disabled:opacity-50 select-none",
    size === "sm" ? "h-7 px-2.5 text-[13px]" : "h-8.5 px-3.5 text-sm",
    variant === "primary" && "bg-btn text-btn-ink hover:bg-btn-hover",
    variant === "secondary" && "border border-line-strong bg-surface text-ink hover:bg-surface-2",
    variant === "ghost" && "text-ink-2 hover:bg-surface-3 hover:text-ink",
    variant === "danger" && "border border-line-strong bg-surface text-danger hover:bg-danger-wash hover:border-danger/40",
    extra,
  );
}

export function Button({
  variant,
  size,
  className,
  ...props
}: ComponentProps<"button"> & { variant?: Variant; size?: Size }) {
  return <button className={buttonClass(variant, size, className)} {...props} />;
}

export function ButtonLink({
  variant,
  size,
  className,
  ...props
}: ComponentProps<typeof Link> & { variant?: Variant; size?: Size }) {
  return <Link className={buttonClass(variant, size, className)} {...props} />;
}


export function Input({ className, ...props }: ComponentProps<"input">) {
  return <input className={cn(inputClass, className)} {...props} />;
}

export function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return <textarea className={cn(inputClass, "h-auto py-2 leading-snug", className)} {...props} />;
}


export function Field({
  label,
  hint,
  htmlFor,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  htmlFor?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-[13px] font-medium text-ink">
        {label}
      </label>
      {children}
      {hint ? <p className="text-xs text-muted">{hint}</p> : null}
    </div>
  );
}

export function Panel({
  id,
  title,
  description,
  actions,
  children,
  className,
  bodyClassName,
}: {
  /** Anchor target; the panel keeps clear of the sticky mobile header when scrolled to. */
  id?: string;
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <section id={id} className={cn("scroll-mt-16 rounded-lg border border-line bg-surface", className)}>
      {title || actions ? (
        <header className="flex items-start justify-between gap-4 border-b border-line px-4 py-3">
          <div className="min-w-0">
            {title ? <h2 className="text-[15px] font-semibold tracking-tight text-ink">{title}</h2> : null}
            {description ? <p className="mt-0.5 text-[13px] text-muted">{description}</p> : null}
          </div>
          {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
        </header>
      ) : null}
      <div className={bodyClassName}>{children}</div>
    </section>
  );
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-[26px] font-semibold leading-tight tracking-[-0.015em] text-ink">{title}</h1>
        {description ? <p className="mt-1 max-w-[70ch] text-[14px] text-ink-2">{description}</p> : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function Badge({
  children,
  tone = "neutral",
  className,
  title,
}: {
  children: ReactNode;
  tone?: "neutral" | "live" | "ok" | "danger" | "outline";
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex h-5 items-center gap-1 rounded-[4px] px-1.5 text-[12px] font-medium leading-none whitespace-nowrap",
        tone === "neutral" && "bg-surface-3 text-ink-2",
        tone === "live" && "bg-sodium-wash text-sodium-ink",
        tone === "ok" && "bg-ok-wash text-ok",
        tone === "danger" && "bg-danger-wash text-danger",
        tone === "outline" && "border border-line-strong text-ink-2",
        className,
      )}
    >
      {children}
    </span>
  );
}

const STATUS_VAR: Record<string, string> = {
  "2xx": "var(--s2xx)",
  "3xx": "var(--s3xx)",
  "4xx": "var(--s4xx)",
  "5xx": "var(--s5xx)",
};

/** Status code in ink with a colored key mark (color never carries meaning alone). */
export function StatusCode({ status, error, showText }: { status: number; error?: string; showText?: boolean }) {
  const cls = statusClass(status);
  return (
    <span className="inline-flex items-center gap-1.5 font-mono text-[12.5px] tabular text-ink" title={error || STATUS_TEXT[status]}>
      <span aria-hidden className="h-2.5 w-[3px] rounded-full" style={{ background: STATUS_VAR[cls] }} />
      {status === 0 ? "ERR" : status}
      {showText && status !== 0 && STATUS_TEXT[status] ? (
        <span className="font-sans text-[13px] text-ink-2">{STATUS_TEXT[status]}</span>
      ) : null}
    </span>
  );
}

export function statusColor(status: number) {
  return STATUS_VAR[statusClass(status)];
}

export function Method({ method }: { method: string }) {
  return <span className="inline-block w-[3.25rem] font-mono text-[12px] font-medium text-ink-2">{method}</span>;
}

/** TCP and TLS tunnels get a badge; HTTP is the default and stays unmarked. */
export function ProtoBadge({ proto }: { proto: string }) {
  if (proto === "tcp") return <Badge tone="outline" title="Raw TCP tunnel">TCP</Badge>;
  if (proto === "tls") return <Badge tone="outline" title="TLS passthrough: the edge doesn't decrypt">TLS</Badge>;
  return null;
}

export function AuthBadge({ mode }: { mode: string }) {
  if (mode === "password") return <Badge tone="outline">Password</Badge>;
  if (mode === "oidc") return <Badge tone="outline">OIDC login</Badge>;
  return <Badge tone="neutral">Public</Badge>;
}

export function EmptyState({
  icon,
  title,
  children,
  action,
}: {
  icon?: ReactNode;
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center px-6 py-12 text-center">
      {icon ? <div className="mb-3 text-muted">{icon}</div> : null}
      <p className="text-[15px] font-semibold text-ink">{title}</p>
      {children ? <div className="mt-1 max-w-[52ch] text-[13px] text-ink-2">{children}</div> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12.5px]", className)}>{children}</span>;
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="rounded border border-line-strong bg-surface-2 px-1 font-mono text-[11px] text-ink-2">{children}</kbd>
  );
}

export function FormMessage({ state }: { state?: { error?: string | null; ok?: string | null } | null }) {
  if (!state) return null;
  if (state.error)
    return (
      <p role="alert" className="rounded-[5px] border border-danger/30 bg-danger-wash px-3 py-2 text-[13px] text-danger">
        {state.error}
      </p>
    );
  if (state.ok)
    return (
      <p role="status" className="rounded-[5px] border border-ok/30 bg-ok-wash px-3 py-2 text-[13px] text-ok">
        {state.ok}
      </p>
    );
  return null;
}
