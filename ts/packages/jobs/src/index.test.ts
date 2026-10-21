import { describe, expect, it } from "vitest";

import { createInlineJobs, phase } from "./index.js";

describe("进程内队列", () => {
  it("已经接上，不再是空桩", () => {
    expect(phase).toBe("queue");
  });

  it("singletonKey 合并还没开始的任务，并保留最新数据", async () => {
    const jobs = createInlineJobs();
    const first = await jobs.send(
      "rss.fetch",
      { n: 1 },
      { singletonKey: "feed", startAfterSeconds: 5 },
    );
    const second = await jobs.send(
      "rss.fetch",
      { n: 2 },
      { singletonKey: "feed", startAfterSeconds: 5 },
    );
    expect(second).toBe(first);
    const seen: unknown[] = [];
    await jobs.work("rss.fetch", async (data) => {
      seen.push(data);
    });
    await jobs.advance(4_000);
    expect(seen).toEqual([]);
    await jobs.advance(1_000);
    expect(seen).toEqual([{ n: 2 }]);
    await jobs.close();
  });

  it("startAfterSeconds 到点才执行", async () => {
    const jobs = createInlineJobs();
    let ran = 0;
    await jobs.work("later", async () => {
      ran += 1;
    });
    await jobs.send("later", {}, { startAfterSeconds: 5 });
    expect(ran).toBe(0);
    await jobs.advance(4_999);
    expect(ran).toBe(0);
    await jobs.advance(1);
    expect(ran).toBe(1);
    await jobs.close();
  });

  it("*/15 在登记当时不触发，跨过下一档才触发", async () => {
    const jobs = createInlineJobs();
    const seen: unknown[] = [];
    await jobs.work("rss.fetch", async (data) => {
      seen.push(data);
    });
    await jobs.schedule("rss.fetch", "*/15 * * * *");
    await jobs.advance(14 * 60 * 1000);
    expect(seen).toEqual([]);
    await jobs.advance(60 * 1000);
    expect(seen).toEqual([{}]);
    await jobs.advance(15 * 60 * 1000);
    expect(seen).toHaveLength(2);
    await jobs.close();
  });
});
