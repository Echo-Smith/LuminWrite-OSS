import type {
  Actor,
  BlockId,
  EditOp,
  RunEvent,
  TurnRequest,
  TurnStatus,
} from "@luminwrite/contracts";
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
import type { TurnSnapshot, WritingStore } from "./store.js";

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

let sequence = 0;
function nid(prefix: string): string {
  sequence += 1;
  return `${prefix}_${String(sequence).padStart(8, "0")}`;
}

export class MemoryWritingStore implements WritingStore {
  readonly docs = new Map<string, MemDoc>();
  private readonly turns = new Map<string, MemTurn>();
  private readonly events: RunEvent[] = [];
  private readonly byTask = new Map<string, ProposeOutcome & { ok: true }>();

  createDocument(input: { title: string; ownerId: string }): { id: string; versionId: string } {
    const id = nid("doc");
    const versionId = nid("ver");
    this.docs.set(id, {
      title: input.title,
      ownerId: input.ownerId,
      head: emptyDoc(),
      versions: [{ id: versionId, content: emptyDoc(), origin: "import" }],
    });
    return { id, versionId };
  }

  head(documentId: string): PMJSON {
    const doc = this.docs.get(documentId);
    if (!doc) throw new Error(`找不到文档 ${documentId}`);
    return doc.head;
  }

  async readBlocks(documentId: string): Promise<PlanBlock[]> {
    return listBlocks(this.head(documentId)).map((node) => {
      const blockId = blockIdOf(node);
      if (!blockId) throw new Error("块缺少 id");
      return { blockId, contentHash: blockHashSync(node), text: blockText(node) };
    });
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

  async propose(input: Parameters<WritingStore["propose"]>[0]): Promise<ProposeOutcome> {
    const cached = this.byTask.get(input.piTaskId);
    if (cached) return { ...cached, already: true };
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
      if (!found)
        return {
          ok: false,
          errors: [{ index, code: "block_not_found", message: `找不到块 ${op.blockId}` }],
        };
      if (blockHashSync(found.node) === op.contentHash) {
        rebased.push(op);
        continue;
      }
      const next = rebaseOp(op, found.node);
      if (!next) {
        return {
          ok: false,
          errors: [{ index, code: "hash_mismatch", message: `块 ${op.blockId} 已变化` }],
        };
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

export type { Actor, BlockId };
