import { describe, expect, it } from "vitest";

import { phase } from "./index.js";

describe("kb", () => {
  it("地基阶段可以入库和检索", () => {
    expect(phase).toBe("foundation");
  });
});
