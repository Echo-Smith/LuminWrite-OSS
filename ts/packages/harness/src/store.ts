import type {
  Actor,
  BlockId,
  DossierContent,
  EditOp,
  RunEvent,
  SessionKind,
  TurnRequest,
  TurnStatus,
} from "@luminwrite/contracts";

import type { PlanBlock } from "./plan.js";
import type { ProposeOutcome } from "./propose.js";

export interface TurnSnapshot {
  id: string;
  documentId: string;
  status: TurnStatus;
  resultVersionId: string | null;
  error: string | null;
  piSubmissionId: string | null;
  modelRef: string | null;
  tierRef: string | null;
}

/** 会话行不保存正文。正文只在文档版本链里。 */
export interface SessionSnapshot {
  id: string;
  documentId: string;
  kind: SessionKind;
  baseVersionId: string;
  piConversationId: string | null;
  status: "open" | "closed";
  modelPin: string | null;
}

export interface DossierSnapshot {
  version: number;
  content: DossierContent;
}

export interface WritingStore {
  readBlocks(documentId: string): Promise<PlanBlock[]>;
  readBlocksAt(documentId: string, versionId: string): Promise<PlanBlock[]>;
  readDossier(documentId: string): Promise<DossierSnapshot>;
  readRelatedDossier(sessionId: string, documentId: string): Promise<DossierSnapshot>;
  sessionFor(req: TurnRequest): Promise<SessionSnapshot>;
  getSession(sessionId: string): Promise<SessionSnapshot | undefined>;
  setSessionConversation(sessionId: string, piConversationId: string): Promise<void>;
  setSessionModelPin(sessionId: string, modelPin: string): Promise<void>;
  advanceSessionBase(sessionId: string, versionId: string): Promise<void>;
  canRead(sessionId: string, documentId: string): Promise<boolean>;
  beginTurn(req: TurnRequest): Promise<{ turnId: string; created: boolean; status: TurnStatus }>;
  markTurn(
    turnId: string,
    patch: {
      status?: TurnStatus;
      piConversationId?: string;
      piSubmissionId?: string;
      resultVersionId?: string;
      error?: string;
      modelRef?: string;
      tierRef?: string | null;
    },
  ): Promise<void>;
  getTurn(turnId: string): Promise<TurnSnapshot | undefined>;
  propose(input: {
    documentId: string;
    sessionId?: string;
    turnId: string;
    ops: EditOp[];
    piTaskId: string;
    applyMode: "auto" | "proposal";
    actor: Actor;
    summary: string;
    scope?: BlockId[];
  }): Promise<ProposeOutcome>;
  saveResearchPlan(input: {
    turnId: string;
    sessionId: string;
    plan: unknown;
    gaps: string[];
  }): Promise<{ id: string }>;
  appendEvent(
    documentId: string,
    event: { turnId?: string; type: string; payload: unknown },
  ): Promise<RunEvent>;
  eventsAfter(documentId: string, afterSeq?: number): Promise<RunEvent[]>;
}
