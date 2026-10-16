import type { BlockId, EditOp } from "@luminwrite/contracts";
import { diffArrays } from "diff";

import { blockHashSync } from "./hash.js";
import { blockIdOf, blockText, listBlocks, type PMJSON, type PMNode } from "./tree.js";

export interface TokenChange {
  type: "equal" | "insert" | "delete";
  value: string;
}

export interface BlockDiff {
  blockId: BlockId | null;
  kind: "unchanged" | "added" | "removed" | "modified";
  before?: string;
  after?: string;
  tokens?: TokenChange[];
}

/**
 * 块内按“词”切：拉丁字母数字成词，每个汉字单独成词。
 * doc-model 不能依赖 Node 专属的 jieba，分词器保持可替换。
 */
export function tokenize(text: string): string[] {
  const tokens: string[] = [];
  const re = /[A-Za-z0-9_]+|\s+|[^\sA-Za-z0-9_]/gu;
  for (const match of text.matchAll(re)) {
    const value = match[0];
    if (value) tokens.push(value);
  }
  return tokens;
}

function toTokenChanges(
  changes: ReadonlyArray<{ added?: boolean; removed?: boolean; value: string[] }>,
): TokenChange[] {
  const tokens: TokenChange[] = [];
  for (const change of changes) {
    const type = change.added ? "insert" : change.removed ? "delete" : "equal";
    for (const value of change.value) {
      if (value) tokens.push({ type, value });
    }
  }
  return tokens;
}

export function diffBlocks(a: PMJSON, b: PMJSON): BlockDiff[] {
  const left = listBlocks(a);
  const right = listBlocks(b);
  const rightById = new Map<BlockId, PMNode>();
  for (const node of right) {
    const id = blockIdOf(node);
    if (id) rightById.set(id, node);
  }
  const leftIds = new Set<BlockId>();
  const diffs: BlockDiff[] = [];
  for (const node of left) {
    const id = blockIdOf(node);
    if (!id) continue;
    leftIds.add(id);
    const other = rightById.get(id);
    const before = blockText(node);
    if (!other) {
      diffs.push({ blockId: id, kind: "removed", before });
      continue;
    }
    const after = blockText(other);
    if (before === after && blockHashSync(node) === blockHashSync(other)) {
      diffs.push({ blockId: id, kind: "unchanged", before, after });
      continue;
    }
    const tokens = toTokenChanges(diffArrays(tokenize(before), tokenize(after)));
    diffs.push({ blockId: id, kind: "modified", before, after, tokens });
  }
  for (const node of right) {
    const id = blockIdOf(node);
    if (!id || leftIds.has(id)) continue;
    diffs.push({ blockId: id, kind: "added", after: blockText(node) });
  }
  return diffs;
}

/** 把两版文档折成 EditOp，供版本日志和以后的偏好学习使用。 */
export function manualOpsFromSteps(before: PMJSON, after: PMJSON): EditOp[] {
  const left = new Map<BlockId, PMNode>();
  const right: PMNode[] = [];
  for (const node of listBlocks(before)) {
    const id = blockIdOf(node);
    if (id) left.set(id, node);
  }
  for (const node of listBlocks(after)) right.push(node);

  const ops: EditOp[] = [];
  const rightIds = new Set<BlockId>();
  for (const node of right) {
    const id = blockIdOf(node);
    if (id) rightIds.add(id);
  }
  for (const [id, node] of left) {
    if (rightIds.has(id)) continue;
    ops.push({
      op: "delete_block",
      blockId: id,
      contentHash: blockHashSync(node),
      expectedOldText: blockText(node),
      reason: "manual",
    });
  }
  for (const node of right) {
    const id = blockIdOf(node);
    if (!id) continue;
    const prev = left.get(id);
    if (!prev) continue;
    const beforeText = blockText(prev);
    const afterText = blockText(node);
    if (beforeText === afterText) continue;
    ops.push({
      op: "replace_text",
      blockId: id,
      contentHash: blockHashSync(prev),
      expectedOldText: beforeText,
      newText: afterText,
      reason: "manual",
    });
  }
  let previous: BlockId | null = null;
  for (const node of right) {
    const id = blockIdOf(node);
    if (!id) continue;
    if (!left.has(id)) {
      const text = blockText(node);
      const markdown =
        node.type === "heading" ? `${"#".repeat(Number(node.attrs?.level ?? 1))} ${text}` : text;
      ops.push({
        op: "insert_block",
        afterBlockId: previous,
        newMarkdown: markdown,
        reason: "manual",
      });
    }
    previous = id;
  }
  return ops;
}
