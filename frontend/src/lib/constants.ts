/**
 * Agent Step 标签和图标映射
 */
import type { AgentStepName } from "./types";

export const STEP_LABELS: Record<AgentStepName, string> = {
  intent: "意图识别",
  query_plan: "检索规划",
  search: "多源检索",
  relevance: "素材过滤",
  outline: "结构规划",
  write: "文章生成",
  post_review: "写后自检",
  auto_fix: "自动修正",
  memory_gate: "记忆检索",
  memory_extract: "记忆提取",
  chat: "对话回复",
  parallel_pre_write: "并行预处理",
};

export const STEP_DESCRIPTIONS: Record<AgentStepName, string> = {
  intent: "分析用户意图，确定写作任务类型",
  query_plan: "规划检索策略，生成搜索关键词",
  search: "并发检索知乎、IMA知识库、全网等多个信源",
  relevance: "对检索素材打分过滤，语义去重",
  outline: "生成文章提纲，等待用户确认或修改",
  write: "按风格Profile生成文章，流式输出",
  post_review: "质量评分、敏感检查、篇幅校验",
  auto_fix: "自动修正低严重度问题",
  memory_gate: "检索用户写作偏好，注入上下文",
  memory_extract: "从写作结果中提取用户偏好模式",
  chat: "对话式回复，无需写作流程",
  parallel_pre_write: "记忆检索与多源检索并行执行",
};

export const STEP_ICONS: Record<AgentStepName, string> = {
  intent: "Brain",
  query_plan: "Search",
  search: "Globe",
  relevance: "Filter",
  outline: "ListTree",
  write: "PenLine",
  post_review: "ShieldCheck",
  auto_fix: "Wrench",
  memory_gate: "Database",
  memory_extract: "Sparkles",
  chat: "MessageCircle",
  parallel_pre_write: "Split",
};
