import type { Metadata } from "next";
import { envDefaultLabel, getSettings, SETTING_DEFS, storedKeys } from "@/lib/settings";
import { smtpConfigured } from "@/lib/mail";
import { SettingsForm, type FieldView } from "./settings-form";

export const metadata: Metadata = { title: "Settings" };

export default async function AdminSettingsPage() {
  const [s, stored, mailOn] = await Promise.all([getSettings(), storedKeys(), smtpConfigured()]);
  const fields: FieldView[] = SETTING_DEFS.map((d) => {
    const v = s[d.key];
    return {
      key: d.key,
      group: d.group,
      label: d.label,
      help: d.key === "require_email_verification" && !mailOn ? `${d.help} Email isn't configured, so this is off for now.` : d.help,
      kind: d.kind,
      min: d.min,
      max: d.max,
      options: d.options,
      // Secrets never go back to the browser; the form only learns whether one is set.
      value: d.kind === "secret" ? "" : Array.isArray(v) ? v.join("\n") : (v as string | number | boolean),
      isSet: d.kind === "secret" ? Boolean(v) : undefined,
      overridden: stored.has(d.key),
      defaultLabel: envDefaultLabel(d),
    };
  });
  return <SettingsForm fields={fields} />;
}
