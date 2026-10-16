import {
  asBlockId,
  type BlockId,
  type ContentHash,
  type EditOp,
  type OpError,
  type OpErrorCode,
} from "@luminwrite/contracts";

import { blockHashSync } from "./hash.js";
import { markdownToBlocks } from "./markdown.js";
import {
  blockIdOf,
  blockText,
  cloneDoc,
  findBlock,
  type PMJSON,
  type PMNode,
  requireParentContent,
  setBlockText,
} from "./tree.js";

export interface ApplyOk {
  ok: true;
  doc: PMJSON;
  changed: BlockId[];
  created: BlockId[];
}

export interface ApplyFail {
  ok: false;
  errors: OpError[];
}

export type ApplyResult = ApplyOk | ApplyFail;

export interface ApplyOptions {
  scope?: BlockId[];
}

function fail(index: number, code: OpErrorCode, message: string): ApplyFail {
  return { ok: false, errors: [{ index, code, message }] };
}

function countOccurrences(haystack: string, needle: string): number {
  if (needle.length === 0) return 0;
  let count = 0;
  let from = 0;
  while (from <= haystack.length) {
    const at = haystack.indexOf(needle, from);
    if (at < 0) break;
    count += 1;
    from = at + needle.length;
  }
  return count;
}

function mutatedBlock(op: EditOp): BlockId | undefined {
  switch (op.op) {
    case "insert_block":
      return undefined;
    default:
      return op.blockId;
  }
}

function inScope(scope: BlockId[] | undefined, blockId: BlockId | null | undefined): boolean {
  if (!scope) return true;
  if (!blockId) return false;
  return scope.includes(blockId);
}

function checkScope(
  op: EditOp,
  scope: BlockId[] | undefined,
  index: number,
): ApplyFail | undefined {
  if (!scope) return undefined;
  if (op.op === "insert_block") {
    if (!inScope(scope, op.afterBlockId) && !inScope(scope, op.parentBlockId)) {
      return fail(index, "out_of_scope", "插入位置不在本次修订范围内");
    }
    return undefined;
  }
  if (!inScope(scope, op.blockId)) {
    return fail(index, "out_of_scope", `块 ${op.blockId} 不在本次修订范围内`);
  }
  return undefined;
}

function replaceOnce(text: string, expected: string, next: string): string {
  const at = text.indexOf(expected);
  return text.slice(0, at) + next + text.slice(at + expected.length);
}

export function applyOps(doc: PMJSON, ops: EditOp[], opts?: ApplyOptions): ApplyResult {
  const seen = new Map<BlockId, number>();
  for (let index = 0; index < ops.length; index += 1) {
    const op = ops[index];
    if (!op) continue;
    const target = mutatedBlock(op);
    if (!target) continue;
    const first = seen.get(target);
    if (first !== undefined) {
      return fail(
        index,
        "overlap",
        `同一批修改里块 ${target} 被改了多次（首次出现在第 ${first} 条）`,
      );
    }
    seen.set(target, index);
  }

  const next = cloneDoc(doc);
  const changed: BlockId[] = [];
  const created: BlockId[] = [];

  for (let index = 0; index < ops.length; index += 1) {
    const op = ops[index];
    if (!op) continue;
    const scoped = checkScope(op, opts?.scope, index);
    if (scoped) return scoped;
    const result = applyOne(next, op, index, changed, created);
    if (result) return result;
  }

  return { ok: true, doc: next, changed, created };
}

function applyOne(
  doc: PMJSON,
  op: EditOp,
  index: number,
  changed: BlockId[],
  created: BlockId[],
): ApplyFail | undefined {
  switch (op.op) {
    case "replace_text":
      return replaceText(doc, op, index, changed);
    case "replace_block":
      return replaceBlock(doc, op, index, changed);
    case "insert_block":
      return insertBlock(doc, op, index, created);
    case "delete_block":
      return deleteBlock(doc, op, index, changed);
    case "move_block":
      return moveBlock(doc, op, index, changed);
    case "set_attr":
      return setAttr(doc, op, index, changed);
    default:
      return fail(index, "schema_invalid", "不支持的操作");
  }
}

function hashError(index: number, block: PMNode, expected: ContentHash): ApplyFail | undefined {
  const actual = blockHashSync(block);
  if (actual !== expected) {
    return fail(index, "hash_mismatch", `块内容已变化，当前哈希是 ${actual}`);
  }
  return undefined;
}

