import { PGlite } from "@electric-sql/pglite";
import { vector } from "@electric-sql/pglite-pgvector";
import { fromPglite, PgBoss } from "pg-boss";
import { describe, expect, it } from "vitest";

import { migrationSql } from "./migrate.js";

describe("PGlite 能力", () => {
  it("pgvector 能加载，pg-boss 能在同一个库里收发任务", async () => {
    const db = new PGlite({ extensions: { vector } });
    const boss = new PgBoss({ db: fromPglite(db), backend: "pglite" });
    const failures: unknown[] = [];
    boss.on("error", (error) => {
      failures.push(error);
    });
    try {
      await db.exec(migrationSql);
      await db.exec(`
        CREATE EXTENSION IF NOT EXISTS vector;
        CREATE TABLE probe_vec (id int, embedding vector(1024));
        INSERT INTO probe_vec VALUES (1, '[${Array.from({ length: 1024 }, (_, index) => (index === 0 ? 1 : 0)).join(",")}]');
        CREATE INDEX probe_vec_hnsw ON probe_vec USING hnsw (embedding vector_cosine_ops);
      `);
      const nearest = await db.query<{ id: number }>(
        `SELECT id FROM probe_vec ORDER BY embedding <=> '[${Array.from({ length: 1024 }, (_, index) => (index === 0 ? 1 : 0)).join(",")}]' LIMIT 1`,
      );
      expect(nearest.rows[0]?.id).toBe(1);

      await boss.start();
      await boss.createQueue("probe");
      const sent = await boss.send("probe", { n: 1 });
      expect(sent).toEqual(expect.any(String));
      const seen = await new Promise<unknown>((resolve, reject) => {
        const timer = setTimeout(() => reject(new Error("pg-boss 没有投递任务")), 8000);
        void boss
          .work("probe", async (jobs) => {
            clearTimeout(timer);
            resolve(jobs);
          })
          .catch(reject);
      });
      expect(seen).toMatchObject([{ id: sent, data: { n: 1 } }]);
      await boss.schedule("probe", "*/15 * * * *", { tick: true });
      expect(await boss.getSchedules("probe")).toMatchObject([
        { name: "probe", cron: "*/15 * * * *" },
      ]);
      expect(failures).toEqual([]);
    } finally {
      await boss.stop({ graceful: false }).catch(() => undefined);
      await db.close();
    }
  });
});
