import type { Actor } from "@luminwrite/contracts";

/**
 * 从一次 HTTP 请求推出调用者。
 * OSS：所有者令牌得到 `{ type: "user" }`，API Key 得到 `{ type: "api_client" }`。
 * `{ type: "system" }` 只给进程内任务，不从请求里来。
 * 商业版可以换成多用户登录，表上的 user_id / owner_id 不用改。
 */
export type AuthPort = {
  authenticate(req: Request): Promise<Actor | null>;
};

export type UsageEvent = {
  readonly requestId: string;
  readonly model: string;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly costMicros: number;
  readonly apiKeyId?: string;
  readonly userId?: string;
  readonly credentialId?: string;
  readonly modelRef?: string;
  readonly tierRef?: string;
  readonly sessionId?: string;
  readonly turnId?: string;
  readonly cacheReadTokens?: number;
  readonly cacheWriteTokens?: number;
  readonly priceKnown?: boolean;
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
