import { MockAgent } from "undici";
import { describe, expect, it } from "vitest";

import { type LookupFn, safeFetch } from "./safe-fetch.js";

const publicLookup: LookupFn = async () => [{ address: "1.1.1.1", family: 4 }];

describe("safeFetch", () => {
  it("拒绝私网、环回、链路本地，以及解析结果里混进的内网地址", async () => {
    await expect(safeFetch("http://127.0.0.1/secret", { lookup: publicLookup })).rejects.toThrow(
      "地址被拒绝",
    );
    await expect(safeFetch("http://10.1.2.3/secret", { lookup: publicLookup })).rejects.toThrow(
      "地址被拒绝",
    );
    await expect(safeFetch("http://169.254.169.254/", { lookup: publicLookup })).rejects.toThrow(
      "地址被拒绝",
    );
    await expect(safeFetch("http://[::1]/", { lookup: publicLookup })).rejects.toThrow(
      "地址被拒绝",
    );
    await expect(
      safeFetch("http://evil.test/", {
        lookup: async () => [
          { address: "1.1.1.1", family: 4 },
          { address: "::ffff:127.0.0.1", family: 6 },
        ],
      }),
    ).rejects.toThrow("地址被拒绝");
    await expect(safeFetch("file:///etc/passwd", { lookup: publicLookup })).rejects.toThrow(
      "只接受 http 和 https",
    );
  });

  it("公网地址能读到正文，重定向到环回会被拦住", async () => {
    const agent = new MockAgent();
    agent.disableNetConnect();
    agent
      .get("http://public.test")
      .intercept({ path: "/ok", method: "GET" })
      .reply(200, "hello", { headers: { etag: "v1", "content-type": "text/plain" } });
    agent
      .get("http://public.test")
      .intercept({ path: "/go", method: "GET" })
      .reply(302, "", { headers: { location: "http://127.0.0.1/secret" } });
    try {
      const ok = await safeFetch("http://public.test/ok", {
        lookup: publicLookup,
        dispatcher: agent,
      });
      expect(ok.status).toBe(200);
      expect(ok.body).toBe("hello");
      expect(ok.etag).toBe("v1");
      await expect(
        safeFetch("http://public.test/go", { lookup: publicLookup, dispatcher: agent }),
      ).rejects.toThrow("地址被拒绝");
    } finally {
      await agent.close();
    }
  });

  it("显式允许时可以访问环回", async () => {
    const agent = new MockAgent();
    agent.disableNetConnect();
    agent.get("http://127.0.0.1").intercept({ path: "/ok" }).reply(200, "local");
    try {
      const result = await safeFetch("http://127.0.0.1/ok", {
        allowPrivate: true,
        lookup: publicLookup,
        dispatcher: agent,
      });
      expect(result.body).toBe("local");
    } finally {
      await agent.close();
    }
  });

  it("304 不带正文，超限和超时会失败", async () => {
    const agent = new MockAgent();
    agent.disableNetConnect();
    agent
      .get("http://public.test")
      .intercept({ path: "/feed" })
      .reply(304, "ignored", {
        headers: { etag: "v1" },
      });
    agent.get("http://public.test").intercept({ path: "/big" }).reply(200, "hello");
    agent.get("http://public.test").intercept({ path: "/slow" }).reply(200, "late").delay(300);
    try {
      const notModified = await safeFetch("http://public.test/feed", {
        lookup: publicLookup,
        dispatcher: agent,
        headers: { "If-None-Match": "v1" },
      });
      expect(notModified.status).toBe(304);
      expect(notModified.body).toBeNull();
      await expect(
        safeFetch("http://public.test/big", {
          lookup: publicLookup,
          dispatcher: agent,
          maxBytes: 4,
        }),
      ).rejects.toThrow("响应超过 8MB");
      await expect(
        safeFetch("http://public.test/slow", {
          lookup: publicLookup,
          dispatcher: agent,
          timeoutMs: 20,
        }),
      ).rejects.toThrow("请求超时");
    } finally {
      await agent.close();
    }
  });

  it("重定向次数有上限", async () => {
    const agent = new MockAgent();
    agent.disableNetConnect();
    for (let hop = 0; hop < 3; hop += 1) {
      agent
        .get("http://public.test")
        .intercept({ path: `/${hop}` })
        .reply(302, "", { headers: { location: `http://public.test/${hop + 1}` } });
    }
    try {
      await expect(
        safeFetch("http://public.test/0", {
          lookup: publicLookup,
          dispatcher: agent,
          maxRedirects: 2,
        }),
      ).rejects.toThrow("重定向次数过多");
    } finally {
      await agent.close();
    }
  });
});
