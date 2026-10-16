import { describe, expect, it } from "vitest";

import { phase } from "./index.js";

describe("sources", () => {
  it("M0 只留桩", () => {
    expect(phase).toBe("stub");
  });
});
