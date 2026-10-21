import type { RoleBudget } from "./schema.js";

export interface RoleSpend {
  steps: number;
  inputTokens: number;
  outputTokens: number;
  costUsd: number;
  elapsedMs: number;
}

export function overBudget(spend: RoleSpend, budget: RoleBudget): boolean {
  if (spend.steps > budget.maxSteps) return true;
  if (spend.inputTokens > budget.maxInputTokens) return true;
  if (spend.outputTokens > budget.maxOutputTokens) return true;
  if (spend.costUsd > budget.maxCostUsd) return true;
  if (spend.elapsedMs > budget.timeoutS * 1000) return true;
  return false;
}
