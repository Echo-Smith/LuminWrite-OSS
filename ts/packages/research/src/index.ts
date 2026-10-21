export type { CitationState, CitedEvidence, ClaimFlag } from "./claims.js";
export { flagClaims } from "./claims.js";
export type { ResearchPlan, SourceChannel, StoredResearchPlan, SubQuestion } from "./plan.js";
export {
  acceptPlan,
  fauxResearchPlan,
  gapsFor,
  hasQuestion,
  isQuestionId,
  parseResearchPlan,
  ResearchPlanSchema,
  readStoredPlan,
} from "./plan.js";
export type { CitationStatus, QuoteCheck } from "./verify.js";
export { normalizeCitationText, verifyQuote } from "./verify.js";

export const phase = "citations" as const;
export const note =
  "检索计划由模型写，落在 research_plans。服务端只校验问题 id、证据钉和引用是否对得上来源。";
