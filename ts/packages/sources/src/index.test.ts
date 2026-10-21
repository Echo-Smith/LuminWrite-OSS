import { describe, expect, it } from "vitest";

import { phase } from "./index.js";

describe("sources", () => {
  it("RSS 和安全抓取已经接上", () => {
    expect(phase).toBe("rss");
  });
});