function replaceText(
  doc: PMJSON,
  op: Extract<EditOp, { op: "replace_text" }>,
  index: number,
  changed: BlockId[],
): ApplyFail | undefined {
  const found = findBlock(doc, op.blockId);
  if (!found) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  const mismatched = hashError(index, found.node, op.contentHash);
  if (mismatched) return mismatched;
  if (op.expectedOldText === op.newText) {
    return fail(index, "noop", "新旧文本相同");
  }
  const text = blockText(found.node);
  const count = countOccurrences(text, op.expectedOldText);
  if (count === 0) return fail(index, "old_text_not_found", "预期原文不在该块中");
  if (count > 1) return fail(index, "old_text_ambiguous", "预期原文在该块中出现了多次");
  setBlockText(found.node, replaceOnce(text, op.expectedOldText, op.newText));
  changed.push(op.blockId);
  return undefined;
}

function replaceBlock(
  doc: PMJSON,
  op: Extract<EditOp, { op: "replace_block" }>,
  index: number,
  changed: BlockId[],
): ApplyFail | undefined {
  const found = findBlock(doc, op.blockId);
  if (!found) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  const mismatched = hashError(index, found.node, op.contentHash);
  if (mismatched) return mismatched;
  const text = blockText(found.node);
  if (text !== op.expectedOldText) {
    return fail(index, "old_text_not_found", "replace_block 要求 expectedOldText 等于整块文本");
  }
  const blocks = markdownToBlocks(op.newMarkdown, () => op.blockId);
  const replacement = blocks[0];
  if (!replacement || blocks.length !== 1) {
    return fail(index, "schema_invalid", "replace_block 的 markdown 必须正好是一块");
  }
  replacement.attrs = { ...replacement.attrs, id: op.blockId };
  const content = requireParentContent(found.parent);
  content[found.index] = replacement;
  changed.push(op.blockId);
  return undefined;
}

function insertBlock(
  doc: PMJSON,
  op: Extract<EditOp, { op: "insert_block" }>,
  index: number,
  created: BlockId[],
): ApplyFail | undefined {
  const blocks = markdownToBlocks(op.newMarkdown);
  if (blocks.length === 0) return fail(index, "schema_invalid", "没有可插入的块");
  let parent = doc;
  let at = 0;
  if (op.afterBlockId) {
    const found = findBlock(doc, op.afterBlockId);
    if (!found) return fail(index, "block_not_found", `找不到锚点块 ${op.afterBlockId}`);
    parent = found.parent;
    at = found.index + 1;
  } else if (op.parentBlockId) {
    const found = findBlock(doc, op.parentBlockId);
    if (!found) return fail(index, "block_not_found", `找不到父块 ${op.parentBlockId}`);
    parent = found.node;
    at = (found.node.content ?? []).length;
  }
  const content = requireParentContent(parent);
  content.splice(at, 0, ...blocks);
  for (const block of blocks) {
    const id = blockIdOf(block);
    if (id) created.push(id);
  }
  return undefined;
}

function deleteBlock(
  doc: PMJSON,
  op: Extract<EditOp, { op: "delete_block" }>,
  index: number,
  changed: BlockId[],
): ApplyFail | undefined {
  const found = findBlock(doc, op.blockId);
  if (!found) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  const mismatched = hashError(index, found.node, op.contentHash);
  if (mismatched) return mismatched;
  if (blockText(found.node) !== op.expectedOldText) {
    return fail(index, "old_text_not_found", "delete_block 要求 expectedOldText 等于整块文本");
  }
  requireParentContent(found.parent).splice(found.index, 1);
  changed.push(op.blockId);
  return undefined;
}

function moveBlock(
  doc: PMJSON,
  op: Extract<EditOp, { op: "move_block" }>,
  index: number,
  changed: BlockId[],
): ApplyFail | undefined {
  const found = findBlock(doc, op.blockId);
  if (!found) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  const mismatched = hashError(index, found.node, op.contentHash);
  if (mismatched) return mismatched;
  if (op.afterBlockId === op.blockId) return fail(index, "schema_invalid", "不能把块移到自己后面");
  const content = requireParentContent(found.parent);
  const [node] = content.splice(found.index, 1);
  if (!node) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  if (op.afterBlockId && findBlock(node, op.afterBlockId)) {
    return fail(index, "schema_invalid", "不能把块移到自己的子孙后面");
  }
  let parent = doc;
  let at = 0;
  if (op.afterBlockId) {
    const anchor = findBlock(doc, op.afterBlockId);
    if (!anchor) return fail(index, "block_not_found", `找不到锚点块 ${op.afterBlockId}`);
    parent = anchor.parent;
    at = anchor.index + 1;
  }
  requireParentContent(parent).splice(at, 0, node);
  changed.push(op.blockId);
  return undefined;
}

