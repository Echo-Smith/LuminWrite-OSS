import { z } from "zod";

const SourceChannelSchema = z.enum(["kb", "library", "rss", "web", "academic"]);
const NeedSchema = z.enum(["fact", "data", "definition", "recent", "opinion", "counterpoint"]);
const QuestionIdSchema = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/);

const StepSchema = z.object({
  channel: SourceChannelSchema,
  query: z.string().trim().min(1).max(300),
  recencyDays: z.number().int().positive().max(3650).optional(),
  minAuthority: z.union([z.literal(1), z.literal(2), z.literal(3)]).optional(),
  why: z.string().trim().min(1).max(300),
});

const SubQuestionSchema = z.object({
  id: QuestionIdSchema,
  text: z.string().trim().min(1).max(500),
  critical: z.boolean(),
  needs: z.array(NeedSchema).max(6),
  plan: z.array(StepSchema).max(8),
});

export const ResearchPlanSchema = z.object({
  thesisDraft: z.string().trim().min(1).max(2000),
  questions: z.array(SubQuestionSchema).max(12),
  outOfScope: z.array(z.string().trim().min(1).max(500)).max(20),
});

export type SourceChannel = z.infer<typeof SourceChannelSchema>;
export type Need = z.infer<typeof NeedSchema>;
export type SubQuestion = z.infer<typeof SubQuestionSchema>;
export type ResearchPlan = z.infer<typeof ResearchPlanSchema>;

/** 写进研究回合的提示和工具说明，让模型第一次就按这个对象写。 */
export const RESEARCH_PLAN_HINT =
  "planJson 是一个 JSON 对象，不要包代码围栏。thesisDraft 是字符串。questions 是数组，每项含 id（字母或数字开头）、text、critical（布尔）、needs（fact、data、definition、recent、opinion、counterpoint）、plan（步骤数组：channel 只能是 kb、library、rss、web、academic，另有 query 和 why，可选 recencyDays 与 minAuthority 1、2 或 3）。outOfScope 是字符串数组，可以省略。";

const CHANNEL_ALIAS: Record<string, SourceChannel> = {
  kb: "kb",
  knowledge_base: "kb",
  "knowledge-base": "kb",
  knowledgebase: "kb",
  知识库: "kb",
  library: "library",
  旧文: "library",
  文库: "library",
  rss: "rss",
  feed: "rss",
  web: "web",
  web_search: "web",
  "web-search": "web",
  internet: "web",
  网页: "web",
  academic: "academic",
  scholar: "academic",
  学术: "academic",
};

const NEED_ALIAS: Record<string, Need> = {
  fact: "fact",
  facts: "fact",
  事实: "fact",
  data: "data",
  数据: "data",
  definition: "definition",
  定义: "definition",
  recent: "recent",
  近期: "recent",
  news: "recent",
  opinion: "opinion",
  观点: "opinion",
  counterpoint: "counterpoint",
  counter: "counterpoint",
  反面: "counterpoint",
};

const UNIMPLEMENTED = new Set<SourceChannel>(["web", "academic"]);

export function isQuestionId(value: string): boolean {
  return QuestionIdSchema.safeParse(value).success;
}

export function hasQuestion(plan: ResearchPlan, questionId: string): boolean {
  return plan.questions.some((question) => question.id === questionId);
}

/** 只把还没对上证据的关键问题记成缺口。web / academic 没有检索实现。 */
export function gapsFor(plan: ResearchPlan, answered: ReadonlySet<string>): string[] {
  const gaps: string[] = [];
  for (const question of plan.questions) {
    if (!question.critical || answered.has(question.id)) continue;
    const channels = question.plan.map((step) => step.channel);
    const unimplemented =
      channels.length > 0 && channels.every((channel) => UNIMPLEMENTED.has(channel));
    gaps.push(
      unimplemented
        ? `${question.id}: M1 不检索 web 或 academic`
        : `${question.id}: 尚未有通过校验的证据`,
    );
  }
  return gaps;
}

export type AcceptedPlan =
  | { ok: true; plan: ResearchPlan; gaps: string[] }
  | { ok: false; issues: string[] };

/**
 * 计划正文由模型写。这里只检查结构和问题 id，不重排信源。
 * 代码围栏、缺省的 outOfScope、布尔和枚举的常见写法会先收成同一结构。
 * 查询正文只按上限截断，不改措辞。
 */
