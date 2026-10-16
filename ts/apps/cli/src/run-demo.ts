import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { openMemoryDatabase } from "@luminwrite/db";
import { createWritingRuntime, writingStoreFromDatabase } from "@luminwrite/harness";
import { createApp } from "@luminwrite/server";

const actor = { type: "user" as const, userId: "user_demo" };

interface TurnResponse {
  status: string;
  versionId?: string;
}

interface DocumentResponse {
  blocks: Array<{ blockId: string; text: string }>;
}

export interface DemoResult {
  ok: boolean;
  log: string;
  documentId?: string;
}

export async function runDemo(): Promise<DemoResult> {
  const lines: string[] = [];
  const dir = await mkdtemp(join(tmpdir(), "lw-demo-"));
  const db = await openMemoryDatabase();
  const runtime = await createWritingRuntime({
    sqlitePath: join(dir, "pi.sqlite"),
    store: writingStoreFromDatabase(db),
  });
  const app = createApp({ db, runtime });
  try {
    const created = (await (
      await app.request("/documents", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ title: "推理成本", ownerId: "user_demo" }),
      })
    ).json()) as { id: string };
    lines.push(`文档 ${created.id}`);
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
      return (await response.json()) as TurnResponse;
    };
    const outline = await turn("outline", "demo-outline", "先写大纲");
    if (outline.status !== "applied")
      return { ok: false, log: lines.concat(`大纲失败 ${outline.status}`).join("\n") };
    lines.push(`大纲版本 ${outline.versionId ?? ""}`);
    const draft = await turn("draft", "demo-draft", "按大纲写初稿");
    if (draft.status !== "applied") return { ok: false, log: lines.concat("初稿失败").join("\n") };
    const drafted = (await (
      await app.request(`/documents/${created.id}`)
    ).json()) as DocumentResponse;
    const revised = await turn("revise", "demo-revise", "只改第2段，写短一点");
    if (revised.status !== "applied")
      return { ok: false, log: lines.concat("修订失败").join("\n") };
    const after = (await (
      await app.request(`/documents/${created.id}`)
    ).json()) as DocumentResponse;
    const untouched =
      after.blocks[0]?.text === drafted.blocks[0]?.text &&
      after.blocks[2]?.text === drafted.blocks[2]?.text &&
      after.blocks[1]?.text !== drafted.blocks[1]?.text;
    if (!untouched) return { ok: false, log: lines.concat("其他段落被改动").join("\n") };
    lines.push("只改了第 2 段，其余段落逐字相同");
    const versions = (await (
      await app.request(`/documents/${created.id}/versions`)
    ).json()) as Array<{ id: string }>;
    await app.request(`/documents/${created.id}/rollback`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ versionId: versions[1]?.id, actor: { type: "system" } }),
    });
    const restored = (await (
      await app.request(`/documents/${created.id}`)
    ).json()) as DocumentResponse;
    if (!restored.blocks[0]?.text.includes("显卡时租")) {
      return { ok: false, log: lines.concat("回滚后的正文不对").join("\n") };
    }
    lines.push(`回滚到大纲，当前 ${restored.blocks.length} 段`);
    lines.push("DEMO_OK");
    return { ok: true, log: lines.join("\n"), documentId: created.id };
  } finally {
    await runtime.close();
    await db.close();
    await rm(dir, { recursive: true, force: true });
  }
}
