import type {
  Actor,
  BlockId,
  EditOp,
  RunEvent,
  SessionKind,
  TurnRequest,
  TurnStatus,
} from "@luminwrite/contracts";
import { emptyDossier } from "@luminwrite/contracts";
import {
  applyOps,
  blockHashSync,
  blockIdOf,
  blockText,
  emptyDoc,
  findBlock,
  listBlocks,
  type PMJSON,
  rebaseOp,
} from "@luminwrite/doc-model";

import type { PlanBlock } from "./plan.js";
import type { ProposeOutcome } from "./propose.js";
import type { DossierSnapshot, SessionSnapshot, TurnSnapshot, WritingStore } from "./store.js";

interface MemVersion {
  id: string;
  content: PMJSON;
  origin: string;
}

interface MemDoc {
  title: string;
  ownerId: string;
  head: PMJSON;
  versions: MemVersion[];
}

interface MemTurn extends TurnSnapshot {
  idempotencyKey: string;
}

interface MemSession {
  id: string;
  documentId: string;
  kind: SessionKind;
  baseVersionId: string;
  piConversationId: string | null;
  status: "open" | "closed";
  reads: Set<string>;
}

interface MemDossier {
  version: number;
  content: ReturnType<typeof emptyDossier>;
}

let sequence = 0;
function nid(prefix: string): string {
  sequence += 1;
  return `${prefix}_${String(sequence).padStart(8, "0")}`;
}

export class MemoryWritingStore implements WritingStore {
  readonly docs = new Map<string, MemDoc>();
  private readonly sessions = new Map<string, MemSession>();
  private readonly dossiers = new Map<string, MemDossier>();
  private readonly turns = new Map<string, MemTurn>();
  private readonly events: RunEvent[] = [];
  private readonly byTask = new Map<string, ProposeOutcome & { ok: true }>();

  createDocument(input: { title: string; ownerId: string }): {
    id: string;
    versionId: string;
    sessionId: string;
  } {
    const id = nid("doc");
    const versionId = nid("ver");
    const sessionId = nid("ses");
    this.docs.set(id, {
      title: input.title,
      ownerId: input.ownerId,
      head: emptyDoc(),
      versions: [{ id: versionId, content: emptyDoc(), origin: "import" }],
    });
    this.dossiers.set(id, { version: 1, content: emptyDossier() });
    this.sessions.set(sessionId, {
      id: sessionId,
      documentId: id,
      kind: "main",
      baseVersionId: versionId,
      piConversationId: null,
      status: "open",
      reads: new Set(),
    });
    return { id, versionId, sessionId };
  }

  seedParagraphs(documentId: string, paragraphs: readonly string[]): string {
    const doc = this.docs.get(documentId);
    if (!doc) throw new Error(`找不到文档 ${documentId}`);
    const applied = applyOps(doc.head, [
      {
        op: "insert_block",
        afterBlockId: null,
        newMarkdown: paragraphs.join("\n\n"),
        reason: "起稿",
      },
    ]);
    if (!applied.ok) throw new Error(applied.errors[0]?.message ?? "起稿失败");
    const versionId = nid("ver");
    doc.head = applied.doc;
    doc.versions.push({ id: versionId, content: applied.doc, origin: "manual" });
    const main = [...this.sessions.values()].find(
      (session) => session.documentId === documentId && session.kind === "main",
    );
    if (main) main.baseVersionId = versionId;
    return versionId;
  }

  openFork(documentId: string, baseVersionId: string): string {
    const doc = this.docs.get(documentId);
    if (!doc?.versions.some((version) => version.id === baseVersionId)) {
      throw new Error("版本不属于这篇文档");
    }
    const id = nid("ses");
    this.sessions.set(id, {
      id,
      documentId,
      kind: "fork",
      baseVersionId,
      piConversationId: null,
      status: "open",
      reads: new Set(),
    });
    return id;
  }

