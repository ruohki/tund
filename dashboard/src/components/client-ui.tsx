"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { useFormStatus } from "react-dom";
import { Check, Copy, Loader2 } from "lucide-react";
import { buttonClass, cn } from "./ui";

export function CopyButton({
  value,
  label,
  className,
  size = "sm",
  variant = "ghost",
}: {
  value: string;
  label?: string;
  className?: string;
  size?: "sm" | "md";
  variant?: "primary" | "secondary" | "ghost";
}) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  return (
    <button
      type="button"
      className={buttonClass(variant, size, cn(!label && "w-7 px-0", className))}
      aria-label={label ?? "Copy to clipboard"}
      title={copied ? "Copied" : "Copy"}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
        } catch {
          const ta = document.createElement("textarea");
          ta.value = value;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand("copy");
          ta.remove();
        }
        setCopied(true);
        if (timer.current) clearTimeout(timer.current);
        timer.current = setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <Check size={14} className="text-ok" /> : <Copy size={14} />}
      {label ? <span>{copied ? "Copied" : label}</span> : null}
    </button>
  );
}

/** A shell command with a prompt and a copy button. */
export function Command({ children, prompt = "$", className }: { children: string; prompt?: string; className?: string }) {
  return (
    <div
      className={cn(
        "group flex items-center gap-2 rounded-md border border-line bg-surface-2 py-1.5 pl-3 pr-1.5 font-mono text-[12.5px] text-ink",
        className,
      )}
    >
      {prompt ? (
        <span aria-hidden className="select-none text-muted">
          {prompt}
        </span>
      ) : null}
      <code className="min-w-0 flex-1 overflow-x-auto whitespace-pre scroll-thin py-0.5">{children}</code>
      <CopyButton value={children} />
    </div>
  );
}

export function SubmitButton({
  children,
  variant = "primary",
  size = "md",
  className,
  pendingText,
  disabled,
  name,
  value,
}: {
  children: ReactNode;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  className?: string;
  pendingText?: string;
  disabled?: boolean;
  /** Sent with the form when this button submits it (React includes the submitter). */
  name?: string;
  value?: string;
}) {
  const { pending } = useFormStatus();
  return (
    <button
      type="submit"
      name={name}
      value={value}
      disabled={pending || disabled}
      className={buttonClass(variant, size, className)}
    >
      {pending ? <Loader2 size={14} className="animate-spin" /> : null}
      {pending && pendingText ? pendingText : children}
    </button>
  );
}

/** Submit button that needs a second click within a few seconds. */
export function ConfirmSubmit({
  children,
  confirmText = "Click again to confirm",
  variant = "danger",
  size = "sm",
  className,
}: {
  children: ReactNode;
  confirmText?: string;
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  className?: string;
}) {
  const [armed, setArmed] = useState(false);
  const { pending } = useFormStatus();
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 3500);
    return () => clearTimeout(t);
  }, [armed]);
  return (
    <button
      type={armed ? "submit" : "button"}
      disabled={pending}
      onClick={(e) => {
        if (!armed) {
          e.preventDefault();
          setArmed(true);
        }
      }}
      className={buttonClass(armed ? "danger" : variant, size, cn(armed && "border-danger/60", className))}
    >
      {pending ? <Loader2 size={14} className="animate-spin" /> : null}
      {armed ? confirmText : children}
    </button>
  );
}

export function Tabs({
  tabs,
  initial,
  className,
}: {
  tabs: { id: string; label: ReactNode; content: ReactNode }[];
  initial?: string;
  className?: string;
}) {
  const [active, setActive] = useState(initial ?? tabs[0]?.id);
  return (
    <div className={className}>
      <div role="tablist" className="flex gap-1 border-b border-line">
        {tabs.map((t) => (
          <button
            key={t.id}
            role="tab"
            type="button"
            aria-selected={active === t.id}
            onClick={() => setActive(t.id)}
            className={cn(
              "-mb-px border-b-2 px-3 py-2 text-[13px] font-medium transition-colors",
              active === t.id ? "border-ink text-ink" : "border-transparent text-muted hover:text-ink",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>
      {tabs.map((t) => (
        <div key={t.id} role="tabpanel" hidden={active !== t.id} className="pt-4">
          {t.content}
        </div>
      ))}
    </div>
  );
}

/** Detects the visitor's OS once mounted so install tabs can default sensibly. */
export function useDetectedOs(): "mac" | "linux" | "windows" | null {
  const [os, setOs] = useState<"mac" | "linux" | "windows" | null>(null);
  useEffect(() => {
    const p = navigator.userAgent.toLowerCase();
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setOs(p.includes("win") ? "windows" : p.includes("mac") ? "mac" : "linux");
  }, []);
  return os;
}
