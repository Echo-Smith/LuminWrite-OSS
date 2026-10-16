import { openMemoryDatabase } from "@luminwrite/db";
import { describe, expect, it } from "vitest";

import { openJobPort } from "./index.js";

describe("pg-boss 适配器", () => {
  it("在内存 PGlite 上收发并登记 15 分钟调度", async () => {
    const db = await openMemoryDatabase();
    const jobs = await openJobPort(db.driver);
    try {
      let resolveSeen: (data: unknown) => void = () => undefined;
      const seen = new Promise<unknown>((resolve) => {
        resolveSeen = resolve;
      });
      await jobs.work("probe", async (data) => {
        resolveSeen(data);
      });
      const id = await jobs.send("probe", { n: 1 });
      expect(id).toEqual(expect.any(String));
      const delivered = await Promise.race([
        seen,
        new Promise<never>((_resolve, reject) => {
          setTimeout(() => reject(new Error("pg-boss 没有投递任务")), 8_000);
        }),
      ]);
      expect(delivered).toEqual({ n: 1 });
      await jobs.schedule("probe", "*/15 * * * *");
    } finally {
      await jobs.close();
      await db.close();
    }
  });
});
