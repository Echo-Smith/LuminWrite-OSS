import type { PMJSON, PMNode } from "./tree.js";
import { newBlockId } from "./tree.js";

/**
 * 派生文档复制正文时给每个块换新 id。
 * 哈希不含块 id，所以相同文本的哈希仍相同；锚点带块 id，因此 A 的修订不能命中 B。
 */
export function remapBlockIds(doc: PMJSON): PMJSON {
  const walk = (node: PMNode): PMNode => {
    const next: PMNode = { type: node.type };
    if (node.attrs) next.attrs = { ...node.attrs };
    if (node.text !== undefined) next.text = node.text;
    if (node.marks) next.marks = node.marks.map((mark) => ({ ...mark, attrs: mark.attrs }));
    const id = node.attrs?.id;
    if (typeof id === "string" && id.startsWith("blk_")) {
      next.attrs = { ...next.attrs, id: newBlockId() };
    }
    if (node.content) next.content = node.content.map(walk);
    return next;
  };
  return walk(structuredClone(doc));
}
