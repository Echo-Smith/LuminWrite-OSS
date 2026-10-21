import type { AddressInfo } from "node:net";
import { join } from "node:path";
import { type ServerType, serve } from "@hono/node-server";
import { ensureOwner, openDatabase, resolveDatabaseLocation } from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { listenUrl, resolveListen } from "./listen.js";

export interface RunningServer {
  url: string;
  warning?: string;
  owner: { userId: string; created: boolean; tokenPath: string };
  close(): Promise<void>;
}

function closeServer(server: ServerType): Promise<void> {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

/** 打开数据库（默认 PGlite）、确保所有者，并开始监听。 */
export async function startServer(env: NodeJS.ProcessEnv = process.env): Promise<RunningServer> {
  const location = resolveDatabaseLocation(env);
  const listen = resolveListen(env);
  const db = await openDatabase(location);
  let runtime: WritingRuntime | undefined;
  let server: ServerType | undefined;
  try {
    const owner = await ensureOwner(db, { dataDir: location.dataDir });
    runtime = await createWritingRuntime({
      sqlitePath: join(location.dataDir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
    });
    const app = createApp({ db, runtime, auth: createOssAuth(db) });
    const address = await new Promise<AddressInfo>((resolve, reject) => {
      const listening = serve(
        {
          fetch: app.fetch,
          hostname: listen.host,
          port: listen.port,
        },
        (info) => resolve(info),
      );
      server = listening;
      listening.on("error", reject);
    });
    if (!server) throw new Error("没有拿到监听地址");
    const listening = server;
    const opened = runtime;
    return {
      url: listenUrl(listen.host, address.port),
      ...(listen.warning ? { warning: listen.warning } : {}),
      owner,
      async close() {
        await closeServer(listening);
        await opened.close();
        await db.close();
      },
    };
  } catch (error) {
    if (server) server.close();
    await runtime?.close();
    await db.close();
    throw error;
  }
}
