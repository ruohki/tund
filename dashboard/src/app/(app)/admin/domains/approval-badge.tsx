import { Badge } from "@/components/ui";

export function ApprovalBadge({ approval }: { approval: string }) {
  if (approval === "pending") return <Badge tone="live">Pending review</Badge>;
  if (approval === "rejected") return <Badge tone="danger">Rejected</Badge>;
  return <Badge tone="ok">Approved</Badge>;
}
