import type { Metadata } from "next";
import { requireUser } from "@/lib/auth";
import { normalizeUserCode, type DeviceOutcome } from "@/lib/device";
import { lookupDeviceCode } from "@/lib/device-db";
import { unverifiedError } from "@/lib/mail";
import { DeviceFlow } from "./device-flow";

export const metadata: Metadata = { title: "Log in your terminal" };

export default async function DevicePage({ searchParams }: PageProps<"/device">) {
  const user = await requireUser();
  const raw = (await searchParams).code;
  const input = typeof raw === "string" ? raw.slice(0, 32) : "";
  const code = input ? normalizeUserCode(input) : null;
  const outcome: DeviceOutcome | null = code ? await lookupDeviceCode(user, code) : null;
  const blocked = await unverifiedError(user);
  return <DeviceFlow key={code ?? input} email={user.email} input={input} code={code} initial={outcome} blocked={blocked} />;
}
