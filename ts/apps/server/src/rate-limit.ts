export interface TokenBucketOptions {
  capacity: number;
  refillPerSecond: number;
  now?: () => number;
}

/**
 * 按密钥放在内存里的令牌桶。进程重启后清空。
 * 默认突发 60 次，之后每秒补 1 个。所有者令牌不走这里。
 */
export class TokenBucketLimiter {
  private readonly buckets = new Map<string, { tokens: number; updatedAt: number }>();
  private readonly options: TokenBucketOptions;

  constructor(options: TokenBucketOptions) {
    this.options = options;
  }

  take(keyId: string): { ok: true } | { ok: false; retryAfterSeconds: number } {
    const now = this.options.now?.() ?? Date.now();
    const current = this.buckets.get(keyId) ?? {
      tokens: this.options.capacity,
      updatedAt: now,
    };
    const elapsedSeconds = Math.max(0, (now - current.updatedAt) / 1000);
    const refilled = Math.min(
      this.options.capacity,
      current.tokens + elapsedSeconds * this.options.refillPerSecond,
    );
    if (refilled < 1) {
      this.buckets.set(keyId, { tokens: refilled, updatedAt: now });
      return { ok: false, retryAfterSeconds: this.retryAfter(1 - refilled) };
    }
    this.buckets.set(keyId, { tokens: refilled - 1, updatedAt: now });
    return { ok: true };
  }

  private retryAfter(tokensNeeded: number): number {
    if (this.options.refillPerSecond <= 0) return 60;
    return Math.max(1, Math.ceil(tokensNeeded / this.options.refillPerSecond));
  }
}

export function defaultRateLimiter(): TokenBucketLimiter {
  return new TokenBucketLimiter({ capacity: 60, refillPerSecond: 1 });
}

/** 正在跑的回合数。和令牌桶一样只活在本进程里，api_tasks 表还没建。 */
export class TaskConcurrency {
  private readonly inflight = new Map<string, number>();

  tryAcquire(keyId: string, max: number): boolean {
    const current = this.inflight.get(keyId) ?? 0;
    if (current >= max) return false;
    this.inflight.set(keyId, current + 1);
    return true;
  }

  release(keyId: string): void {
    const current = this.inflight.get(keyId) ?? 0;
    if (current <= 1) this.inflight.delete(keyId);
    else this.inflight.set(keyId, current - 1);
  }
}
