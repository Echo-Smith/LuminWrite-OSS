import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ensureOwner, openMemoryDatabase, readOwnerToken, SessionScopeError } from "@luminwrite/db";
import {
  createWritingRuntime,
  SECTION_PARAGRAPHS,
  writingStoreFromDatabase,
} from "@luminwrite/harness";
import { createApp, createOssAuth } from "@luminwrite/server";

const actor = { type: "user" as const, userId: "user_demo" };
const forkActor = { type: "user" as const, userId: "user_fork" };

interface TurnResponse {
  status: string;
  versionId?: string;
  error?: string;
}

interface BlockView {
  blockId: string;
  text: string;
  contentHash: string;
}

interface DocumentResponse {
  blocks: BlockView[];
}

interface ApplyResponse {
  ok: boolean;
  versionId?: string;
  errors?: Array<{ code: string }>;
}

interface SessionResponse {
  id: string;
  kind: string;
  piConversationId: string | null;
  baseVersionId: string;
}

interface DossierResponse {
  version: number;
  content: {
    outline: string[];
    decisions: string[];
    citations: Array<{ title: string; locator: string }>;
    requirements: string[];
  };
}

export interface DemoResult {
  ok: boolean;
  log: string;
  documentId?: string;
}

export async function runDemo(): Promise<DemoResult> {
  const lines: string[] = [];
  const dir = await mkdtemp(join(tmpdir(), "lw-demo-"));
  const dataDir = join(dir, "data");
  const db = await openMemoryDatabase();
  await ensureOwner(db, { dataDir });
  const token = await readOwnerToken(dataDir);
  const runtime = await createWritingRuntime({
    sqlitePath: join(dir, "pi.sqlite"),
    store: writingStoreFromDatabase(db),
  });
  const app = createApp({ db, runtime, auth: createOssAuth(db) });
  const call = async (path: string, init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    headers.set("authorization", `Bearer ${token}`);
    if (init?.body && !headers.has("content-type")) headers.set("content-type", "application/json");
    return app.request(path, { ...init, headers });
  };
  const fail = (message: string): DemoResult => ({
    ok: false,
    log: lines.concat(message).join("\n"),
  });
  try {
    const created = (await (
      await call("/api/v1/documents", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ title: "推理成本", ownerId: "user_demo" }),
      })
    ).json()) as { id: string; sessionId: string };
    lines.push(`文档 ${created.id}`);
    const seeded = (await (
      await call(`/api/v1/documents/${created.id}/ops`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          actor,
          sessionId: created.sessionId,
          summary: "五段底稿",
          ops: [
            {
              op: "insert_block",
              afterBlockId: null,
              newMarkdown: SECTION_PARAGRAPHS.join("\n\n"),
              reason: "起稿",
            },
          ],
        }),
      })
    ).json()) as ApplyResponse;
    if (!seeded.ok || !seeded.versionId) return fail("底稿没有写上");
    const dossier = (await (
      await call(`/api/v1/documents/${created.id}/dossier`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          expectedVersion: 1,
          ops: [
            { op: "set_outline", items: ["先看时租", "再看批大小"] },
            { op: "add_citation", title: "时租价目", locator: "2026-10" },
            { op: "add_requirement", text: "只写能核对的数字" },
            { op: "add_decision", text: "先不增加机器" },
          ],
        }),
      })
    ).json()) as { ok?: boolean; version?: number };
    if (dossier.version !== 2) return fail("档案没有按操作更新");

    const turn = async (
      documentId: string,
      sessionId: string,
      key: string,
      instruction: string,
    ) => {
      const response = await call(`/api/v1/documents/${documentId}/turns`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          sessionId,
          kind: "revise",
          instruction,
          scope: "selection",
          delivery: "followup",
          applyMode: "auto",
          idempotencyKey: key,
          actor,
        }),
      });
      return (await response.json()) as TurnResponse;
    };

    const mainEdit = await turn(created.id, created.sessionId, "main-p3", "只改第3段，写短一点");
    if (mainEdit.status !== "applied") return fail(`主会话改第 3 段失败 ${mainEdit.status}`);
    const afterMain = await loadDoc(call, created.id);
    lines.push("主会话改了第 3 段");

    const fork = (await (
      await call(`/api/v1/documents/${created.id}/sessions`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          kind: "fork",
          baseVersionId: seeded.versionId,
          actor: forkActor,
        }),
      })
    ).json()) as SessionResponse;
    if (fork.kind !== "fork" || fork.piConversationId !== null)
      return fail("分叉会话没有从空对话开始");
    const stale = await turn(created.id, fork.id, "fork-p3", "只改第3段，写短一点");
    if (stale.status !== "conflicted") return fail(`过期的第 3 段应当被拒绝，实际 ${stale.status}`);
    const afterStale = await loadDoc(call, created.id);
    if (afterStale.blocks[2]?.text !== afterMain.blocks[2]?.text)
      return fail("过期修订改写了第 3 段");
    lines.push("分叉基于旧版本改第 3 段被拒绝");

    const forkFifth = await turn(created.id, fork.id, "fork-p5", "只改第5段，写短一点");
    if (forkFifth.status !== "applied") return fail(`分叉改第 5 段失败 ${forkFifth.status}`);
    const afterFork = await loadDoc(call, created.id);
    if (afterFork.blocks[2]?.text !== afterMain.blocks[2]?.text)
      return fail("第 5 段的修订动了第 3 段");
    if (afterFork.blocks[4]?.text === afterMain.blocks[4]?.text) return fail("第 5 段没有改动");
    if (afterFork.blocks[0]?.text !== afterMain.blocks[0]?.text) return fail("第 1 段被带动");
    if (afterFork.blocks[3]?.text !== afterMain.blocks[3]?.text) return fail("第 4 段被带动");
    lines.push("分叉改第 5 段已应用，第 3 段仍是主会话的文本");

    const derived = (await (
      await call(`/api/v1/documents/${created.id}/derive`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          versionId: seeded.versionId,
          inheritDossier: true,
          ownerId: "user_demo",
          title: "推理成本的派生",
          actor,
        }),
      })
    ).json()) as { id: string; sessionId: string };
    const derivedSessions = (await (
      await call(`/api/v1/documents/${derived.id}/sessions`)
    ).json()) as SessionResponse[];
    if (derivedSessions.length !== 1 || derivedSessions[0]?.piConversationId !== null) {
      return fail("派生文档带走了聊天");
    }
    const derivedDossier = (await (
      await call(`/api/v1/documents/${derived.id}/dossier`)
    ).json()) as DossierResponse;
    if (derivedDossier.content.decisions.length !== 0) return fail("派生档案不该带走关键决定");
    if (JSON.stringify(derivedDossier.content).includes("先不增加机器")) {
      return fail("关键决定被复制进了派生档案");
    }
    const later = (await (
      await call(`/api/v1/documents/${created.id}/dossier`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          expectedVersion: 2,
          ops: [{ op: "add_decision", text: "改用更便宜的显卡时租" }],
        }),
      })
    ).json()) as { ok?: boolean };
    if (!later.ok) return fail("源文档的新决定没有写上");
    let liveDecisions: string[] = [];
    try {
      const live = await runtime.readRelatedDossier(derived.sessionId, created.id);
      liveDecisions = live.content.decisions;
    } catch {
      return fail("派生会话没有按血缘读到源文档档案");
    }
    if (
      !liveDecisions.includes("先不增加机器") ||
      !liveDecisions.includes("改用更便宜的显卡时租")
    ) {
      return fail("读到的不是源文档的当前决定");
    }
    const stillEmpty = (await (
      await call(`/api/v1/documents/${derived.id}/dossier`)
    ).json()) as DossierResponse;
    if (stillEmpty.content.decisions.length !== 0) return fail("读取源档案时复制了决定");
    const stranger = (await (
      await call("/api/v1/documents", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ title: "无关", ownerId: "user_demo" }),
      })
    ).json()) as { id: string };
    try {
      await runtime.readRelatedDossier(derived.sessionId, stranger.id);
      return fail("无关文档的档案不该被读到");
    } catch (error) {
      if (!(error instanceof SessionScopeError)) return fail("拒绝无关文档时的错误不对");
    }
    lines.push("派生会话按血缘读到源文档的当前决定，没有复制到自己的档案");
    const adopted = (await (
      await call(`/api/v1/documents/${derived.id}/dossier`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          expectedVersion: 1,
          ops: [{ op: "add_decision", text: "先不增加机器" }],
        }),
      })
    ).json()) as { ok?: boolean; content?: { decisions: string[] } };
    if (!adopted.ok || adopted.content?.decisions.join("、") !== "先不增加机器") {
      return fail("派生文档没有用自己的档案操作记下决定");
    }
    lines.push("要留下的决定写进派生文档自己的档案");
    if (
      derivedDossier.content.outline.length !== 2 ||
      derivedDossier.content.requirements.length !== 1
    ) {
      return fail("派生档案没有复制大纲和要求");
    }
    const derivedDoc = await loadDoc(call, derived.id);
    const sourceIds = new Set(afterFork.blocks.map((block) => block.blockId));
    if (derivedDoc.blocks.some((block) => sourceIds.has(block.blockId)))
      return fail("派生文档沿用了源文档的块 id");
    if (derivedDoc.blocks.map((block) => block.text).join("\n") !== SECTION_PARAGRAPHS.join("\n")) {
      return fail("派生文档的正文不是所选版本");
    }
    lines.push(`派生文档 ${derived.id}，块 id 与源文档没有交集`);

    const derivedEdit = await turn(
      derived.id,
      derived.sessionId,
      "derived-p1",
      "只改第1段，写短一点",
    );
    if (derivedEdit.status !== "applied")
      return fail(`派生文档改第 1 段失败 ${derivedEdit.status}`);
    const sourceAfterDerived = await loadDoc(call, created.id);
    if (
      sourceAfterDerived.blocks.map((block) => block.text).join("\n") !==
      afterFork.blocks.map((block) => block.text).join("\n")
    ) {
      return fail("派生文档的修订改到了源文档");
    }
    lines.push("派生文档的修订没有改到源文档");

    const listed = (await (await call(`/api/v1/documents/${created.id}/derived`)).json()) as Array<{
      id: string;
    }>;
    if (!listed.some((item) => item.id === derived.id)) return fail("源文档列不出派生文档");

    await call(`/api/v1/sessions/${created.sessionId}/reads`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ documentId: derived.id }),
    });
    const derivedNow = await loadDoc(call, derived.id);
    const sourceNow = await loadDoc(call, created.id);
    const foreign = derivedNow.blocks[0];
    const home = sourceNow.blocks[0];
    if (!foreign || !home) return fail("缺少可写回的段落");
    const rejected = (await (
      await call(`/api/v1/sessions/${created.sessionId}/edits`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          documentIds: [created.id],
          actor,
          ops: [
            {
              op: "replace_text",
              blockId: foreign.blockId,
              contentHash: foreign.contentHash,
              expectedOldText: foreign.text,
              newText: "不该写上的句子。",
              reason: "误用派生文档的块 id",
            },
          ],
        }),
      })
    ).json()) as ApplyResponse;
    if (rejected.ok || rejected.errors?.[0]?.code !== "block_not_found") {
      return fail("用派生文档的块 id 写源文档应当被拒绝");
    }
    const mixed = (await (
      await call(`/api/v1/sessions/${created.sessionId}/edits`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          documentIds: [created.id, derived.id],
          actor,
          ops: [
            {
              op: "replace_text",
              blockId: home.blockId,
              contentHash: home.contentHash,
              expectedOldText: home.text,
              newText: foreign.text,
              reason: "一批里写两篇",
            },
          ],
        }),
      })
    ).json()) as ApplyResponse;
    if (mixed.ok || mixed.errors?.[0]?.code !== "out_of_scope")
      return fail("一批修订不该同时写两篇文档");
    const brought = (await (
      await call(`/api/v1/sessions/${created.sessionId}/edits`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          documentIds: [created.id],
          actor,
          summary: "从派生文档写回第 1 段",
          ops: [
            {
              op: "replace_text",
              blockId: home.blockId,
              contentHash: home.contentHash,
              expectedOldText: home.text,
              newText: foreign.text,
              reason: "写回派生文档的第 1 段",
            },
          ],
        }),
      })
    ).json()) as ApplyResponse;
    if (!brought.ok) return fail("用源文档自己的锚点写回失败");
    const afterBring = await loadDoc(call, created.id);
    const derivedUntouched = await loadDoc(call, derived.id);
    if (afterBring.blocks[0]?.text !== foreign.text) return fail("第 1 段没有写回");
    if (afterBring.blocks[2]?.text !== afterFork.blocks[2]?.text) return fail("写回改动了第 3 段");
    if (derivedUntouched.blocks[0]?.text !== foreign.text) return fail("写回改动了派生文档");
    lines.push("主会话读了派生文档，用源文档的锚点写回第 1 段");
    lines.push("DEMO_OK");
    return { ok: true, log: lines.join("\n"), documentId: created.id };
  } finally {
    await runtime.close();
    await db.close();
    await rm(dir, { recursive: true, force: true });
  }
}

async function loadDoc(
  call: (path: string, init?: RequestInit) => Promise<Response>,
  id: string,
): Promise<DocumentResponse> {
  return (await (await call(`/api/v1/documents/${id}`)).json()) as DocumentResponse;
}
