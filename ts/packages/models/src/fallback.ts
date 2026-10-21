import type { PricedModel } from "./registry.js";

export class UpstreamModelError extends Error {
  readonly status: number | undefined;
  readonly retryable: boolean;
  constructor(message: string, options: { status?: number; retryable: boolean }) {
    super(message);
    this.name = "UpstreamModelError";
    this.status = options.status;
    this.retryable = options.retryable;
  }
}

export interface FallbackEvent {
  type: "model.fallback";
  from: string;
  to: string;
  credentialId: string;
  reason: string;
}

const COOLDOWN_MS = 60_000;

/**
 * 只在第一个 token 之前换下一个候选。429 的凭据冷却 60 秒。
 * 会话之后应钉住 `used.ref`，不再走档位里更靠前的模型。
 */
export async function runWithFallback<T>(input: {
  chain: readonly PricedModel[];
  now?: number;
  cooldownUntil?: Map<string, number>;
  onEvent?: (event: FallbackEvent) => void;
  run: (model: PricedModel, markToken: () => void) => Promise<T>;
}): Promise<{ value: T; used: PricedModel; events: FallbackEvent[] }> {
  const now = input.now ?? Date.now();
  const cooldown = input.cooldownUntil ?? new Map<string, number>();
  const events: FallbackEvent[] = [];
  let lastError: unknown;
  const pending = input.chain.filter((model) => (cooldown.get(model.credentialId) ?? 0) <= now);
  for (let index = 0; index < pending.length; index += 1) {
    const model = pending[index];
    if (!model) continue;
    if ((cooldown.get(model.credentialId) ?? 0) > now) continue;
    let sawToken = false;
    try {
      const value = await input.run(model, () => {
        sawToken = true;
      });
      return { value, used: model, events };
    } catch (error) {
      lastError = error;
      const retryable = error instanceof UpstreamModelError && error.retryable;
      const status = error instanceof UpstreamModelError ? error.status : undefined;
      const later = pending
        .slice(index + 1)
        .find(
          (candidate) =>
            (cooldown.get(candidate.credentialId) ?? 0) <= now &&
            candidate.credentialId !== (status === 429 ? model.credentialId : ""),
        );
      if (sawToken || !retryable || !later) throw error;
      if (status === 429) cooldown.set(model.credentialId, now + COOLDOWN_MS);
      const event: FallbackEvent = {
        type: "model.fallback",
        from: model.ref,
        to: later.ref,
        credentialId: model.credentialId,
        reason: status === 429 ? "429" : "retryable",
      };
      events.push(event);
      input.onEvent?.(event);
    }
  }
  throw lastError instanceof Error ? lastError : new Error("没有可用模型");
}
