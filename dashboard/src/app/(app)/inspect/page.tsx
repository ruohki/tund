import type { Metadata } from "next";
import { requireUser } from "@/lib/auth";
import { isUuid, requestHostnames } from "@/lib/requests";
import { InspectView } from "@/components/inspect/inspect-view";
import { connectionAddresses } from "@/lib/connections";

export const metadata: Metadata = { title: "Inspect" };

export default async function InspectPage({ searchParams }: PageProps<"/inspect">) {
  const user = await requireUser();
  const { host, id, view } = await searchParams;
  const [hostnames, addresses] = await Promise.all([requestHostnames(user.id), connectionAddresses(user.id)]);
  return (
    <div className="-mx-2 -my-2 sm:-mx-4 sm:-my-3">
      <div className="mb-3 flex items-baseline justify-between gap-4 px-2">
        <h1 className="text-[22px] font-semibold tracking-[-0.015em] text-ink">Inspect</h1>
        <p className="hidden text-[13px] text-muted sm:block">Every request and connection through your tunnels, as it happens.</p>
      </div>
      <InspectView
        hostnames={hostnames.filter((h) => !addresses.includes(h))}
        addresses={addresses}
        initialView={view === "connections" ? "connections" : "requests"}
        initialHost={typeof host === "string" ? host.toLowerCase() : ""}
        initialId={isUuid(id) ? id : ""}
      />
    </div>
  );
}
