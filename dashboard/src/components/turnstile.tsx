"use client";

import { useEffect, useRef } from "react";

declare global {
  interface Window {
    turnstile?: {
      render: (el: HTMLElement, opts: Record<string, unknown>) => string;
      reset: (id: string) => void;
      remove: (id: string) => void;
    };
  }
}

const SRC = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";

/**
 * Cloudflare Turnstile widget; adds a cf-turnstile-response field to the
 * surrounding form. Tokens work once, so pass the form's result as `reset` to
 * get a fresh challenge after every submit.
 */
export function Turnstile({ siteKey, reset }: { siteKey: string; reset?: unknown }) {
  const ref = useRef<HTMLDivElement>(null);
  const widget = useRef<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const render = () => {
      if (cancelled || !ref.current || !window.turnstile || widget.current) return;
      widget.current = window.turnstile.render(ref.current, { sitekey: siteKey, theme: "auto" });
    };
    let script: HTMLScriptElement | null = null;
    if (window.turnstile) render();
    else {
      script = document.querySelector<HTMLScriptElement>(`script[src="${SRC}"]`);
      if (!script) {
        script = document.createElement("script");
        script.src = SRC;
        script.async = true;
        document.head.appendChild(script);
      }
      script.addEventListener("load", render);
    }
    return () => {
      cancelled = true;
      script?.removeEventListener("load", render);
      if (widget.current && window.turnstile) window.turnstile.remove(widget.current);
      widget.current = null;
    };
  }, [siteKey]);

  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    if (widget.current && window.turnstile) window.turnstile.reset(widget.current);
  }, [reset]);

  return <div ref={ref} className="min-h-[65px]" />;
}