  head(documentId: string): PMJSON {
    const doc = this.docs.get(documentId);
    if (!doc) throw new Error(`找不到文档 ${documentId}`);
    return doc.head;
  }

  async readBlocks(documentId: string): Promise<PlanBlock[]> {
    return blocksOf(this.head(documentId));
  }

  async readBlocksAt(documentId: string, versionId: string): Promise<PlanBlock[]> {
    const doc = this.docs.get(documentId);
    const version = doc?.versions.find((item) => item.id === versionId);
    if (!version) throw new Error(`找不到版本 ${versionId}`);
    return blocksOf(version.content);
  }

  async readDossier(documentId: string): Promise<DossierSnapshot> {
    const dossier = this.dossiers.get(documentId);
    if (!dossier) throw new Error(`找不到文档 ${documentId}`);
    return { version: dossier.version, content: dossier.content };
  }

  async sessionFor(req: TurnRequest): Promise<SessionSnapshot> {
    const session = req.sessionId
      ? this.sessions.get(req.sessionId)
      : [...this.sessions.values()].find(
          (item) => item.documentId === req.documentId && item.kind === "main",
        );
    if (!session) throw new Error("找不到会话");
    if (session.documentId !== req.documentId) throw new Error("会话只能修订自己所属的文档");
    if (session.status !== "open") throw new Error("会话已关闭");
    return snapshotOf(session);
  }

  async getSession(sessionId: string): Promise<SessionSnapshot | undefined> {
    const session = this.sessions.get(sessionId);
    return session ? snapshotOf(session) : undefined;
  }

  async setSessionConversation(sessionId: string, piConversationId: string): Promise<void> {
    const session = this.sessions.get(sessionId);
    if (!session) throw new Error("找不到会话");
    session.piConversationId = piConversationId;
  }

  async advanceSessionBase(sessionId: string, versionId: string): Promise<void> {
    const session = this.sessions.get(sessionId);
    if (!session) throw new Error("找不到会话");
    const doc = this.docs.get(session.documentId);
    if (!doc?.versions.some((version) => version.id === versionId)) {
      throw new Error("版本不属于这篇文档");
    }
    session.baseVersionId = versionId;
  }

  async canRead(sessionId: string, documentId: string): Promise<boolean> {
    const session = this.sessions.get(sessionId);
    if (!session) return false;
    return session.documentId === documentId || session.reads.has(documentId);
  }

  async beginTurn(
    req: TurnRequest,
  ): Promise<{ turnId: string; created: boolean; status: TurnStatus }> {
    const existing = [...this.turns.values()].find(
      (turn) => turn.documentId === req.documentId && turn.idempotencyKey === req.idempotencyKey,
    );
    if (existing) return { turnId: existing.id, created: false, status: existing.status };
    const turn: MemTurn = {
      id: nid("trn"),
      documentId: req.documentId,
      status: "queued",
      resultVersionId: null,
      error: null,
      piSubmissionId: null,
      idempotencyKey: req.idempotencyKey,
    };
    this.turns.set(turn.id, turn);
    return { turnId: turn.id, created: true, status: "queued" };
  }

  async markTurn(turnId: string, patch: Parameters<WritingStore["markTurn"]>[1]): Promise<void> {
    const turn = this.turns.get(turnId);
    if (!turn) return;
    if (patch.status) turn.status = patch.status;
    if (patch.piSubmissionId) turn.piSubmissionId = patch.piSubmissionId;
    if (patch.resultVersionId) turn.resultVersionId = patch.resultVersionId;
    if (patch.error) turn.error = patch.error;
  }

  async getTurn(turnId: string): Promise<TurnSnapshot | undefined> {
    const turn = this.turns.get(turnId);
    if (!turn) return undefined;
    return { ...turn };
  }

  private async conflict(
    turnId: string,
    index: number,
    code: "block_not_found" | "hash_mismatch",
    message: string,
  ): Promise<ProposeOutcome> {
    await this.markTurn(turnId, { status: "conflicted", error: message });
    return { ok: false, errors: [{ index, code, message }] };
  }

