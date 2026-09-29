"use client";

import { useActionState, useState } from "react";
import { removeSmtpAction, saveSmtpAction, sendTestEmailAction } from "@/app/actions/admin";
import { buttonClass, cn, Field, FormMessage, inputClass } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";

type Current = {
  host: string;
  port: number;
  security: "starttls" | "tls" | "none";
  username: string;
  hasPassword: boolean;
  passwordBroken: boolean;
  from_email: string;
  from_name: string;
} | null;

export function SmtpForm({ current, defaultFromName }: { current: Current; defaultFromName: string }) {
  const [state, action] = useActionState(saveSmtpAction, null);
  const [security, setSecurity] = useState(current?.security ?? "starttls");
  const [port, setPort] = useState(String(current?.port ?? 587));
  return (
    <form action={action} className="flex flex-col gap-3.5">
      <div className="grid gap-3.5 sm:grid-cols-[minmax(0,1fr)_7rem]">
        <Field label="Host" htmlFor="smtp-host">
          <input id="smtp-host" name="host" required defaultValue={current?.host} placeholder="smtp.example.com" className={inputClass} />
        </Field>
        <Field label="Port" htmlFor="smtp-port">
          <input
            id="smtp-port"
            name="port"
            required
            inputMode="numeric"
            value={port}
            onChange={(e) => setPort(e.target.value)}
            className={cn(inputClass, "tabular")}
          />
        </Field>
      </div>
      <Field
        label="Connection security"
        htmlFor="smtp-security"
        hint="STARTTLS is the usual choice on port 587, TLS on port 465. Unencrypted only for a relay on the same host or network."
      >
        <select
          id="smtp-security"
          name="security"
          value={security}
          onChange={(e) => {
            const v = e.target.value as typeof security;
            setSecurity(v);
            // Suggest the port that goes with the choice when the old one was the other default.
            if (v === "tls" && port === "587") setPort("465");
            if (v === "starttls" && port === "465") setPort("587");
          }}
          className={cn(inputClass, "sm:w-72")}
        >
          <option value="starttls">STARTTLS (upgrade the connection)</option>
          <option value="tls">TLS from the start (SMTPS)</option>
          <option value="none">None (unencrypted)</option>
        </select>
      </Field>
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field label="Username" htmlFor="smtp-user" hint="Leave empty if the server doesn't require login.">
          <input id="smtp-user" name="username" defaultValue={current?.username} autoComplete="off" className={inputClass} />
        </Field>
        <Field
          label="Password"
          htmlFor="smtp-pass"
          hint={
            current?.passwordBroken
              ? "The stored password can't be decrypted anymore. Enter it again."
              : current?.hasPassword
                ? "Stored encrypted and never shown. Leave empty to keep it."
                : "Stored encrypted and never shown again."
          }
        >
          <input
            id="smtp-pass"
            name="password"
            type="password"
            autoComplete="new-password"
            placeholder={current?.hasPassword ? "unchanged" : ""}
            className={inputClass}
          />
        </Field>
      </div>
      {current?.hasPassword ? (
        <label className="flex items-center gap-2 text-[13px] text-ink-2">
          <input type="checkbox" name="clear_password" className="accent-[var(--ink)]" />
          Remove the stored password
        </label>
      ) : null}
      <div className="grid gap-3.5 sm:grid-cols-2">
        <Field label="From address" htmlFor="smtp-from">
          <input
            id="smtp-from"
            name="from_email"
            type="email"
            required
            defaultValue={current?.from_email}
            placeholder="tund@example.com"
            className={inputClass}
          />
        </Field>
        <Field label="From name" htmlFor="smtp-from-name">
          <input id="smtp-from-name" name="from_name" defaultValue={current?.from_name ?? defaultFromName} className={inputClass} />
        </Field>
      </div>
      <FormMessage state={state} />
      <div className="flex flex-wrap items-center gap-2">
        <SubmitButton pendingText="Saving…">Save email settings</SubmitButton>
        {current ? <RemoveSmtpButton /> : null}
      </div>
    </form>
  );
}

/** Two-step remove that submits the SMTP form to a different action (and skips its validation). */
function RemoveSmtpButton() {
  const [armed, setArmed] = useState(false);
  return armed ? (
    <button type="submit" formAction={removeSmtpAction} formNoValidate className={buttonClass("danger")}>
      Remove SMTP settings?
    </button>
  ) : (
    <button type="button" onClick={() => setArmed(true)} className={buttonClass("ghost")}>
      Remove
    </button>
  );
}

export function TestEmailForm({ disabled }: { disabled: boolean }) {
  const [state, action, pending] = useActionState(sendTestEmailAction, null);
  // Controlled, so the address survives React's form reset for another try.
  const [to, setTo] = useState("");
  return (
    <form action={action} className="flex flex-col gap-3">
      <Field label="Send to" htmlFor="test-to">
        <input
          id="test-to"
          name="to"
          type="email"
          required
          value={to}
          onChange={(e) => setTo(e.target.value)}
          placeholder="you@example.com"
          className={inputClass}
          disabled={disabled}
        />
      </Field>
      <div>
        <SubmitButton variant="secondary" pendingText="Sending…" disabled={disabled}>
          Send test email
        </SubmitButton>
      </div>
      {pending ? null : state?.error ? (
        <div role="alert" className="rounded-md border border-danger/30 bg-danger-wash px-3 py-2">
          <p className="text-[12.5px] font-medium text-danger">The SMTP server said:</p>
          <pre className="mt-1 whitespace-pre-wrap break-all font-mono text-[12px] text-danger">{state.error}</pre>
        </div>
      ) : (
        <FormMessage state={pending ? null : state} />
      )}
      {disabled ? <p className="text-[12.5px] text-muted">Save SMTP settings first.</p> : null}
    </form>
  );
}

type Template = { id: string; label: string; subject: string; html: string; text: string };

export function TemplatePreviews({ templates }: { templates: Template[] }) {
  const [active, setActive] = useState(templates[0].id);
  const [asText, setAsText] = useState(false);
  const t = templates.find((x) => x.id === active)!;
  return (
    <div>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2">
        <div role="tablist" className="flex flex-wrap gap-1">
          {templates.map((x) => (
            <button
              key={x.id}
              type="button"
              role="tab"
              aria-selected={x.id === active}
              onClick={() => setActive(x.id)}
              className={cn(
                "rounded-[4px] px-2 py-1 text-[12.5px]",
                x.id === active ? "bg-surface-3 font-medium text-ink" : "text-muted hover:text-ink",
              )}
            >
              {x.label}
            </button>
          ))}
        </div>
        <button type="button" onClick={() => setAsText((v) => !v)} className="text-[12.5px] text-muted hover:text-ink">
          {asText ? "Show HTML" : "Show plain text"}
        </button>
      </div>
      <p className="border-b border-line px-4 py-2 text-[13px]">
        <span className="text-muted">Subject: </span>
        <span className="text-ink">{t.subject}</span>
      </p>
      {asText ? (
        <pre className="max-h-[520px] overflow-auto scroll-thin whitespace-pre-wrap p-4 font-mono text-[12px] leading-5 text-ink-2">{t.text}</pre>
      ) : (
        // Sandboxed: previews can't run scripts or reach the dashboard.
        <iframe title={`${t.label} preview`} srcDoc={t.html} sandbox="" className="h-[520px] w-full bg-white" />
      )}
    </div>
  );
}
