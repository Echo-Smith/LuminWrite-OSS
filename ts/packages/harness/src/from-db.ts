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
    async sessionFor(req) {
      const session = await db.resolveSession(req);
      return {
        id: session.id,
        documentId: session.documentId,
        kind: session.kind,
        baseVersionId: session.baseVersionId,
        piConversationId: session.piConversationId,
        status: session.status,
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
      };
    },
    setSessionConversation: (sessionId, piConversationId) =>
      db.setSessionConversation(sessionId, piConversationId),
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
      };
    },
    propose: (input) => db.propose(input),
    appendEvent: (documentId, event) => db.appendEvent(documentId, event),
    eventsAfter: (documentId, afterSeq) => db.eventsAfter(documentId, afterSeq),
  };
}