function setAttr(
  doc: PMJSON,
  op: Extract<EditOp, { op: "set_attr" }>,
  index: number,
  changed: BlockId[],
): ApplyFail | undefined {
  const found = findBlock(doc, op.blockId);
  if (!found) return fail(index, "block_not_found", `找不到块 ${op.blockId}`);
  const mismatched = hashError(index, found.node, op.contentHash);
  if (mismatched) return mismatched;
  if ("id" in op.attrs) return fail(index, "schema_invalid", "不能通过 set_attr 修改块 id");
  found.node.attrs = { ...found.node.attrs, ...op.attrs };
  changed.push(op.blockId);
  return undefined;
}

export function rebaseOp(op: EditOp, current: PMNode): EditOp | null {
  if (op.op === "insert_block") return op;
  const id = blockIdOf(current);
  if (!id || id !== op.blockId) return null;
  const hash = blockHashSync(current);
  if (hash === op.contentHash) return op;
  const text = blockText(current);
  if (op.op === "replace_text") {
    if (countOccurrences(text, op.expectedOldText) !== 1) return null;
    return { ...op, contentHash: hash };
  }
  if (op.op === "replace_block" || op.op === "delete_block") {
    if (text !== op.expectedOldText) return null;
    return { ...op, contentHash: hash };
  }
  return null;
}

export function emptyDoc(): PMJSON {
  return { type: "doc", content: [] };
}

export function docFromParagraphs(paragraphs: string[], ids?: BlockId[]): PMJSON {
  return {
    type: "doc",
    content: paragraphs.map((text, index) => ({
      type: "paragraph",
      attrs: { id: ids?.[index] ?? newBlockIdFrom(index, ids) },
      content: [{ type: "text", text }],
    })),
  };
}

function newBlockIdFrom(index: number, ids: BlockId[] | undefined): BlockId {
  const given = ids?.[index];
  if (given) return given;
  return asBlockId(`blk_${"p".repeat(8)}${String(index).padStart(8, "0")}`);
}

/** 拆段：原 id 留在前半，后半拿新 id。对齐 Tiptap UniqueID 的文档行为。 */
export function splitBlock(doc: PMJSON, blockId: BlockId, charOffset: number): ApplyResult {
  const found = findBlock(doc, blockId);
  if (!found) return fail(0, "block_not_found", `找不到块 ${blockId}`);
  const text = blockText(found.node);
  if (charOffset <= 0 || charOffset >= text.length) {
    return fail(0, "schema_invalid", "拆分位置必须落在文本内部");
  }
  const next = cloneDoc(doc);
  const again = findBlock(next, blockId);
  if (!again) return fail(0, "block_not_found", `找不到块 ${blockId}`);
  const newId = asBlockId(`blk_split${String(charOffset).padStart(11, "0")}`);
  setBlockText(again.node, text.slice(0, charOffset));
  const sibling: PMNode = {
    type: again.node.type,
    attrs: { ...again.node.attrs, id: newId },
    content: [{ type: "text", text: text.slice(charOffset) }],
  };
  requireParentContent(again.parent).splice(again.index + 1, 0, sibling);
  return { ok: true, doc: next, changed: [blockId], created: [newId] };
}

/** 合并相邻块：保留前一块的 id。 */
export function mergeBlocks(doc: PMJSON, firstId: BlockId, secondId: BlockId): ApplyResult {
  const first = findBlock(doc, firstId);
  const second = findBlock(doc, secondId);
  if (!first || !second) return fail(0, "block_not_found", "找不到要合并的块");
  if (first.parent !== second.parent || second.index !== first.index + 1) {
    return fail(0, "schema_invalid", "只能合并同一个父节点下相邻的块");
  }
  const next = cloneDoc(doc);
  const left = findBlock(next, firstId);
  const right = findBlock(next, secondId);
  if (!left || !right) return fail(0, "block_not_found", "找不到要合并的块");
  setBlockText(left.node, blockText(left.node) + blockText(right.node));
  requireParentContent(right.parent).splice(right.index, 1);
  return { ok: true, doc: next, changed: [firstId], created: [] };
}
