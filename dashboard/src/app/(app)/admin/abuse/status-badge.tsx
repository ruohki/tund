import { Badge } from "@/components/ui";

export function StatusBadge({ status }: { status: string }) {
  if (status === "open") return <Badge tone="live">Open</Badge>;
  if (status === "resolved") return <Badge tone="ok">Resolved</Badge>;
  return <Badge>Dismissed</Badge>;
}
