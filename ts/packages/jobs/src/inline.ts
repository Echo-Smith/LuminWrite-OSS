import type { JobPort, JobSendOptions } from "./port.js";

interface PendingJob {
  id: string;
  name: string;
  data: unknown;
  dueAt: number;
  singletonKey?: string;
  started: boolean;
}

interface Schedule {
  name: string;
  cron: string;
  armedAt: number;
}

const MAX_FIRES = 32;

export interface InlineJobs extends JobPort {
  advance(ms: number): Promise<void>;
}

/** 测试用队列：同步执行，时间只在 advance 里往前走。 */
export function createInlineJobs(): InlineJobs {
  let clock = 0;
  let sequence = 0;
  let draining = false;
  const pending: PendingJob[] = [];
  const handlers = new Map<string, (data: unknown) => Promise<void>>();
  const schedules: Schedule[] = [];

  async function drain(): Promise<void> {
    if (draining) return;
    draining = true;
    try {
      for (;;) {
        const job = pending.find(
          (item) => !item.started && item.dueAt <= clock && handlers.has(item.name),
        );
        if (!job) break;
        const handler = handlers.get(job.name);
        if (!handler) break;
        job.started = true;
        await handler(job.data);
        const index = pending.indexOf(job);
        if (index >= 0) pending.splice(index, 1);
      }
    } finally {
      draining = false;
    }
  }

  async function send(name: string, data: unknown, opts?: JobSendOptions): Promise<string> {
    const dueAt = clock + Math.max(0, opts?.startAfterSeconds ?? 0) * 1000;
    if (opts?.singletonKey) {
      const existing = pending.find(
        (job) => !job.started && job.name === name && job.singletonKey === opts.singletonKey,
      );
      if (existing) {
        existing.data = data;
        existing.dueAt = dueAt;
        return existing.id;
      }
    }
    sequence += 1;
    const job: PendingJob = {
      id: `job_${sequence}`,
      name,
      data,
      dueAt,
      started: false,
      ...(opts?.singletonKey ? { singletonKey: opts.singletonKey } : {}),
    };
    pending.push(job);
    if (dueAt <= clock) await drain();
    return job.id;
  }

  return {
    send,
    async work(name, handler) {
      handlers.set(name, handler);
      await drain();
    },
    async schedule(name, cron) {
      parseCron(cron);
      schedules.push({ name, cron, armedAt: clock });
    },
    async advance(ms) {
      if (ms <= 0) return;
      const start = clock;
      const end = clock + ms;
      const due: string[] = [];
      for (const item of schedules) {
        const times = cronTimes(item.cron, Math.max(start, item.armedAt), end);
        for (const _time of times) due.push(item.name);
      }
      clock = end;
      for (const name of due) await send(name, {});
      await drain();
    },
    async close() {
      pending.splice(0, pending.length);
      handlers.clear();
    },
  };
}

function parseCron(cron: string): string[] {
  const fields = cron.trim().split(/\s+/);
  if (fields.length !== 5) throw new Error(`cron 需要 5 段：${cron}`);
  return fields;
}

function cronTimes(cron: string, fromExclusiveMs: number, toInclusiveMs: number): number[] {
  const startMinute = Math.floor(fromExclusiveMs / 60_000) + 1;
  const endMinute = Math.floor(toInclusiveMs / 60_000);
  const hits: number[] = [];
  for (let minute = startMinute; minute <= endMinute && hits.length < MAX_FIRES; minute += 1) {
    if (cronMatches(cron, new Date(minute * 60_000))) hits.push(minute);
  }
  return hits;
}

function cronMatches(cron: string, date: Date): boolean {
  const [minute, hour, day, month, weekday] = parseCron(cron);
  if (!minute || !hour || !day || !month || !weekday) return false;
  return (
    fieldMatches(minute, date.getUTCMinutes(), 0, 59) &&
    fieldMatches(hour, date.getUTCHours(), 0, 23) &&
    fieldMatches(day, date.getUTCDate(), 1, 31) &&
    fieldMatches(month, date.getUTCMonth() + 1, 1, 12) &&
    fieldMatches(weekday, date.getUTCDay(), 0, 6)
  );
}

function fieldMatches(expr: string, value: number, min: number, max: number): boolean {
  return expr.split(",").some((part) => {
    const [range, stepText] = part.split("/");
    const step = stepText === undefined ? 1 : Number(stepText);
    if (!range || !Number.isInteger(step) || step < 1) return false;
    let lo = min;
    let hi = max;
    if (range !== "*") {
      if (range.includes("-")) {
        const [rawLo, rawHi] = range.split("-");
        lo = Number(rawLo);
        hi = Number(rawHi);
      } else {
        lo = Number(range);
        hi = lo;
      }
    }
    if (!Number.isInteger(lo) || !Number.isInteger(hi)) return false;
    if (value < lo || value > hi) return false;
    return (value - lo) % step === 0;
  });
}
