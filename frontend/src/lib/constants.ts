/**
 * Agent Step 标签和图标映射
 */
import type { AgentStepName } from "./types";

export const STEP_LABELS: Record<AgentStepName, string> = {
  // ── Harness 模式（架构 C）──
  search_web: "联网搜索",
  read_source: "读取全文",
  write_article: "文章生成",
  review_article: "质量评审",
  revise_section: "定向修正",
  word_count_check: "字数检查",
  rewrite_title: "标题优化",
  fact_check: "事实核查",
  // ── 旧 pipeline/unified 模式（兼容历史会话）──
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
  // ── Harness 模式（架构 C）──
  search_web: "搜索网络获取写作素材或回答问题",
  read_source: "读取搜索结果的完整内容",
  write_article: "LLM 自主写作，流式输出文章",
  review_article: "多维度质量评审：事实/结构/风格/修辞/安全",
  revise_section: "定向修改文章某一部分",
  word_count_check: "检查文章字数是否符合风格要求",
  rewrite_title: "生成备选标题供选择",
  fact_check: "提取并验证文章中的关键事实",
  // ── 旧 pipeline/unified 模式（兼容历史会话）──
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
  // ── Harness 模式（架构 C）──
  search_web: "Globe",
  read_source: "FileText",
  write_article: "PenLine",
  review_article: "ShieldCheck",
  revise_section: "Wrench",
  word_count_check: "Hash",
  rewrite_title: "Type",
  fact_check: "SearchCheck",
  // ── 旧 pipeline/unified 模式（兼容历史会话）──
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
