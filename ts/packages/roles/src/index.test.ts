import type { EditOp } from "@luminwrite/contracts";
import { describe, expect, it } from "vitest";

import { overBudget } from "./budget.js";
import { builtinRoles } from "./load.js";
import { parseRole } from "./parse.js";
import { outsideScope } from "./scope.js";
import { allowedToolNames } from "./tools.js";

const budget = {
  maxSteps: 2,
  maxInputTokens: 10,
  maxOutputTokens: 10,
  maxCostUsd: 1,
  timeoutS: 5,
};

describe("角色", () => {
  it("四个内置角色只认槽，审校交提案，引用审计不检索", () => {
    const roles = builtinRoles();
    expect(roles.map((role) => role.id)).toEqual([
      "citation_audit",
      "fact_check",
      "researcher",
      "reviewer",
    ]);
    expect(roles.map((role) => role.model.slot)).toEqual([
      "role.citation_audit",
      "role.fact_check",
      "role.researcher",
      "role.reviewer",
    ]);
    const reviewer = roles.find((role) => role.id === "reviewer");
    const audit = roles.find((role) => role.id === "citation_audit");
    expect(reviewer?.output).toBe("proposal");
    expect(reviewer?.origin).toBe("builtin");
    expect(audit?.scope.includeSources).toBe(false);
    if (!audit) throw new Error("没有引用审计");
    expect(allowedToolNames(audit)).not.toContain("search_kb");
    for (const role of roles) {
      expect(allowedToolNames(role)).not.toContain("delegate");
      expect(role.prompt.length).toBeGreaterThan(0);
    }
  });

  it("模型 id 写进角色文件会被拒绝", () => {
    const markdown = `---
id: bad
name: 坏角色
version: 1
description: 不该写死模型
output: findings
tools:
  allow:
    - read_blocks
scope:
  maxBlocks: 1
  includeNeighbors: 0
  includeSources: false
model:
  slot: role.reviewer
  modelId: gpt
budget:
  maxSteps: 1
  maxInputTokens: 1
  maxOutputTokens: 1
  maxCostUsd: 0
  timeoutS: 1
---
不要用。
`;
    expect(() => parseRole(markdown, "user")).toThrow(/additionalProperties|modelId|不合法/);
  });

  it("缺 frontmatter 或重复字段不能当角色", () => {
    expect(() => parseRole("没有头", "user")).toThrow(/frontmatter/);
    expect(() =>
      parseRole(
        `---
id: twice
id: again
name: 重复
version: 1
description: 重复
output: findings
tools:
  allow:
    - read_blocks
scope:
  maxBlocks: 1
  includeNeighbors: 0
  includeSources: false
model:
  slot: role.reviewer
budget:
  maxSteps: 1
  maxInputTokens: 1
  maxOutputTokens: 1
  maxCostUsd: 0
  timeoutS: 1
---
正文
`,
        "user",
      ),
    ).toThrow(/重复字段/);
  });

  it("步数、token、费用或时间超了算超预算", () => {
    const base = { steps: 1, inputTokens: 1, outputTokens: 1, costUsd: 0, elapsedMs: 1 };
    expect(overBudget(base, budget)).toBe(false);
    expect(overBudget({ ...base, steps: 3 }, budget)).toBe(true);
    expect(overBudget({ ...base, inputTokens: 11 }, budget)).toBe(true);
    expect(overBudget({ ...base, outputTokens: 11 }, budget)).toBe(true);
    expect(overBudget({ ...base, costUsd: 1.1 }, budget)).toBe(true);
    expect(overBudget({ ...base, elapsedMs: 5001 }, budget)).toBe(true);
  });

  it("范围外的块和关掉来源后的检索工具都进不去", () => {
    const inside: EditOp = {
      op: "replace_text",
      blockId: "blk_inside00000001",
      contentHash: `sha256:${"a".repeat(64)}`,
      expectedOldText: "原文",
      newText: "新文",
      reason: "改",
    };
    const outside: EditOp = { ...inside, blockId: "blk_outside0000001" };
    const insert: EditOp = {
      op: "insert_block",
      afterBlockId: null,
      newMarkdown: "新段",
      reason: "插",
    };
    expect(outsideScope(inside, ["blk_inside00000001"])).toBe(false);
    expect(outsideScope(outside, ["blk_inside00000001"])).toBe(true);
    expect(outsideScope(insert, ["blk_inside00000001"])).toBe(true);
    const role = parseRole(
      `---
id: quiet
name: 只读
version: 1
description: 不检索
output: findings
tools:
  allow:
    - read_blocks
    - search_kb
    - delegate
  deny:
    - delegate
scope:
  maxBlocks: 2
  includeNeighbors: 0
  includeSources: false
model:
  slot: role.citation_audit
  prefer:
    - tier/strong
  temperature: 0
budget:
  maxSteps: 4
  maxInputTokens: 10
  maxOutputTokens: 10
  maxCostUsd: 0
  timeoutS: 30
---
只看。
`,
      "user",
    );
    expect(role.model.slot).toBe("role.citation_audit");
    expect(role.model.prefer).toEqual(["tier/strong"]);
    expect(allowedToolNames(role)).toEqual(["read_blocks"]);
  });
});
