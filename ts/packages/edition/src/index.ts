export type UsageEvent = {
  readonly requestId: string;
  readonly model: string;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly costMicros: number;
};

export type UsageSink = {
  record(event: UsageEvent): Promise<void>;
};

export const noopUsageSink: UsageSink = {
  record() {
    return Promise.resolve();
  },
};

export type EntitlementPort = {
  allow(feature: string): Promise<boolean>;
};

export const allowAllEntitlements: EntitlementPort = {
  allow() {
    return Promise.resolve(true);
  },
};

export type EditAuthorizer = {
  canEdit(documentId: string, actorUserId: string): Promise<boolean>;
};

export function ownerAuthorizer(
  ownerOf: (documentId: string) => Promise<string | undefined>,
): EditAuthorizer {
  return {
    async canEdit(documentId, actorUserId) {
      const owner = await ownerOf(documentId);
      return owner === actorUserId;
    },
  };
}

export type ProposalPolicy = {
  defaultApplyMode(document: { humanTouched: boolean }): "auto" | "proposal";
};

export const humanTouchedProposalPolicy: ProposalPolicy = {
  defaultApplyMode(document) {
    return document.humanTouched ? "proposal" : "auto";
  },
};

export type OpPipelineHooks = {
  beforeApply?(documentId: string, opCount: number): Promise<void>;
};

export const noopOpPipeline: OpPipelineHooks = {};

export type RealtimeChannel = {
  publish(documentId: string, event: { type: string }): Promise<void>;
};

export const noopRealtime: RealtimeChannel = {
  publish() {
    return Promise.resolve();
  },
};
