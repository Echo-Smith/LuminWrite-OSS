import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { BlockId, EditOp } from "@luminwrite/contracts";
import { openMemoryDatabase } from "@luminwrite/db";
import { blockHashSync, blockText, listBlocks, type PMJSON } from "@luminwrite/doc-model";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { describe, expect, it } from "vitest";

import { createApp } from "./app.js";

const actor = { type: "user" as const, userId: "user_1" };

function replaceFirst(doc: PMJSON, newText: string): EditOp {
  const node = listBlocks(doc)[0];
  if (!node) throw new Error("没有块");
  return {
    op: "replace_text",
    blockId: node.attrs?.id as BlockId,
    contentHash: blockHashSync(node),
    expectedOldText: blockText(node),
    newText,
    reason: "手改",
  };
}

describe("HTTP", () => {
  it("建文档、出大纲、写初稿、只改一段、回滚", async () => {
    const dir = await mkdtemp(join(tmpdir(), "lw-http-"));
    const db = await openMemoryDatabase();
    const runtime = await createWritingRuntime({
      sqlitePath: join(dir, "pi.sqlite"),
      store: writingStoreFromDatabase(db),
      usage: { record: (event) => db.recordUsage(event) },
    });
    const app = createApp({ db, runtime });
    const health = await app.request("/health");
    expect(await health.json()).toMatchObject({ ok: true });

    const createdRes = await app.request("/documents", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ title: "推理成本", ownerId: "user_1" }),
    });
    expect(createdRes.status).toBe(201);
    const created = (await createdRes.json()) as { id: string };
    const turn = async (kind: string, key: string, instruction: string) => {
      const response = await app.request(`/documents/${created.id}/turns`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          kind,
          instruction,
          scope: kind === "revise" ? "selection" : "document",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: key,
          actor,
        }),
      });
      expect(response.status).toBe(200);
      return (await response.json()) as { status: string; versionId?: string };
    };

    const outline = await turn("outline", "outline-1", "先写大纲");
    expect(outline.status).toBe("applied");
    const draft = await turn("draft", "draft-1", "按大纲写初稿");
    expect(draft.status).toBe("applied");
    const before = await app.request(`/documents/${created.id}`);
    const drafted = (await before.json()) as { blocks: Array<{ text: string; blockId: string }> };
    const revised = await turn("revise", "revise-1", "只改第2段，写短一点");
    expect(revised.status).toBe("applied");
    const afterRes = await app.request(`/documents/${created.id}`);
    const after = (await afterRes.json()) as { blocks: Array<{ text: string; blockId: string }> };
    expect(after.blocks[0]?.text).toBe(drafted.blocks[0]?.text);
    expect(after.blocks[2]?.text).toBe(drafted.blocks[2]?.text);
    expect(after.blocks[1]?.text).not.toBe(drafted.blocks[1]?.text);
    expect(after.blocks[0]?.blockId).toBe(drafted.blocks[0]?.blockId);
    expect(after.blocks[2]?.blockId).toBe(drafted.blocks[2]?.blockId);

    const versions = (await (
      await app.request(`/documents/${created.id}/versions`)
    ).json()) as Array<{ id: string }>;
    const rolled = await app.request(`/documents/${created.id}/rollback`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ versionId: versions[1]?.id, actor: { type: "system" } }),
    });
    expect(rolled.status).toBe(200);
    const restored = (await (await app.request(`/documents/${created.id}`)).json()) as {
      blocks: Array<{ text: string }>;
    };
    expect(restored.blocks.map((block) => block.text)).toEqual([
      "推理成本先看三件事：显卡时租、显存占用、批大小。",
      "批大小从 1 提到 8 时，单次请求分到的固定开销会下降。",
      "建议先量出当前批大小和每小时费用，再决定加机器还是调批大小。",
    ]);

    const events = await app.request(`/documents/${created.id}/events?after=0`);
    expect((await events.json()) as unknown[]).not.toHaveLength(0);
    const stream = await app.request(`/documents/${created.id}/events/stream`);
    expect(await stream.text()).toContain("id: ");

    const head = await db.getDocument(created.id);
    const manual = await app.request(`/documents/${created.id}/ops`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        actor,
        ops: [replaceFirst(head?.head?.content ?? { type: "doc" }, "手改第一段。")],
      }),
    });
    expect(manual.status).toBe(200);
    const stale = await app.request(`/documents/${created.id}/ops`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        actor,
        ops: [replaceFirst(head?.head?.content ?? { type: "doc" }, "再改一次但锚点是旧的。")],
      }),
    });
    expect(stale.status).toBe(409);

    await runtime.close();
    await db.close();
    await rm(dir, { recursive: true, force: true });
  });
});