export function acceptPlan(input: unknown): AcceptedPlan {
  const raw = typeof input === "string" ? input : stableJson(input);
  let value = input;
  if (typeof input === "string") {
    try {
      value = unwrapModelJson(input);
    } catch {
      return { ok: false, issues: [planIssue("不是 JSON", [], raw)] };
    }
  }
  if (typeof value === "string") {
    try {
      value = unwrapModelJson(value);
    } catch {
      return { ok: false, issues: [planIssue("不是 JSON", [], raw)] };
    }
  }
  const parsed = ResearchPlanSchema.safeParse(coercePlan(value));
  if (!parsed.success) {
    return { ok: false, issues: [planIssue("格式", parsed.error.issues, raw)] };
  }
  const seen = new Set<string>();
  const issues: string[] = [];
  for (const question of parsed.data.questions) {
    if (seen.has(question.id)) issues.push(`问题 id 重复：${question.id}`);
    seen.add(question.id);
  }
  if (issues.length > 0) return { ok: false, issues };
  return { ok: true, plan: parsed.data, gaps: gapsFor(parsed.data, new Set()) };
}

export function parseResearchPlan(input: unknown): ResearchPlan {
  const accepted = acceptPlan(input);
  if (!accepted.ok) throw new Error(accepted.issues.join("；"));
  return accepted.plan;
}

/**
 * 测试和 faux 回合里的模型替身。它只复述指令，不拆问题。
 * 先写知识库是这只替身的选择，和提示词里的建议一致，不是代码里的检索顺序。
 */
export function fauxResearchPlan(instruction: string): ResearchPlan {
  const text = instruction.trim().slice(0, 200) || "待查的问题";
  return {
    thesisDraft: text,
    questions: [
      {
        id: "q1",
        text,
        critical: true,
        needs: ["fact"],
        plan: [{ channel: "kb", query: text, why: "先在知识库里找原文" }],
      },
    ],
    outOfScope: [],
  };
}

export interface StoredResearchPlan {
  id: string;
  turnId: string;
  sessionId: string;
  plan: ResearchPlan;
  gaps: string[];
  createdAt: string;
}

function asJson(value: unknown): unknown {
  if (typeof value !== "string") return value;
  return JSON.parse(value) as unknown;
}

function planIssue(
  kind: string,
  issues: ReadonlyArray<{ path: ReadonlyArray<PropertyKey>; message: string }>,
  raw: string,
): string {
  const paths =
    issues.length === 0
      ? kind
      : issues
          .slice(0, 4)
          .map((issue) => {
            const path = issue.path.length > 0 ? issue.path.map(String).join(".") : "root";
            return `${path}：${issue.message}`;
          })
          .join("；");
  const text = `检索计划格式不合法（${paths}）：${excerptOf(raw)}`;
  return text.length <= 500 ? text : `${text.slice(0, 500)}…`;
}

function excerptOf(text: string): string {
  const flat = text.replace(/\s+/g, " ").trim();
  const redacted = flat
    .replace(/\bsk-[A-Za-z0-9_-]{8,}/g, "[redacted]")
    .replace(/\bAIza[0-9A-Za-z_-]{8,}/g, "[redacted]")
    .replace(/bearer\s+\S+/gi, "bearer [redacted]");
  return redacted.length <= 180 ? redacted : `${redacted.slice(0, 180)}…`;
}

function stableJson(value: unknown): string {
  try {
    return JSON.stringify(value);
  } catch {
    return "";
  }
}

function unwrapModelJson(input: string): unknown {
  let current: unknown = input.trim();
  for (let depth = 0; depth < 2 && typeof current === "string"; depth += 1) {
    const body = stripFence(current);
    try {
      current = JSON.parse(body) as unknown;
    } catch {
      current = JSON.parse(sliceJson(body)) as unknown;
    }
  }
  return current;
}

function stripFence(text: string): string {
  const fenced = /^```(?:json|JSON)?[^\n]*\n?([\s\S]*?)\n?```$/.exec(text.trim());
  return (fenced?.[1] ?? text).trim();
}

function sliceJson(body: string): string {
  const objectAt = body.indexOf("{");
  const arrayAt = body.indexOf("[");
  const start = objectAt < 0 ? arrayAt : arrayAt < 0 ? objectAt : Math.min(objectAt, arrayAt);
  if (start < 0) throw new Error("不是 JSON");
  const end = body.lastIndexOf(body[start] === "{" ? "}" : "]");
  if (end <= start) throw new Error("不是 JSON");
  return body.slice(start, end + 1);
}

function coercePlan(value: unknown): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const plan = value as Record<string, unknown>;
  return {
    ...plan,
    thesisDraft: clipText(plan.thesisDraft, 2000),
    questions: coerceQuestions(plan.questions),
    outOfScope: coerceScope(plan.outOfScope),
  };
}

function coerceQuestions(value: unknown): unknown {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    return [coerceQuestion(value, 0)];
  }
  if (!Array.isArray(value)) return value;
  return value.slice(0, 12).map((item, index) => coerceQuestion(item, index));
}

