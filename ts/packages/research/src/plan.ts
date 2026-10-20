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
export type SubQuestion = z.infer<typeof SubQuestionSchema>;
export type ResearchPlan = z.infer<typeof ResearchPlanSchema>;

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

/** 计划正文由模型写。这里只检查结构和问题 id，不改查询、不重排信源。 */
export function acceptPlan(input: unknown): AcceptedPlan {
  const parsed = ResearchPlanSchema.safeParse(input);
  if (!parsed.success) return { ok: false, issues: ["检索计划格式不合法"] };
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
