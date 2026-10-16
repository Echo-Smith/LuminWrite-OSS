import type { DatabaseDriver } from "@luminwrite/db";
import { fromPglite, PgBoss } from "pg-boss";

import type { JobPort, JobSendOptions } from "./port.js";

function asObject(data: unknown): object {
  if (data && typeof data === "object" && !Array.isArray(data)) return data;
  return { value: data ?? null };
}

/** 运行时队列。PGlite 用 fromPglite，Postgres 用连接串。 */
export async function openJobPort(driver: DatabaseDriver): Promise<JobPort> {
  if (driver.kind === "postgres" && !driver.connectionString) {
    throw new Error("Postgres 缺少连接串");
  }
  const boss =
    driver.kind === "pglite"
      ? new PgBoss({ db: fromPglite(driver.client), backend: "pglite" })
      : new PgBoss({ connectionString: driver.connectionString });
  boss.on("error", () => undefined);
  await boss.start();
  const queues = new Set<string>();

  async function ensure(name: string): Promise<void> {
    if (queues.has(name)) return;
    try {
      await boss.createQueue(name);
    } catch (error) {
      const message = error instanceof Error ? error.message : "";
      if (!/exist/i.test(message)) throw error;
    }
    queues.add(name);
  }

  return {
    async send(name, data, opts?: JobSendOptions) {
      await ensure(name);
      const payload = asObject(data);
      const options = {
        ...(opts?.singletonKey ? { singletonKey: opts.singletonKey } : {}),
        ...(opts?.startAfterSeconds != null ? { startAfter: opts.startAfterSeconds } : {}),
      };
      const sent = await boss.send(name, payload, options);
      if (sent) return sent;
      if (opts?.singletonKey) {
        // 不带 startAfter，已排队任务保持原来的到期时间。
        const updated = await boss.update(name, payload, {
          singletonKey: opts.singletonKey,
        });
        const existing = updated.jobs[0];
        if (existing) return existing;
      }
      throw new Error("任务没有入队");
    },
    async work(name, handler) {
      await ensure(name);
      await boss.work(name, async (jobs) => {
        for (const job of jobs) await handler(job.data);
      });
    },
    async schedule(name, cron) {
      await ensure(name);
      await boss.schedule(name, cron, {});
    },
    async close() {
      await boss.stop({ graceful: false });
    },
  };
}
