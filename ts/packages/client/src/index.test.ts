import { describe, expect, it } from "vitest";

import { createClient } from "./index.js";

describe("客户端", () => {
  it("把健康检查发给给定的 fetch", async () => {
    const client = createClient(
      "http://127.0.0.1:8787",
      async () => new Response(JSON.stringify({ ok: true })),
    );
    expect(await client.health()).toEqual({ ok: true });
  });
});
