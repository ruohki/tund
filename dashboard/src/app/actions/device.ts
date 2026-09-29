"use server";

import { requireUser } from "@/lib/auth";
import { unverifiedError } from "@/lib/mail";
import { decideDeviceCode } from "@/lib/device-db";
import type { DeviceOutcome } from "@/lib/device";

export async function approveDeviceAction(code: string): Promise<DeviceOutcome> {
  const user = await requireUser();
  const blocked = await unverifiedError(user);
  if (blocked) return { state: "error", message: blocked };
  return decideDeviceCode(user, code, true);
}

export async function denyDeviceAction(code: string): Promise<DeviceOutcome> {
  return decideDeviceCode(await requireUser(), code, false);
}
