import { getCurrentUser } from "@/lib/auth";
import { subscribe, type LiveEvent } from "@/lib/stream";

export const dynamic = "force-dynamic";

export async function GET(request: Request) {
  const user = await getCurrentUser();
  if (!user) return Response.json({ error: "Not signed in." }, { status: 401 });

  const encoder = new TextEncoder();
  let cleanup = () => {};

  const stream = new ReadableStream<Uint8Array>({
    async start(controller) {
      let closed = false;
      const send = (chunk: string) => {
        if (closed) return;
        try {
          controller.enqueue(encoder.encode(chunk));
        } catch {
          close();
        }
      };
      const onEvent = (e: LiveEvent) => send(`event: ${e.type}\ndata: ${JSON.stringify(e.data)}\n\n`);

      let unsubscribe = () => {};
      const heartbeat = setInterval(() => send(`: ping\n\n`), 15_000);
      const close = () => {
        if (closed) return;
        closed = true;
        clearInterval(heartbeat);
        unsubscribe();
        try {
          controller.close();
        } catch {
          /* already closed */
        }
      };
      cleanup = close;
      request.signal.addEventListener("abort", close);

      send(`retry: 3000\n: connected\n\n`);
      try {
        unsubscribe = await subscribe(user.id, onEvent);
        if (closed) unsubscribe();
      } catch (err) {
        console.error("tund: live stream unavailable", err);
        send(`event: error\ndata: ${JSON.stringify({ error: "Live updates are unavailable (database LISTEN failed)." })}\n\n`);
        close();
      }
    },
    cancel() {
      cleanup();
    },
  });

  return new Response(stream, {
    headers: {
      "Content-Type": "text/event-stream; charset=utf-8",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
