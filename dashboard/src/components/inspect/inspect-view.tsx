"use client";

import { useState } from "react";
import { Inspector } from "./inspector";
import { ConnectionsView } from "./connections-view";

/** HTTP requests or TCP/TLS connections; the tunnel filter switches between them. */
export function InspectView({
  hostnames,
  addresses,
  initialView,
  initialHost,
  initialId,
  initialCompare,
}: {
  hostnames: string[];
  addresses: string[];
  initialView: "requests" | "connections";
  initialHost: string;
  initialId: string;
  /** With initialId: open the comparison of the two requests. */
  initialCompare: string;
}) {
  const [state, setState] = useState({ view: initialView, host: initialHost });
  return state.view === "connections" ? (
    <ConnectionsView
      key={`c:${state.host}`}
      hostnames={hostnames}
      addresses={addresses}
      initialAddress={state.host}
      onSwitch={(host) => setState({ view: "requests", host })}
    />
  ) : (
    <Inspector
      key={`h:${state.host}`}
      hostnames={hostnames}
      addresses={addresses}
      initialHost={state.host}
      initialId={state.host === initialHost ? initialId : ""}
      initialCompare={state.host === initialHost ? initialCompare : ""}
      onSwitch={(host) => setState({ view: "connections", host })}
    />
  );
}
