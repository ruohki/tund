"use client";

import { useActionState } from "react";
import { Dices } from "lucide-react";
import { releaseTcpAction, reserveTcpAction } from "@/app/actions/domains";
import { cn, FormMessage, inputClass } from "@/components/ui";
import { Command, ConfirmSubmit, CopyButton, SubmitButton } from "@/components/client-ui";

export type TcpPortItem = { port: number; online: boolean; createdAt: string };

function TeamField({ teamId }: { teamId?: string | null }) {
  return teamId ? <input type="hidden" name="team_id" value={teamId} /> : null;
}

/** Reserve a random free port or a specific one in the server's range. */
export function ReserveTcpForm({
  range,
  full,
  teamId,
}: {
  range: { from: number; to: number };
  full: string | null;
  teamId?: string | null;
}) {
  const [picked, pickAction] = useActionState(reserveTcpAction, null);
  const [random, randomAction] = useActionState(reserveTcpAction, null);
  if (full) return <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[13px] text-ink-2">{full}</p>;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <form action={randomAction}>
          <TeamField teamId={teamId} />
          <SubmitButton variant="secondary" pendingText="Reserving…">
            <Dices size={14} /> Reserve a random port
          </SubmitButton>
        </form>
        <span className="text-[12.5px] text-muted">or</span>
        <form action={pickAction} className="flex items-center gap-2">
          <TeamField teamId={teamId} />
          <input
            name="port"
            required
            inputMode="numeric"
            aria-label="Port"
            placeholder={String(range.from)}
            className={cn(inputClass.replace("w-full", ""), "w-28 font-mono text-[13px] tabular")}
          />
          <SubmitButton pendingText="Reserving…">Reserve this port</SubmitButton>
        </form>
        <span className="text-[12px] text-muted tabular">
          Range {range.from}–{range.to}
        </span>
      </div>
      <FormMessage state={picked ?? random} />
    </div>
  );
}

export function TcpPortRow({
  item,
  host,
  teamId,
  canManage = true,
}: {
  item: TcpPortItem;
  host: string;
  teamId?: string | null;
  canManage?: boolean;
}) {
  const address = `tcp://${host}:${item.port}`;
  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="flex min-w-0 items-center gap-2">
          {item.online ? <span className="live-dot shrink-0" title="A tunnel is online on this port" /> : null}
          <span className="truncate font-mono text-[13.5px] font-medium text-ink">{address}</span>
          <CopyButton value={address} />
        </span>
        {canManage ? (
          <form action={releaseTcpAction}>
            <TeamField teamId={teamId} />
            <input type="hidden" name="port" value={item.port} />
            <ConfirmSubmit variant="ghost" confirmText="Release this port?">
              Release
            </ConfirmSubmit>
          </form>
        ) : null}
      </div>
      <Command className="mt-2.5 max-w-xl">{`tund tcp 22 --remote-port ${item.port}`}</Command>
    </li>
  );
}
