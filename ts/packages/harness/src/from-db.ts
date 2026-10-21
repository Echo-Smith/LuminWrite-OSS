import type { ContentHash } from "@luminwrite/contracts";
import type { LwDatabase } from "@luminwrite/db";

import type { WritingStore } from "./store.js";

export function writingStoreFromDatabase(db: LwDatabase): WritingStore {
  return {
    async readBlocks(documentId) {
      const blocks = await db.blocks(documentId);
      return blocks.map((block) => ({
        blockId: block.blockId,
        contentHash: block.contentHash as ContentHash,
        text: block.text,
      }));
    },
    async readBlocksAt(documentId, versionId) {
      const blocks = await db.readBlocksAt(documentId, versionId);
      return blocks.map((block) => ({
        blockId: block.blockId,
        contentHash: block.contentHash as ContentHash,
        text: block.text,
      }));
    },
    readDossier: (documentId) => db.readDossier(documentId),
    readRelatedDossier: (sessionId, documentId) => db.readRelatedDossier(sessionId, documentId),
    async sessionFor(req) {
      const session = await db.resolveSession(req);
      return {
        id: session.id,
        documentId: session.documentId,
        kind: session.kind,
        baseVersionId: session.baseVersionId,
        piConversationId: session.piConversationId,
        status: session.status,
        modelPin: session.modelPin,
      };
    },
    async getSession(sessionId) {
      const session = await db.getSession(sessionId);
      if (!session) return undefined;
      return {
        id: session.id,
        documentId: session.documentId,
        kind: session.kind,
        baseVersionId: session.baseVersionId,
        piConversationId: session.piConversationId,
        status: session.status,
        modelPin: session.modelPin,
      };
    },
    setSessionConversation: (sessionId, piConversationId) =>
      db.setSessionConversation(sessionId, piConversationId),
    setSessionModelPin: (sessionId, modelPin) => db.setSessionModelPin(sessionId, modelPin),
    advanceSessionBase: (sessionId, versionId) => db.advanceSessionBase(sessionId, versionId),
    canRead: (sessionId, documentId) => db.canRead(sessionId, documentId),
    beginTurn: (req) => db.beginTurn(req),
    markTurn: (turnId, patch) => db.markTurn(turnId, patch),
    async getTurn(turnId) {
      const turn = await db.getTurn(turnId);
      if (!turn) return undefined;
      return {
        id: turn.id,
        documentId: turn.documentId,
        status: turn.status,
        resultVersionId: turn.resultVersionId,
        error: turn.error,
        piSubmissionId: turn.piSubmissionId,
        modelRef: turn.modelRef,
        tierRef: turn.tierRef,
      };
    },
    propose: (input) => db.propose(input),
    saveResearchPlan: (input) => db.saveResearchPlan(input),
    saveSubagentRun: (input) => db.saveSubagentRun(input),
    listSubagentRuns: (documentId) => db.listSubagentRuns(documentId),
    appendEvent: (documentId, event) => db.appendEvent(documentId, event),
    eventsAfter: (documentId, afterSeq) => db.eventsAfter(documentId, afterSeq),
  };
}
