import { describe, expect, it } from "vitest";

import { note, phase } from "./index.js";

describe("research", () => {
  it("计划落库，引用由校验决定", () => {
    expect(phase).toBe("citations");
    expect(note).toContain("research_plans");
  });
});
