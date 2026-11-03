import type { AddressInfo } from "node:net";
import { join } from "node:path";
import { type ServerType, serve } from "@hono/node-server";
import {
  discardPurgeIntervalMs,
  discardRetentionDays,
  ensureMasterKey,
  ensureOwner,
  importEnvCredential,
  openDatabase,
  resolveDatabaseLocation,
} from "@luminwrite/db";
import type { WritingRuntime } from "@luminwrite/harness";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { type JobPort, openJobPort } from "@luminwrite/jobs";
import { createKnowledgeBase } from "@luminwrite/kb";
import { createClosedModels } from "@luminwrite/models";
import { createFeedService, safeFetch } from "@luminwrite/sources";

import { createApp } from "./app.js";
import { createOssAuth } from "./auth.js";
import { bindDocumentIndex, startDocumentIndex } from "./doc-index.js";
import { kbEmbedderFor } from "./kb-embed.js";
import { sessionKnowledge } from "./kb-session.js";
import { listenUrl, resolveListen } from "./listen.js";
import { ensureDefaultRegistry, resolveTurnChain, turnSpendFor } from "./model-registry.js";
import { writingNotesPort } from "./notes-port.js";

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
  let jobs: JobPort | undefined;
  try {
    const owner = await ensureOwner(db, { dataDir: location.dataDir });
    const secrets = await ensureMasterKey({ db, dataDir: location.dataDir, env });
    await importEnvCredential(db, secrets.ring, env, owner.userId);
    await ensureDefaultRegistry(db, owner.userId, env);
    const models = createClosedModels();
    const registry = { db, env, keyring: secrets.ring, models };
    const kb = createKnowledgeBase({
      db,
      embedder: kbEmbedderFor({ db, keyring: secrets.ring, env }),
    });
    jobs = await openJobPort(db.driver);
    const fetchPage = (url: string, options?: { headers?: Record<string, string> }) =>
      safeFetch(url, { ...options, allowPrivate: env.LW_ALLOW_PRIVATE_FETCH === "1" });
    const feeds = createFeedService({ db, kb, fetch: fetchPage, jobs });
    await feeds.start();
    bindDocumentIndex(db, jobs, kb);
    await startDocumentIndex(jobs, kb);
    await kb.rebuildSearchText();
    runtime = await createWritingRuntime({
      sqlitePath: join(location.dataDir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
      models,
      env,
      resolveTurn: (input) => resolveTurnChain(registry, input),
      turnSpend: (actor) => turnSpendFor(registry, actor),
      knowledge: sessionKnowledge(db, kb),
      loadEvidence: async (documentId) => {
        const document = await db.getDocument(documentId);
        if (!document) return [];
        return kb.listEvidence(document.ownerId, documentId);
      },
      notes: writingNotesPort(db),
    });
    const app = createApp({
      db,
      runtime,
      auth: createOssAuth(db),
      keyring: secrets.ring,
      env,
      models,
      kb,
      feeds,
      pageFetch: fetchPage,
    });
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
    if (!jobs) throw new Error("队列没有启动");
    const listening = server;
    const opened = runtime;
    const queue = jobs;
    const retentionDays = discardRetentionDays(env);
    const purgeEvery = discardPurgeIntervalMs(env);
    const purge = () => {
      void (async () => {
        if (!(await db.autoPurgeDiscarded(owner.userId))) return;
        const archived = await db.query<{ id: string; pi: string | null }>(
          `SELECT s.id, s.pi_conversation_id AS pi
           FROM sessions s
           LEFT JOIN documents d ON d.id = s.document_id
           WHERE s.pi_conversation_id IS NOT NULL
             AND (s.discarded_at IS NOT NULL OR d.discarded_at IS NOT NULL)`,
        );
        const piIds = archived.map((row) => row.pi).filter((id): id is string => Boolean(id));
        const work = opened.activePiWork
          ? await opened.activePiWork(piIds)
          : { busyConversationIds: [] as string[] };
        const busyPi = new Set(work.busyConversationIds);
        const protectSessionIds = archived
          .filter((row) => row.pi && busyPi.has(row.pi))
          .map((row) => row.id);
        const result = await db.purgeDiscarded({ retentionDays, protectSessionIds });
        const ids = result.purgedSessions
          .map((session) => session.piConversationId)
          .filter((id): id is string => Boolean(id));
        if (ids.length > 0) await opened.purgePiConversations?.(ids);
        if (result.purged.length > 0 || result.purgedSessions.length > 0) {
          await db.scrubFreedText();
        }
      })().catch(() => undefined);
    };
    purge();
    const purgeTimer = purgeEvery > 0 ? setInterval(purge, purgeEvery) : undefined;
    purgeTimer?.unref();
    const warning = [listen.warning, secrets.warning].filter((item) => item).join("\n");
    return {
      url: listenUrl(listen.host, address.port),
      ...(warning ? { warning } : {}),
      owner,
      async close() {
        try {
          if (purgeTimer) clearInterval(purgeTimer);
          await closeServer(listening);
          await opened.close();
          await queue.close();
        } finally {
          await db.close();
        }
      },
    };
  } catch (error) {
    if (server) server.close();
    await runtime?.close();
    await jobs?.close();
    await db.close();
    throw error;
  }
}
