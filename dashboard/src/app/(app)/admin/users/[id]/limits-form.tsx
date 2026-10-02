"use client";

import { useActionState, useState } from "react";
import { setUserLimitsAction } from "@/app/actions/admin";
import { cn, FormMessage, inputClass, Select } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import { formatLifetime } from "@/lib/format";

type Mode = "inherit" | "unlimited" | "custom";
const modeOf = (v: number | null): Mode => (v === null ? "inherit" : v === 0 ? "unlimited" : "custom");

function Row({
  name,
  label,
  unit,
  value,
  inheritLabel,
  hint,
}: {
  name: string;
  label: string;
  unit: string;
  value: number | null;
  inheritLabel: string;
  hint?: (n: number) => string;
}) {
  const [mode, setMode] = useState<Mode>(modeOf(value));
  const [n, setN] = useState(value && value > 0 ? String(value) : "");
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-[13.5px] font-medium text-ink">{label}</legend>
      <div className="flex flex-wrap items-center gap-2">
        <Select
          name={`${name}_mode`}
          value={mode}
          onValueChange={(v) => setMode(v as Mode)}
          aria-label={`${label}: how it's set`}
          className="sm:w-56"
          options={[
            { value: "inherit", label: `Default (${inheritLabel})` },
            { value: "unlimited", label: "Unlimited" },
            { value: "custom", label: "Custom value" },
          ]}
        />
        {mode === "custom" ? (
          <span className="flex items-center gap-2">
            <input
              name={name}
              value={n}
              onChange={(e) => setN(e.target.value)}
              inputMode="numeric"
              required
              aria-label={`${label} in ${unit}`}
              className={cn(inputClass.replace("w-full", ""), "w-32 tabular")}
            />
            <span className="text-[12.5px] text-muted">{unit}</span>
            {hint && Number(n) > 0 ? <span className="text-[12.5px] text-muted">= {hint(Number(n))}</span> : null}
          </span>
        ) : null}
      </div>
    </fieldset>
  );
}

export function LimitsForm({
  id,
  bandwidth,
  transfer,
  lifetime,
  defaultBandwidth,
  defaultTransfer,
  defaultLifetime,
}: {
  id: string;
  bandwidth: number | null;
  transfer: number | null;
  lifetime: number | null;
  defaultBandwidth: string;
  defaultTransfer: string;
  defaultLifetime: string;
}) {
  const [state, action] = useActionState(setUserLimitsAction, null);
  return (
    <form action={action} className="flex flex-col gap-4">
      <input type="hidden" name="id" value={id} />
      <Row
        name="bandwidth"
        label="Speed limit (each direction)"
        unit="kbit/s"
        value={bandwidth}
        inheritLabel={defaultBandwidth}
        hint={(k) => `${(k / 1000).toLocaleString("en", { maximumFractionDigits: 1 })} Mbit/s`}
      />
      <Row name="transfer" label="Transfer per month" unit="GB" value={transfer} inheritLabel={defaultTransfer} />
      <Row
        name="lifetime"
        label="Maximum tunnel lifetime"
        unit="minutes"
        value={lifetime}
        inheritLabel={defaultLifetime}
        hint={(m) => formatLifetime(m)}
      />
      <FormMessage state={state} />
      <div>
        <SubmitButton size="sm" pendingText="Saving…">
          Save limits
        </SubmitButton>
      </div>
    </form>
  );
}
