/**
 * 知识库检索还没做。索引和检索只收已保存且未丢弃的文档，
 * 判断在 `@luminwrite/db` 的 `documentVisibleToKb`（saved_at 非空且 discarded_at 为空）。
 */
export const phase = "stub" as const;
export const note =
  "知识库检索还没做。索引时调用 documentVisibleToKb：已保存且未丢弃才进库，未保存和已丢弃的不进检索。";
