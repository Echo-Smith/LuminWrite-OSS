import { describe, expect, it } from "vitest";

import { runDemo } from "./run-demo.js";

describe("演示", () => {
  it("不需要模型密钥就能走完建文档到定点修订", async () => {
    const result = await runDemo();
    expect(result.ok, result.log).toBe(true);
    expect(result.log).toContain("DEMO_OK");
    expect(result.log).toContain("逐字相同");
  });
});
