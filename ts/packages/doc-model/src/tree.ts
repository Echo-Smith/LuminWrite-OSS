import { asBlockId, type BlockId } from "@luminwrite/contracts";
import { nanoid } from "nanoid";

export interface PMMark {
  type: string;
  attrs?: Record<string, unknown>;
}

/** ProseMirror JSON 的最小子集。块 id 放在 attrs.id。 */
export interface PMNode {
  type: string;
  attrs?: Record<string, unknown>;
  content?: PMNode[];
  text?: string;
  marks?: PMMark[];
}

export type PMJSON = PMNode;

export const BLOCK_TYPES = new Set([
  "paragraph",
  "heading",
  "blockquote",
  "codeBlock",
  "listItem",
  "bulletList",
  "orderedList",
  "table",
  "horizontalRule",
]);

export function newBlockId(): BlockId {
  return asBlockId(`blk_${nanoid(16)}`);
}

export function blockIdOf(node: PMNode): BlockId | undefined {
  const id = node.attrs?.id;
  if (typeof id !== "string" || !id.startsWith("blk_")) return undefined;
  return id as BlockId;
}

export function blockText(node: PMNode): string {
  if (typeof node.text === "string") return node.text;
  let text = "";
  for (const child of node.content ?? []) {
    text += blockText(child);
  }
  return text;
}

export interface BlockRef {
  node: PMNode;
  parent: PMNode;
  index: number;
}

export function findBlock(doc: PMJSON, blockId: BlockId): BlockRef | undefined {
  const walk = (node: PMNode): BlockRef | undefined => {
    const children = node.content ?? [];
    for (let index = 0; index < children.length; index += 1) {
      const child = children[index];
      if (!child) continue;
      if (blockIdOf(child) === blockId) return { node: child, parent: node, index };
      const nested = walk(child);
      if (nested) return nested;
    }
    return undefined;
  };
  return walk(doc);
}

export function listBlocks(doc: PMJSON): PMNode[] {
  const blocks: PMNode[] = [];
  const walk = (node: PMNode): void => {
    if (BLOCK_TYPES.has(node.type) && blockIdOf(node)) blocks.push(node);
    for (const child of node.content ?? []) walk(child);
  };
  walk(doc);
  return blocks;
}

export function cloneDoc<T>(value: T): T {
  return structuredClone(value);
}

export function setBlockText(node: PMNode, text: string): void {
  node.content = [{ type: "text", text }];
}

export interface Located {
  node: PMNode;
  parent: PMNode;
  index: number;
}

export function requireParentContent(parent: PMNode): PMNode[] {
  if (!parent.content) parent.content = [];
  return parent.content;
}
