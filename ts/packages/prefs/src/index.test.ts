import { describe, expect, it } from "vitest";

import { phase } from "./index.js";

describe("prefs", () => {
  it("M0 只留桩", () => {
    expect(phase).toBe("stub");
  });
});
