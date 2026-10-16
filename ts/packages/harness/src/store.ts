import type {
  Actor,
  BlockId,
  EditOp,
  RunEvent,
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
}

export interface WritingStore {
  readBlocks(documentId: string): Promise<PlanBlock[]>;
  beginTurn(req: TurnRequest): Promise<{ turnId: string; created: boolean; status: TurnStatus }>;
  markTurn(
    turnId: string,
    patch: {
      status?: TurnStatus;
      piConversationId?: string;
      piSubmissionId?: string;
      resultVersionId?: string;
      error?: string;
    },
  ): Promise<void>;
  getTurn(turnId: string): Promise<TurnSnapshot | undefined>;
  propose(input: {
    documentId: string;
    turnId: string;
    ops: EditOp[];
    piTaskId: string;
    applyMode: "auto" | "proposal";
    actor: Actor;
    summary: string;
    scope?: BlockId[];
  }): Promise<ProposeOutcome>;
  appendEvent(
    documentId: string,
    event: { turnId?: string; type: string; payload: unknown },
  ): Promise<RunEvent>;
  eventsAfter(documentId: string, afterSeq?: number): Promise<RunEvent[]>;
}
