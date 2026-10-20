import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { blockText, listBlocks } from "@luminwrite/doc-model";
import { builtinRoles, parseRole } from "@luminwrite/roles";
import { afterEach, describe, expect, it } from "vitest";

import { MemoryWritingStore } from "./memory-store.js";
import { createWritingRuntime } from "./runtime.js";

const user = { type: "user" as const, userId: "user_1" };
const dirs: string[] = [];

afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

function request(documentId: string, key: string, instruction: string) {
  return {
    documentId,
    kind: "review" as const,
    instruction,
    scope: "document" as const,
    delivery: "followup" as const,
    applyMode: "auto" as const,
    idempotencyKey: key,
    actor: user,
  };
}

const tight = parseRole(
  `---
id: tight
name: 预算为零
version: 1
description: 第一步就超预算
output: findings
tools:
  allow:
    - read_blocks
scope:
  maxBlocks: 4
  includeNeighbors: 0
  includeSources: false
model:
  slot: role.reviewer
budget:
  maxSteps: 0
  maxInputTokens: 100
  maxOutputTokens: 100
  maxCostUsd: 1
  timeoutS: 30
---
只看。
`,
  "user",
);

describe("delegate", () => {
  it("越界修订不落版本，预算用尽也不落版本，范围内的缩短会落一版", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-delegate-"));
    dirs.push(dir);
    const store = new MemoryWritingStore();
    const created = store.createDocument({ title: "审校", ownerId: "user_1" });
    store.seedParagraphs(created.id, [
      "第一段先写成本。后面这句不该留。",
      "第二段保持原样。不要改这里。",
    ]);
    const versionsBefore = store.docs.get(created.id)?.versions.length ?? 0;
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store,
      roles: [...builtinRoles(), tight],
    });
    try {
      const outside = await runtime.submitTurn(
        request(created.id, "out-1", "委托 reviewer 越界改第二段"),
      );
      expect(outside.status).toBe("failed");
      const outsideTurn = await store.getTurn(outside.turnId);
      expect(outsideTurn?.error).toBe("out_of_scope");
      expect(store.docs.get(created.id)?.versions).toHaveLength(versionsBefore);
      expect(listBlocks(store.head(created.id)).map(blockText)).toEqual([
        "第一段先写成本。后面这句不该留。",
        "第二段保持原样。不要改这里。",
      ]);
      expect(store.runs).toHaveLength(1);
      expect(store.runs[0]).toMatchObject({
        status: "failed",
        slot: "role.reviewer",
        output: { code: "out_of_scope" },
      });

      const broke = await runtime.submitTurn(request(created.id, "budget-1", "委托 tight 看一眼"));
      expect(broke.status).toBe("failed");
      expect((await store.getTurn(broke.turnId))?.error).toBe("budget_exceeded");
      expect(store.docs.get(created.id)?.versions).toHaveLength(versionsBefore);
      const budgetRun = store.runs.find((run) => run.agentRef === "tight");
      expect(budgetRun).toMatchObject({
        status: "budget_exceeded",
        slot: "role.reviewer",
        modelRef: "test/faux",
      });

      const edited = await runtime.submitTurn(
        request(created.id, "edit-1", "委托 reviewer 缩短第一段"),
      );
      expect(edited.status).toBe("applied");
      expect(edited.versionId).toBeTruthy();
      const texts = listBlocks(store.head(created.id)).map(blockText);
      expect(texts).toEqual(["第一段先写成本。", "第二段保持原样。不要改这里。"]);
      expect(store.docs.get(created.id)?.versions.length).toBe(versionsBefore + 1);
      const editedRun = store.runs.find((run) => run.turnId === edited.turnId);
      expect(editedRun).toMatchObject({
        status: "succeeded",
        slot: "role.reviewer",
        modelRef: "test/faux",
      });

      const again = await runtime.submitTurn(
        request(created.id, "edit-1", "委托 reviewer 缩短第一段"),
      );
      expect(again.turnId).toBe(edited.turnId);
      expect(store.runs.filter((run) => run.turnId === edited.turnId)).toHaveLength(1);
      expect(store.docs.get(created.id)?.versions.length).toBe(versionsBefore + 1);

      const events = await runtime.events(created.id);
      expect(events.filter((event) => event.type === "delegate.result").length).toBeGreaterThan(0);
    } finally {
      await runtime.close();
    }
  });
});
