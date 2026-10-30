"use client";

import { useEffect } from "react";

declare global {
  interface Window {
    ITillSupport?: (command: string, ...args: unknown[]) => void;
    __ITillSupportLoaded?: boolean;
  }
}

const SRC = "https://support.till.md/widget.js";
const HOST_ID = "itill-support-widget";
/** Time the chat's iframe gets to forget the session before it is removed. */
const LOGOUT_MS = 300;

// The loader runs once per document, can't be unloaded and can't change its
// key, while the dashboard stays in one document across sign-in and sign-out.
// So this module tracks the one widget on the page, and switching means
// removing what the loader leaves behind.
let shown: { key: string; identified: boolean } | null = null;
let removal: ReturnType<typeof setTimeout> | undefined;

// Each step waits for the one before, so a sign-in can't overtake a sign-out
// and nothing reaches a widget that hasn't loaded yet.
let queue: Promise<unknown> = Promise.resolve();
const enqueue = (step: () => unknown) => {
  queue = queue.then(step);
};

function load(key: string) {
  return new Promise<void>((resolve) => {
    const script = document.createElement("script");
    script.src = SRC;
    script.async = true;
    script.dataset.key = key;
    // A blocked script only means no widget.
    script.onload = script.onerror = () => resolve();
    document.body.appendChild(script);
  });
}

async function unload(identified: boolean) {
  const host = document.getElementById(HOST_ID);
  if (host) host.style.display = "none";
  // Without this the session stays in the iframe's storage for the next person on this browser.
  if (identified) {
    window.ITillSupport?.("logout");
    await new Promise((resolve) => setTimeout(resolve, LOGOUT_MS));
  }
  host?.remove();
  document.querySelector(`script[src="${SRC}"]`)?.remove();
  delete window.ITillSupport;
  delete window.__ITillSupportLoaded;
}

/**
 * Support chat in the page corner. Shown only while mounted: pages that both
 * have it keep the same widget, so an open chat survives navigating between them.
 */
export function SupportWidget({
  widgetKey,
  userToken,
  context,
}: {
  widgetKey: string;
  userToken?: string;
  context?: Record<string, string | number | boolean>;
}) {
  useEffect(() => {
    clearTimeout(removal);
    if (shown?.key !== widgetKey) {
      if (shown) {
        const { identified } = shown;
        enqueue(() => unload(identified));
      }
      shown = { key: widgetKey, identified: false };
      enqueue(() => load(widgetKey));
    }
    if (userToken && !shown.identified) {
      shown.identified = true;
      enqueue(() => window.ITillSupport?.("identify", { userToken }));
    }
    if (context) enqueue(() => window.ITillSupport?.("context", context));
    // Deferred: on a navigation the next page's widget mounts right after this runs and cancels it.
    return () => {
      removal = setTimeout(() => {
        if (!shown) return;
        const { identified } = shown;
        shown = null;
        enqueue(() => unload(identified));
      }, 0);
    };
  }, [widgetKey, userToken, context]);
  return null;
}
