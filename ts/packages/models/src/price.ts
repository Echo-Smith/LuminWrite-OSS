import { type AnyModel, calculateCost, type Model } from "@earendil-works/pi-ai";
import type { TokenRates } from "@luminwrite/contracts";

export interface FullRates {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export function fillRates(rates: TokenRates): FullRates {
  return {
    input: rates.input,
    output: rates.output,
    cacheRead: rates.cacheRead ?? 0,
    cacheWrite: rates.cacheWrite ?? 0,
  };
}

const ZERO: FullRates = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };

/** 用户手填 > 注册表声明 > pi-ai 目录。目录价全是 0 时不当真（套餐价）。 */
export function choosePrice(input: {
  user?: TokenRates;
  registry?: TokenRates;
  catalog?: TokenRates;
}): { known: boolean; source: "user" | "registry" | "catalog" | "none"; rates: FullRates } {
  if (input.user) return { known: true, source: "user", rates: fillRates(input.user) };
  if (input.registry) return { known: true, source: "registry", rates: fillRates(input.registry) };
  if (input.catalog) {
    const rates = fillRates(input.catalog);
    const known =
      rates.input > 0 || rates.output > 0 || rates.cacheRead > 0 || rates.cacheWrite > 0;
    if (known) return { known: true, source: "catalog", rates };
    return { known: false, source: "none", rates };
  }
  return { known: false, source: "none", rates: ZERO };
}

export function rateSum(rates: FullRates): number {
  return rates.input + rates.output + rates.cacheRead + rates.cacheWrite;
}

/** 用 pi-ai 的 `calculateCost`。有用户价或保守价时先改模型上的单价再算。 */
export function costMicros(
  model: Pick<Model<string>, "cost">,
  usage: { input: number; output: number; cacheRead?: number; cacheWrite?: number },
  override?: TokenRates,
): number {
  const rates = override ? { ...model.cost, ...fillRates(override) } : model.cost;
  const scratch = {
    input: usage.input,
    output: usage.output,
    cacheRead: usage.cacheRead ?? 0,
    cacheWrite: usage.cacheWrite ?? 0,
    totalTokens: usage.input + usage.output,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
  };
  const priced = {
    id: "cost",
    name: "cost",
    api: "faux",
    provider: "test",
    baseUrl: "http://127.0.0.1",
    input: ["text"],
    reasoning: false,
    contextWindow: 1,
    maxTokens: 1,
    ...model,
    cost: rates,
  } as AnyModel;
  const cost = calculateCost(priced, scratch);
  return Math.round(cost.total * 1_000_000);
}

export function ownerSpendAllowed(spentMicros: number, capUsd: string | undefined): boolean {
  if (capUsd === undefined || capUsd.trim() === "") return true;
  const cap = Number(capUsd);
  if (!Number.isFinite(cap) || cap < 0) return true;
  return spentMicros < Math.round(cap * 1_000_000);
}

export class TurnSpendError extends Error {
  readonly retryAfterSeconds: number;
  constructor(message: string, retryAfterSeconds: number) {
    super(message);
    this.name = "TurnSpendError";
    this.retryAfterSeconds = retryAfterSeconds;
  }
}

export function secondsUntilUtcMidnight(now = new Date()): number {
  const next = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() + 1);
  return Math.max(1, Math.ceil((next - now.getTime()) / 1000));
}

/** 已知价用该模型的单价。未知价用档位里已知最贵成员的价，记入当日上限。 */
export function chargeMicros(
  priceKnown: boolean,
  rates: FullRates,
  conservative: FullRates,
  usage: { input: number; output: number; cacheRead?: number; cacheWrite?: number },
): number {
  const chosen = priceKnown ? rates : conservative;
  return costMicros({ cost: chosen }, usage, chosen);
}

export function assertTurnSpend(input: {
  userMicros: number;
  keyMicros: number;
  ownerCapUsd?: string;
  keyCapUsd?: number | null;
  retryAfterSeconds: number;
}): void {
  if (!ownerSpendAllowed(input.userMicros, input.ownerCapUsd)) {
    throw new TurnSpendError("已到今日费用上限", input.retryAfterSeconds);
  }
  if (input.keyCapUsd != null && Number.isFinite(input.keyCapUsd) && input.keyCapUsd >= 0) {
    const capMicros = Math.round(input.keyCapUsd * 1_000_000);
    if (input.keyMicros >= capMicros) {
      throw new TurnSpendError("已达到当日成本上限", input.retryAfterSeconds);
    }
  }
}