  async propose(input: Parameters<WritingStore["propose"]>[0]): Promise<ProposeOutcome> {
    const cached = this.byTask.get(input.piTaskId);
    if (cached) return { ...cached, already: true };
    if (input.sessionId) {
      const session = this.sessions.get(input.sessionId);
      if (!session || session.documentId !== input.documentId || session.status !== "open") {
        await this.markTurn(input.turnId, {
          status: "failed",
          error: "会话只能修订自己所属的文档",
        });
        return {
          ok: false,
          errors: [{ index: 0, code: "out_of_scope", message: "会话只能修订自己所属的文档" }],
        };
      }
    }
    const doc = this.docs.get(input.documentId);
    if (!doc)
      return { ok: false, errors: [{ index: 0, code: "block_not_found", message: "找不到文档" }] };
    const rebased: EditOp[] = [];
    for (let index = 0; index < input.ops.length; index += 1) {
      const op = input.ops[index];
      if (!op) continue;
      if (op.op === "insert_block") {
        rebased.push(op);
        continue;
      }
      const found = findBlock(doc.head, op.blockId);
      if (!found) {
        return this.conflict(input.turnId, index, "block_not_found", `找不到块 ${op.blockId}`);
      }
      if (blockHashSync(found.node) === op.contentHash) {
        rebased.push(op);
        continue;
      }
      const next = rebaseOp(op, found.node);
      if (!next) {
        return this.conflict(input.turnId, index, "hash_mismatch", `块 ${op.blockId} 已变化`);
      }
      rebased.push(next);
    }
    const applied = applyOps(doc.head, rebased, input.scope ? { scope: input.scope } : undefined);
    if (!applied.ok) {
      await this.markTurn(input.turnId, {
        status: "conflicted",
        error: applied.errors[0]?.message,
      });
      return applied;
    }
    if (input.applyMode === "proposal") {
      const proposalId = nid("prp");
      const outcome = {
        ok: true as const,
        already: false,
        status: "proposed" as const,
        proposalId,
      };
      this.byTask.set(input.piTaskId, outcome);
      await this.markTurn(input.turnId, { status: "proposed" });
      return outcome;
    }
    const versionId = nid("ver");
    doc.head = applied.doc;
    doc.versions.push({ id: versionId, content: applied.doc, origin: "turn" });
    const outcome = { ok: true as const, already: false, status: "applied" as const, versionId };
    this.byTask.set(input.piTaskId, outcome);
    await this.markTurn(input.turnId, { status: "applied", resultVersionId: versionId });
    await this.appendEvent(input.documentId, {
      turnId: input.turnId,
      type: "edits.applied",
      payload: { versionId },
    });
    return outcome;
  }

  async appendEvent(
    documentId: string,
    event: { turnId?: string; type: string; payload: unknown },
  ): Promise<RunEvent> {
    const seq = this.events.filter((item) => item.documentId === documentId).length + 1;
    const stored: RunEvent = {
      seq,
      documentId,
      ...(event.turnId ? { turnId: event.turnId } : {}),
      type: event.type,
      payload: event.payload,
    };
    this.events.push(stored);
    return stored;
  }

  async eventsAfter(documentId: string, afterSeq = 0): Promise<RunEvent[]> {
    return this.events.filter((event) => event.documentId === documentId && event.seq > afterSeq);
  }
}

function blocksOf(doc: PMJSON): PlanBlock[] {
  return listBlocks(doc).map((node) => {
    const blockId = blockIdOf(node);
    if (!blockId) throw new Error("块缺少 id");
    return { blockId, contentHash: blockHashSync(node), text: blockText(node) };
  });
}

function snapshotOf(session: MemSession): SessionSnapshot {
  return {
    id: session.id,
    documentId: session.documentId,
    kind: session.kind,
    baseVersionId: session.baseVersionId,
    piConversationId: session.piConversationId,
    status: session.status,
  };
}

export type { Actor, BlockId };
