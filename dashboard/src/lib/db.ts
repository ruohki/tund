import "server-only";
import postgres from "postgres";

type Sql = ReturnType<typeof postgres>;

const globalForDb = globalThis as unknown as { __tundSql?: Sql };

function create(): Sql {
  const url = process.env.TUND_DATABASE_URL;
  if (!url) {
    throw new Error("TUND_DATABASE_URL is not set. Point it at the Postgres database shared with tund-server.");
  }
  // postgres.js checks target_session_attrs=read-write with
  // default_transaction_read_only, which is off on a Patroni replica too, and
  // it spreads connections over all hosts of a multi-host URL: writes (and
  // LISTEN, which replicas never deliver) would land on replicas. "primary"
  // is checked with in_hot_standby, which is right.
  const tsa = /[?&]target_session_attrs=([^&]+)/.exec(url)?.[1];
  return postgres(url, {
    ...(tsa === "read-write" ? { target_session_attrs: "primary" as const } : {}),
    max: 10,
    idle_timeout: 30,
    connect_timeout: 10,
    onnotice: () => {},
    types: {
      // bigint columns (sizes, counts) fit comfortably in a JS number.
      bigint: {
        to: 20,
        from: [20],
        serialize: (x: number) => String(x),
        parse: (x: string) => Number(x),
      },
    },
  });
}

export function db(): Sql {
  if (!globalForDb.__tundSql) globalForDb.__tundSql = create();
  return globalForDb.__tundSql;
}

/** Send a Postgres notification (tund-server listens on tund_config). */
export async function notify(channel: string, payload: Record<string, unknown>) {
  await db()`select pg_notify(${channel}, ${JSON.stringify(payload)})`;
}
