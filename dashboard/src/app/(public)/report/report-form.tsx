"use client";

import { useActionState, useState } from "react";
import { CheckCircle2 } from "lucide-react";
import { submitReportAction } from "@/app/actions/abuse";
import { REPORT_CATEGORIES } from "@/lib/abuse-shared";
import { Field, FormMessage, Input, Select, Textarea } from "@/components/ui";
import { SubmitButton } from "@/components/client-ui";
import { Turnstile } from "@/components/turnstile";

export function ReportForm({ host, placeholder, siteKey }: { host: string; placeholder: string; siteKey: string | null }) {
  const [state, action] = useActionState(submitReportAction, null);
  // Controlled, so a rejected submit doesn't wipe a long description.
  const [v, setV] = useState({ host, category: "", url: "", description: "", email: "" });
  const set = (k: keyof typeof v) => (e: { target: { value: string } }) => setV((p) => ({ ...p, [k]: e.target.value }));

  if (state?.ok) {
    return (
      <div role="status" className="rounded-lg border border-line bg-surface px-5 py-5">
        <p className="flex items-center gap-2 text-[16px] font-semibold text-ink">
          <CheckCircle2 size={18} className="text-ok" /> Thanks, we got your report
        </p>
        <p className="mt-1.5 text-[14px] leading-relaxed text-ink-2">
          The operators will look at <span className="font-mono text-[13px] text-ink">{state.ok}</span>.
          {v.email ? " If they need more details, they'll email you." : ""} You can close this page.
        </p>
      </div>
    );
  }

  return (
    <form action={action} className="flex flex-col gap-4">
      <Field label="Address of the tunnel" htmlFor="host" hint="The hostname in the browser's address bar.">
        <Input
          id="host"
          name="host"
          value={v.host}
          onChange={set("host")}
          placeholder={placeholder}
          required
          autoFocus={!host}
          spellCheck={false}
          autoCapitalize="off"
          className="font-mono text-[13px]"
        />
      </Field>
      <Field label="What's wrong?" htmlFor="category">
        <Select
          id="category"
          name="category"
          value={v.category}
          onValueChange={(category) => setV((prev) => ({ ...prev, category }))}
          required
          autoFocus={Boolean(host)}
          placeholder="Choose one"
          options={REPORT_CATEGORIES.map((c) => ({ value: c.value, label: c.label }))}
        />
      </Field>
      <Field label="Full URL" htmlFor="url" hint="Optional: the exact page, if it isn't the start page.">
        <Input
          id="url"
          name="url"
          type="url"
          value={v.url}
          onChange={set("url")}
          placeholder={`https://${v.host || placeholder}/…`}
          spellCheck={false}
          className="font-mono text-[13px]"
        />
      </Field>
      <Field label="What did you see?" htmlFor="description" hint="For example: which company the page pretends to be, where you found the link.">
        <Textarea
          id="description"
          name="description"
          value={v.description}
          onChange={set("description")}
          rows={5}
          required
          minLength={10}
          maxLength={5000}
        />
      </Field>
      <Field label="Your email" htmlFor="email" hint="Optional, only if you're fine with being asked for details.">
        <Input id="email" name="email" type="email" value={v.email} onChange={set("email")} autoComplete="email" />
      </Field>
      {siteKey ? <Turnstile siteKey={siteKey} reset={state} /> : null}
      <FormMessage state={state} />
      <SubmitButton className="self-start" pendingText="Sending…">
        Send report
      </SubmitButton>
    </form>
  );
}
