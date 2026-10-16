import { describe, expect, it } from "vitest";

import { phase } from "./index.js";

describe("roles", () => {
  it("M0 只留桩", () => {
    expect(phase).toBe("stub");
  });
});
