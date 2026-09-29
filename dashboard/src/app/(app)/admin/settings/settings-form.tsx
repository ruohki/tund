"use client";

import { useActionState, useState } from "react";
import { RotateCcw } from "lucide-react";
import { resetSettingAction, saveSettingsAction } from "@/app/actions/admin";
import { cn, FormMessage, inputClass, Panel } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

export type FieldView = {
  key: string;
  group: string;
  label: string;
  help: string;
  kind: "bool" | "int" | "string" | "signup" | "enum" | "list" | "secret" | "markdown";
  min?: number;
  max?: number;
  options?: [string, string][];
  isSet?: boolean;
  value: string | number | boolean;
  overridden: boolean;
  defaultLabel: string;
};

const GROUPS = ["Sign-up & accounts", "Limits", "Abuse protection", "Traffic capture & retention", "Branding", "Legal pages"];

/** kbit/s with the same value in Mbit/s next to it. */
function KbpsInput({ f, id }: { f: FieldView; id: string }) {
  const [v, setV] = useState(String(f.value));
  const n = Number(v);
  return (
    <span className="flex items-center gap-2">
      <input
        id={id}
        name={f.key}
        type="number"
        inputMode="numeric"
        min={f.min}
        max={f.max}
        value={v}
        onChange={(e) => setV(e.target.value)}
        className={cn(inputClass, "tabular sm:w-40")}
      />
      <span className="text-[12.5px] text-muted tabular">
        {n > 0 ? `= ${(n / 1000).toLocaleString("en", { maximumFractionDigits: 2 })} Mbit/s` : "unlimited"}
      </span>
    </span>
  );
}

function Control({ f }: { f: FieldView }) {
  const id = `set-${f.key}`;
  switch (f.kind) {
    case "bool":
      return (
        <input
          id={id}
          name={f.key}
          type="checkbox"
          defaultChecked={Boolean(f.value)}
          className="h-4 w-4 accent-[var(--ink)]"
        />
      );
    case "signup":
      return (
        <select id={id} name={f.key} defaultValue={String(f.value)} className={cn(inputClass, "sm:w-60")}>
          <option value="open">Anyone (open sign-up)</option>
          <option value="invite">Only with a team invite</option>
          <option value="closed">Nobody (admins create accounts)</option>
        </select>
      );
    case "int":
      return f.key === "limit_bandwidth_kbps" ? (
        <KbpsInput f={f} id={id} />
      ) : (
        <input
          id={id}
          name={f.key}
          type="number"
          inputMode="numeric"
          min={f.min}
          max={f.max}
          defaultValue={Number(f.value)}
          className={cn(inputClass, "tabular sm:w-40")}
        />
      );
    case "enum":
      return (
        <select id={id} name={f.key} defaultValue={String(f.value)} className={cn(inputClass, "sm:w-72")}>
          {f.options?.map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      );
    case "list":
      return (
        <textarea
          id={id}
          name={f.key}
          defaultValue={String(f.value)}
          rows={6}
          spellCheck={false}
          className={cn(inputClass, "h-auto py-2 font-mono text-[12.5px] leading-5")}
        />
      );
    case "markdown":
      return (
        <textarea
          id={id}
          name={f.key}
          defaultValue={String(f.value)}
          rows={8}
          placeholder="Empty: the built-in text is used."
          className={cn(inputClass, "h-auto py-2 font-mono text-[12.5px] leading-5")}
        />
      );
    case "secret":
      return (
        <span className="flex flex-col gap-1.5">
          <input
            id={id}
            name={f.key}
            type="password"
            autoComplete="off"
            placeholder={f.isSet ? "unchanged" : "not set"}
            className={cn(inputClass, "sm:w-80")}
          />
          {f.isSet ? (
            <label className="flex items-center gap-2 text-[12.5px] text-ink-2">
              <input type="checkbox" name={`clear:${f.key}`} className="accent-[var(--ink)]" /> Remove the stored key
            </label>
          ) : null}
        </span>
      );
    default:
      return <input id={id} name={f.key} defaultValue={String(f.value)} className={cn(inputClass, "sm:w-80")} />;
  }
}

export function SettingsForm({ fields }: { fields: FieldView[] }) {
  const [state, action] = useActionState(saveSettingsAction, null);
  return (
    <form action={action} className="flex flex-col gap-6">
      {GROUPS.map((g) => (
        <Panel key={g} id={g.toLowerCase().replace(/[^a-z0-9]+/g, "-")} title={g}>
          <ul className="divide-y divide-line">
            {fields
              .filter((f) => f.group === g)
              .map((f) => (
                <li key={f.key} className="grid gap-3 px-4 py-3.5 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] md:items-start md:gap-6">
                  <input type="hidden" name={`present:${f.key}`} value="1" />
                  <div className="min-w-0">
                    <label htmlFor={`set-${f.key}`} className="text-[13.5px] font-medium text-ink">
                      {f.label}
                    </label>
                    <p className="mt-0.5 text-[12.5px] text-muted">{f.help}</p>
                  </div>
                  <div className="flex flex-col gap-1.5">
                    <Control f={f} />
                    <p className="flex flex-wrap items-center gap-x-2 text-[12px] text-muted">
                      <span>Default: {f.defaultLabel}</span>
                      {f.overridden ? (
                        <button
                          type="submit"
                          formAction={resetSettingAction.bind(null, f.key)}
                          formNoValidate
                          className="inline-flex items-center gap-1 font-medium text-ink-2 underline-offset-4 hover:text-ink hover:underline"
                        >
                          <RotateCcw size={11} /> Reset to default
                        </button>
                      ) : (
                        <span>(using it)</span>
                      )}
                    </p>
                  </div>
                </li>
              ))}
          </ul>
        </Panel>
      ))}
      <div className="sticky bottom-0 -mx-1 flex flex-wrap items-center gap-3 rounded-md border border-line bg-surface px-4 py-3 shadow-pop">
        <SubmitButton pendingText="Saving…">Save settings</SubmitButton>
        <div className="min-w-0 flex-1">
          <FormMessage state={state} />
        </div>
        <p className="text-[12px] text-muted">Changes apply right away; no restart needed.</p>
      </div>
    </form>
  );
}
