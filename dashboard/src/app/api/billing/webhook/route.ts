import type { NextRequest } from "next/server";
import type Stripe from "stripe";
import { stripeClient, syncSubscription } from "@/lib/billing";
import { decryptSecret, getSettings } from "@/lib/settings";

export const dynamic = "force-dynamic";

/**
 * Stripe webhooks (Admin → Billing sets up the endpoint): keeps the
 * subscriptions table in step with Stripe. Any dashboard node can take them.
 */
export async function POST(req: NextRequest) {
  const b = (await getSettings()).billing;
  const secret = decryptSecret(b.webhook_secret_enc);
  if (!b.secret_key_enc || !secret) return Response.json({ error: "billing is not set up" }, { status: 503 });
  const client = stripeClient(b);
  const body = await req.text();
  let event: Stripe.Event;
  try {
    event = client.webhooks.constructEvent(body, req.headers.get("stripe-signature") ?? "", secret);
  } catch {
    return Response.json({ error: "invalid signature" }, { status: 400 });
  }
  try {
    switch (event.type) {
      case "checkout.session.completed": {
        const s = event.data.object;
        if (s.mode === "subscription" && s.subscription) {
          await syncSubscription(typeof s.subscription === "string" ? s.subscription : s.subscription.id);
        }
        break;
      }
      case "customer.subscription.created":
      case "customer.subscription.updated":
      case "customer.subscription.deleted":
      case "customer.subscription.paused":
      case "customer.subscription.resumed":
        await syncSubscription(event.data.object);
        break;
    }
  } catch (err) {
    // Stripe retries on errors, which is what we want for a database hiccup.
    console.error("tund: billing webhook", event.type, err);
    return Response.json({ error: "processing failed" }, { status: 500 });
  }
  return Response.json({ received: true });
}