function coerceQuestion(value: unknown, index: number): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const question = value as Record<string, unknown>;
  const critical = coerceCritical(question.critical);
  const needs = coerceNeeds(question.needs);
  return {
    ...question,
    id: coerceId(question.id, index),
    text: clipText(question.text, 500),
    ...(critical === undefined ? {} : { critical }),
    ...(needs === undefined ? {} : { needs }),
    plan: coerceSteps(question.plan),
  };
}

function coerceSteps(value: unknown): unknown {
  if (value && typeof value === "object" && !Array.isArray(value)) return [coerceStep(value)];
  if (!Array.isArray(value)) return value;
  return value.slice(0, 8).map((item) => coerceStep(item));
}

function coerceStep(value: unknown): unknown {
  if (!value || typeof value !== "object" || Array.isArray(value)) return value;
  const step = { ...(value as Record<string, unknown>) };
  const channel = coerceChannel(step.channel);
  if (channel) step.channel = channel;
  step.query = clipText(step.query, 300);
  step.why = clipText(step.why, 300);
  if (!("recencyDays" in step) || coerceRecency(step.recencyDays) === undefined) {
    delete step.recencyDays;
  } else {
    step.recencyDays = coerceRecency(step.recencyDays);
  }
  const authority = coerceAuthority(step.minAuthority);
  if (authority === undefined) delete step.minAuthority;
  else step.minAuthority = authority;
  return step;
}

function coerceScope(value: unknown): string[] {
  if (value === undefined || value === null) return [];
  const list = Array.isArray(value) ? value : [value];
  return list
    .slice(0, 20)
    .map((item) => clipText(item, 500))
    .filter((item): item is string => typeof item === "string" && item.length > 0);
}

function coerceId(value: unknown, index: number): string {
  const raw = typeof value === "string" ? value.trim() : "";
  if (QuestionIdSchema.safeParse(raw).success) return raw;
  const slug = raw
    .replace(/[^A-Za-z0-9_.-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
  if (QuestionIdSchema.safeParse(slug).success) return slug;
  return `q${index + 1}`;
}

function coerceCritical(value: unknown): boolean | undefined {
  if (typeof value === "boolean") return value;
  if (value === 1 || value === "1" || value === "true" || value === "yes" || value === "是") {
    return true;
  }
  if (value === 0 || value === "0" || value === "false" || value === "no" || value === "否") {
    return false;
  }
  return undefined;
}

function coerceNeeds(value: unknown): unknown {
  if (value === undefined) return undefined;
  const list = Array.isArray(value) ? value : [value];
  return list.slice(0, 6).map((item) => {
    if (typeof item !== "string") return item;
    const trimmed = item.trim();
    return NEED_ALIAS[trimmed] ?? NEED_ALIAS[trimmed.toLowerCase()] ?? item;
  });
}

function coerceChannel(value: unknown): SourceChannel | undefined {
  if (typeof value !== "string") return undefined;
  const trimmed = value.trim();
  return CHANNEL_ALIAS[trimmed] ?? CHANNEL_ALIAS[trimmed.toLowerCase()];
}

function coerceRecency(value: unknown): number | undefined {
  const parsed = asInt(value);
  if (parsed === undefined || parsed <= 0 || parsed > 3650) return undefined;
  return parsed;
}

function coerceAuthority(value: unknown): 1 | 2 | 3 | undefined {
  if (value === 1 || value === 2 || value === 3) return value;
  if (typeof value === "string") {
    const text = value.trim().toLowerCase();
    if (text === "1" || text === "low" || value.trim() === "低") return 1;
    if (text === "2" || text === "medium" || value.trim() === "中") return 2;
    if (text === "3" || text === "high" || value.trim() === "高") return 3;
  }
  const parsed = asInt(value);
  if (parsed === 1 || parsed === 2 || parsed === 3) return parsed;
  return undefined;
}

function asInt(value: unknown): number | undefined {
  if (typeof value === "number" && Number.isFinite(value)) return Math.trunc(value);
  if (typeof value !== "string") return undefined;
  const matched = /^-?\d+/.exec(value.trim());
  if (!matched) return undefined;
  return Number(matched[0]);
}

function clipText(value: unknown, max: number): unknown {
  if (typeof value !== "string") return value;
  const trimmed = value.trim();
  return trimmed.length <= max ? trimmed : trimmed.slice(0, max);
}

/** H2 直接读 research_plans 的 jsonb。读的时候再过一遍结构，不重新计算缺口。 */
export function readStoredPlan(row: {
  id: string;
  turnId: string;
  sessionId: string;
  plan: unknown;
  gaps: unknown;
  createdAt: string;
}): StoredResearchPlan {
  const gaps = z.array(z.string()).parse(asJson(row.gaps));
  return {
    id: row.id,
    turnId: row.turnId,
    sessionId: row.sessionId,
    plan: parseResearchPlan(asJson(row.plan)),
    gaps,
    createdAt: row.createdAt,
  };
}
