import type { Metadata } from "next";
import { requireUser } from "@/lib/auth";
import { DeviceApproved } from "../device-flow";

export const metadata: Metadata = { title: "Terminal logged in" };

/** Where the terminal sends the browser after a callback login (`tund login` on this computer). */
export default async function DeviceDonePage() {
  const user = await requireUser();
  return <DeviceApproved email={user.email} />;
}
